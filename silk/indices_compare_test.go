//go:build cgo_libopus

package silk

import (
	"encoding/binary"
	"os"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/rangecoding"
)

func loadPacketsSimple(t *testing.T, bitFile string, maxPackets int) [][]byte {
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

func TestSilkIndicesPulsesMatchLibopusFrame2(t *testing.T) {
	bitFile := "../testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPacketsSimple(t, bitFile, 2)
	if len(packets) < 2 {
		t.Skip("not enough packets")
	}

	bandwidth := BandwidthNarrowband
	duration := Frame60ms
	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		t.Fatalf("frameParams: %v", err)
	}
	frameLength := nbSubfr * subFrameLengthMs * fsKHz

	// Decode indices/pulses using Go implementation.
	pkt := packets[1]
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])

	st := &NewDecoder().state[0]
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

	targetFrame := 2
	var goIndices sideInfoIndices
	var goPulses []int16
	for i := 0; i < framesPerPacket; i++ {
		condCoding := codeIndependently
		if i > 0 {
			condCoding = codeConditionally
		}
		vad := st.VADFlags[i] != 0
		silkDecodeIndices(st, &rd, vad, condCoding)
		pulses := make([]int16, roundUpShellFrame(st.frameLength))
		silkDecodePulses(&rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		if i == targetFrame {
			goIndices = st.indices
			goPulses = append([]int16(nil), pulses[:st.frameLength]...)
			break
		}
	}

	libFrame, err := cgowrap.SilkDecodeIndicesPulses(pkt[1:], fsKHz, nbSubfr, framesPerPacket, targetFrame, frameLength)
	if err != nil || libFrame == nil {
		t.Fatalf("libopus decode indices/pulses failed")
	}

	// Compare indices
	if goIndices.signalType != libFrame.SignalType ||
		goIndices.quantOffsetType != libFrame.QuantOffsetType ||
		goIndices.NLSFInterpCoefQ2 != libFrame.NLSFInterpCoef ||
		goIndices.Seed != libFrame.Seed ||
		goIndices.PERIndex != libFrame.PERIndex ||
		goIndices.LTPScaleIndex != libFrame.LTPScaleIndex ||
		goIndices.contourIndex != libFrame.ContourIndex ||
		goIndices.lagIndex != libFrame.LagIndex {
		t.Fatalf("scalar indices mismatch: go=%+v lib=%+v", goIndices, *libFrame)
	}

	for i := 0; i < maxNbSubfr; i++ {
		if goIndices.GainsIndices[i] != libFrame.GainsIndices[i] {
			t.Fatalf("GainsIndices[%d] mismatch: go=%d lib=%d", i, goIndices.GainsIndices[i], libFrame.GainsIndices[i])
		}
		if goIndices.LTPIndex[i] != libFrame.LTPIndex[i] {
			t.Fatalf("LTPIndex[%d] mismatch: go=%d lib=%d", i, goIndices.LTPIndex[i], libFrame.LTPIndex[i])
		}
	}
	for i := 0; i < maxLPCOrder+1; i++ {
		if goIndices.NLSFIndices[i] != libFrame.NLSFIndices[i] {
			t.Fatalf("NLSFIndices[%d] mismatch: go=%d lib=%d", i, goIndices.NLSFIndices[i], libFrame.NLSFIndices[i])
		}
	}

	// Compare pulses
	if len(goPulses) != len(libFrame.Pulses) {
		t.Fatalf("pulses length mismatch: go=%d lib=%d", len(goPulses), len(libFrame.Pulses))
	}
	for i := range goPulses {
		if goPulses[i] != libFrame.Pulses[i] {
			t.Fatalf("pulses[%d] mismatch: go=%d lib=%d", i, goPulses[i], libFrame.Pulses[i])
		}
	}
}
