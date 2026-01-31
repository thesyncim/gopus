//go:build cgo

package silk

import (
	"encoding/binary"
	"os"
	"testing"

	cgowrap "github.com/thesyncim/gopus/internal/celt/cgo_test"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func loadPacketsForCoreCompare(t *testing.T, bitFile string, maxPackets int) [][]byte {
	t.Helper()
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("failed to read bitstream: %v", err)
	}
	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}
	return packets
}

// TestSilkDecodeCoreCompare ensures Go core matches libopus core for a target frame.
func TestSilkDecodeCoreCompareFrame2(t *testing.T) {
	bitFile := "../testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPacketsForCoreCompare(t, bitFile, 2)
	if len(packets) < 2 {
		t.Skip("not enough packets")
	}

	dec := NewDecoder()

	// Decode packet 0 to set state.
	{
		pkt0 := packets[0]
		var rd0 rangecoding.Decoder
		rd0.Init(pkt0[1:])
		if _, err := dec.DecodeFrame(&rd0, BandwidthNarrowband, Frame60ms, true); err != nil {
			t.Fatalf("packet0 decode failed: %v", err)
		}
	}

	// Now manually decode packet 1 and capture frame 2 inputs.
	pkt1 := packets[1]
	var rd rangecoding.Decoder
	rd.Init(pkt1[1:])

	config := GetBandwidthConfig(BandwidthNarrowband)
	fsKHz := config.SampleRate / 1000

	framesPerPacket, nbSubfr, err := frameParams(Frame60ms)
	if err != nil {
		t.Fatalf("frameParams: %v", err)
	}

	st := &dec.state[0]
	st.nFramesDecoded = 0
	st.nFramesPerPacket = framesPerPacket
	st.nbSubfr = nbSubfr
	silkDecoderSetFs(st, fsKHz)

	decodeVADAndLBRRFlags(&rd, st, framesPerPacket)
	if st.LBRRFlag != 0 {
		for i := 0; i < framesPerPacket; i++ {
			if st.LBRRFlags[i] == 0 {
				continue
			}
			condCoding := codeIndependently
			if i > 0 && st.LBRRFlags[i-1] != 0 {
				condCoding = codeConditionally
			}
			silkDecodeIndices(st, &rd, true, condCoding)
			pulses := make([]int16, roundUpShellFrame(st.frameLength))
			silkDecodePulses(&rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		}
	}

	var (
		targetFrame = 2
		outGo       []int16
		outC        []int16
	)

	for i := 0; i < framesPerPacket; i++ {
		frameIndex := st.nFramesDecoded
		condCoding := codeIndependently
		if frameIndex > 0 {
			condCoding = codeConditionally
		}
		vad := st.VADFlags[frameIndex] != 0
		silkDecodeIndices(st, &rd, vad, condCoding)
		pulses := make([]int16, roundUpShellFrame(st.frameLength))
		silkDecodePulses(&rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		var ctrl decoderControl
		silkDecodeParameters(st, &ctrl, condCoding)

		if i == targetFrame {
			// Copy state/control for comparison.
			stCopy := *st
			ctrlCopy := ctrl
			frameOut := make([]int16, st.frameLength)

			// Run Go core on copied state.
			silkDecodeCore(&stCopy, &ctrlCopy, frameOut, pulses)
			outGo = frameOut

			// Run C core with same inputs.
			predCoef := make([]int16, 0, 2*maxLPCOrder)
			predCoef = append(predCoef, ctrl.PredCoefQ12[0][:]...)
			predCoef = append(predCoef, ctrl.PredCoefQ12[1][:]...)

			outC = cgowrap.SilkDecodeCore(
				st.fsKHz, st.nbSubfr, st.frameLength, st.subfrLength, st.ltpMemLength, st.lpcOrder,
				st.prevGainQ16, st.lossCnt, st.prevSignalType,
				st.indices.signalType, st.indices.quantOffsetType, st.indices.NLSFInterpCoefQ2, st.indices.Seed,
				st.outBuf[:], st.sLPCQ14Buf[:],
				ctrl.GainsQ16[:], predCoef, ctrl.LTPCoefQ14[:], ctrl.pitchL[:], ctrl.LTPScaleQ14,
				pulses,
			)
			break
		}

		// Advance state for next frame.
		frameOut := make([]int16, st.frameLength)
		silkDecodeCore(st, &ctrl, frameOut, pulses)
		silkUpdateOutBuf(st, frameOut)
		st.lossCnt = 0
		st.lagPrev = ctrl.pitchL[st.nbSubfr-1]
		st.prevSignalType = int(st.indices.signalType)
		st.firstFrameAfterReset = false
		st.nFramesDecoded++
	}

	if len(outGo) == 0 || len(outC) == 0 {
		t.Fatalf("failed to capture core outputs")
	}
	if len(outGo) != len(outC) {
		t.Fatalf("length mismatch: go=%d c=%d", len(outGo), len(outC))
	}
	for i := range outGo {
		if outGo[i] != outC[i] {
			t.Fatalf("core mismatch at sample %d: go=%d c=%d", i, outGo[i], outC[i])
		}
	}
}
