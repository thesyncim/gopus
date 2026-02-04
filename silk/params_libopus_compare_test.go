//go:build cgo_libopus

package silk

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/rangecoding"
)

func loadPacketsForParams(t *testing.T, bitFile string, maxPackets int) [][]byte {
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

// TestSilkParamsMatchLibopusVector02 compares decoded parameters for the first mismatch packet.
func TestSilkParamsMatchLibopusVector02(t *testing.T) {
	bitFile := filepath.Join("..", "testvectors", "testdata", "opus_testvectors", "testvector02.bit")
	packets := loadPacketsForParams(t, bitFile, 5)
	if len(packets) < 3 {
		t.Skip("not enough packets")
	}

	const targetPkt = 2

	goDec := NewDecoder()
	libState := cgowrap.NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	for pktIdx, pkt := range packets {
		if len(pkt) < 1 {
			continue
		}
		config := pkt[0] >> 3
		if config >= 12 {
			continue // not SILK-only
		}

		bwGroup := config / 4
		var bw Bandwidth
		switch bwGroup {
		case 0:
			bw = BandwidthNarrowband
		case 1:
			bw = BandwidthMediumband
		case 2:
			bw = BandwidthWideband
		default:
			t.Fatalf("unexpected bandwidth group: %d", bwGroup)
		}
		frameSize := []int{480, 960, 1920, 2880}[config%4]
		duration := FrameDurationFromTOC(frameSize)
		framesPerPacket, nbSubfr, err := frameParams(duration)
		if err != nil || framesPerPacket == 0 {
			t.Fatalf("frame params: %v", err)
		}
		fsKHz := GetBandwidthConfig(bw).SampleRate / 1000

		libParams, err := libState.DecodePacketParams(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil || len(libParams) == 0 {
			t.Fatalf("libopus params decode failed: %v", err)
		}

		if pktIdx != targetPkt {
			// Advance state for both decoders.
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			if _, err := goDec.DecodeFrameRaw(&rd, bw, duration, true); err != nil {
				t.Fatalf("gopus DecodeFrameRaw failed (pkt %d): %v", pktIdx, err)
			}
			continue
		}

		// Now decode target packet with parameter comparison.
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])

		st := &goDec.state[0]
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

			lib := libParams[i]
			if int(st.indices.NLSFInterpCoefQ2) != int(lib.NLSFInterpCoefQ2) {
				t.Fatalf("frame %d NLSF interp mismatch: go=%d lib=%d", i, st.indices.NLSFInterpCoefQ2, lib.NLSFInterpCoefQ2)
			}
			for k := 0; k < nbSubfr; k++ {
				if ctrl.GainsQ16[k] != lib.GainsQ16[k] {
					t.Fatalf("frame %d gains mismatch[%d]: go=%d lib=%d", i, k, ctrl.GainsQ16[k], lib.GainsQ16[k])
				}
			}
			for k := 0; k < st.lpcOrder; k++ {
				if ctrl.PredCoefQ12[0][k] != lib.PredCoefQ12[0][k] {
					t.Fatalf("frame %d predCoef0[%d] mismatch: go=%d lib=%d", i, k, ctrl.PredCoefQ12[0][k], lib.PredCoefQ12[0][k])
				}
				if ctrl.PredCoefQ12[1][k] != lib.PredCoefQ12[1][k] {
					t.Fatalf("frame %d predCoef1[%d] mismatch: go=%d lib=%d", i, k, ctrl.PredCoefQ12[1][k], lib.PredCoefQ12[1][k])
				}
			}
			for k := 0; k < nbSubfr; k++ {
				if ctrl.pitchL[k] != int(lib.PitchL[k]) {
					t.Fatalf("frame %d pitchL[%d] mismatch: go=%d lib=%d", i, k, ctrl.pitchL[k], lib.PitchL[k])
				}
			}
			if int32(ctrl.LTPScaleQ14) != lib.LTPScaleQ14 {
				t.Fatalf("frame %d LTPScale mismatch: go=%d lib=%d", i, ctrl.LTPScaleQ14, lib.LTPScaleQ14)
			}

			// Advance state to keep in sync.
			frameOut := make([]int16, st.frameLength)
			silkDecodeCore(st, &ctrl, frameOut, pulses)
			silkUpdateOutBuf(st, frameOut)
			silkPLCGlueFrames(st, frameOut, st.frameLength)
			st.lossCnt = 0
			st.lagPrev = ctrl.pitchL[st.nbSubfr-1]
			st.prevSignalType = int(st.indices.signalType)
			st.firstFrameAfterReset = false
			st.nFramesDecoded++
		}

		break
	}
}
