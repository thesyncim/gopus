//go:build cgo

package silk

import (
	"encoding/binary"
	"os"
	"testing"

	cgowrap "github.com/thesyncim/gopus/internal/celt/cgo_test"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func loadPacketsForParamsCompare(t *testing.T, bitFile string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("failed to read bitstream: %v", err)
	}
	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
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

type frameIndices struct {
	NLSFIndices      [maxLPCOrder + 1]int8
	NLSFInterpCoefQ2 int8
	PrevNLSFQ15      [maxLPCOrder]int16
}

func decodePacketParamsGo(dec *Decoder, rd *rangecoding.Decoder, bandwidth Bandwidth, duration FrameDuration) ([]decoderControl, []frameIndices, error) {
	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	st := &dec.state[0]

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, nil, err
	}

	st.nFramesDecoded = 0
	st.nFramesPerPacket = framesPerPacket
	st.nbSubfr = nbSubfr
	silkDecoderSetFs(st, fsKHz)

	decodeVADAndLBRRFlags(rd, st, framesPerPacket)
	if st.LBRRFlag != 0 {
		for i := 0; i < framesPerPacket; i++ {
			if st.LBRRFlags[i] == 0 {
				continue
			}
			condCoding := codeIndependently
			if i > 0 && st.LBRRFlags[i-1] != 0 {
				condCoding = codeConditionally
			}
			silkDecodeIndices(st, rd, true, condCoding)
			pulses := make([]int16, roundUpShellFrame(st.frameLength))
			silkDecodePulses(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		}
	}

	params := make([]decoderControl, framesPerPacket)
	indices := make([]frameIndices, framesPerPacket)
	for i := 0; i < framesPerPacket; i++ {
		frameIndex := st.nFramesDecoded
		condCoding := codeIndependently
		if frameIndex > 0 {
			condCoding = codeConditionally
		}
		vad := st.VADFlags[frameIndex] != 0
		silkDecodeIndices(st, rd, vad, condCoding)
		pulses := make([]int16, roundUpShellFrame(st.frameLength))
		silkDecodePulses(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		indices[i].PrevNLSFQ15 = st.prevNLSFQ15
		silkDecodeParameters(st, &params[i], condCoding)
		indices[i].NLSFIndices = st.indices.NLSFIndices
		indices[i].NLSFInterpCoefQ2 = st.indices.NLSFInterpCoefQ2

		frameOut := make([]int16, st.frameLength)
		silkDecodeCore(st, &params[i], frameOut, pulses)
		silkUpdateOutBuf(st, frameOut)
		st.lossCnt = 0
		st.lagPrev = params[i].pitchL[st.nbSubfr-1]
		st.prevSignalType = int(st.indices.signalType)
		st.firstFrameAfterReset = false
		st.nFramesDecoded++
	}

	return params, indices, nil
}

// TestSilkParamsFirstMismatch compares decoder control parameters and finds the first mismatch.
func TestSilkParamsFirstMismatch(t *testing.T) {
	bitFile := "../testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPacketsForParamsCompare(t, bitFile)
	if len(packets) == 0 {
		t.Skip("not enough packets")
	}

	goDec := NewDecoder()
	libState := cgowrap.NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	for pktIdx, pkt := range packets {
		config := pkt[0] >> 3
		stereo := (pkt[0] & 0x04) != 0
		if config >= 12 || stereo {
			continue
		}
		bw := int(config / 4) // 0=NB,1=MB,2=WB for SILK
		silkBW, ok := BandwidthFromOpus(bw)
		if !ok {
			t.Fatalf("invalid bandwidth")
		}
		frameSizes := []int{480, 960, 1920, 2880}
		duration := FrameDurationFromTOC(frameSizes[int(config%4)])
		framesPerPacket, nbSubfr, err := frameParams(duration)
		if err != nil {
			t.Fatalf("frameParams: %v", err)
		}
		fsKHz := GetBandwidthConfig(silkBW).SampleRate / 1000

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goParams, goIndices, err := decodePacketParamsGo(goDec, &rd, silkBW, duration)
		if err != nil {
			t.Fatalf("go decode params failed: %v", err)
		}
		libParams, err := libState.DecodePacketParams(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil {
			t.Fatalf("libopus decode params failed: %v", err)
		}
		if len(goParams) != len(libParams) {
			t.Fatalf("params length mismatch pkt=%d go=%d lib=%d", pktIdx, len(goParams), len(libParams))
		}

		for f := 0; f < len(goParams); f++ {
			// Compare NLSF indices/interp first
			for i := 0; i < goDec.state[0].lpcOrder+1; i++ {
				if goIndices[f].NLSFIndices[i] != libParams[f].NLSFIndices[i] {
					t.Logf("pkt=%d frame=%d NLSFIndices[%d] go=%d lib=%d", pktIdx, f, i, goIndices[f].NLSFIndices[i], libParams[f].NLSFIndices[i])
					t.Fatalf("indices mismatch at pkt=%d frame=%d", pktIdx, f)
				}
			}
			if goIndices[f].NLSFInterpCoefQ2 != libParams[f].NLSFInterpCoefQ2 {
				t.Logf("pkt=%d frame=%d NLSFInterpCoefQ2 go=%d lib=%d", pktIdx, f, goIndices[f].NLSFInterpCoefQ2, libParams[f].NLSFInterpCoefQ2)
				t.Fatalf("indices mismatch at pkt=%d frame=%d", pktIdx, f)
			}
			// Compare GainsQ16
			for i := 0; i < nbSubfr; i++ {
				if goParams[f].GainsQ16[i] != libParams[f].GainsQ16[i] {
					t.Logf("pkt=%d frame=%d GainsQ16[%d] go=%d lib=%d", pktIdx, f, i, goParams[f].GainsQ16[i], libParams[f].GainsQ16[i])
					t.Fatalf("params mismatch at pkt=%d frame=%d", pktIdx, f)
				}
			}
			// Compare PredCoefQ12
			for k := 0; k < 2; k++ {
				for i := 0; i < goDec.state[0].lpcOrder; i++ {
					if goParams[f].PredCoefQ12[k][i] != libParams[f].PredCoefQ12[k][i] {
						// Debug NLSF decode and NLSF2A outputs.
						lpcOrder := goDec.state[0].lpcOrder
						useWB := lpcOrder == 16
						goNLSF := make([]int16, maxLPCOrder)
						silkNLSFDecode(goNLSF, goIndices[f].NLSFIndices[:], goDec.state[0].nlsfCB)
						libNLSF := cgowrap.SilkNLSFDecode(goIndices[f].NLSFIndices[:], useWB)
						for n := 0; n < lpcOrder; n++ {
							if goNLSF[n] != libNLSF[n] {
								t.Logf("pkt=%d frame=%d NLSF[%d] go=%d lib=%d", pktIdx, f, n, goNLSF[n], libNLSF[n])
								break
							}
						}
						if goIndices[f].NLSFInterpCoefQ2 < 4 {
							nlsf0Go := make([]int16, lpcOrder)
							nlsf0Lib := make([]int16, lpcOrder)
							for n := 0; n < lpcOrder; n++ {
								diff := int32(goNLSF[n]) - int32(goIndices[f].PrevNLSFQ15[n])
								nlsf0Go[n] = int16(int32(goIndices[f].PrevNLSFQ15[n]) + (int32(goIndices[f].NLSFInterpCoefQ2)*diff)>>2)
								diffLib := int32(libNLSF[n]) - int32(goIndices[f].PrevNLSFQ15[n])
								nlsf0Lib[n] = int16(int32(goIndices[f].PrevNLSFQ15[n]) + (int32(goIndices[f].NLSFInterpCoefQ2)*diffLib)>>2)
							}
							goA0 := make([]int16, lpcOrder)
							_ = silkNLSF2A(goA0, nlsf0Go, lpcOrder)
							libA0 := cgowrap.SilkNLSF2A(nlsf0Lib, lpcOrder)
							for n := 0; n < lpcOrder; n++ {
								if goA0[n] != libA0[n] {
									t.Logf("pkt=%d frame=%d A0[%d] go=%d lib=%d", pktIdx, f, n, goA0[n], libA0[n])
									break
								}
							}
						}
						goA1 := make([]int16, lpcOrder)
						_ = silkNLSF2A(goA1, goNLSF[:lpcOrder], lpcOrder)
						libA1 := cgowrap.SilkNLSF2A(libNLSF[:lpcOrder], lpcOrder)
						for n := 0; n < lpcOrder; n++ {
							if goA1[n] != libA1[n] {
								t.Logf("pkt=%d frame=%d A1[%d] go=%d lib=%d", pktIdx, f, n, goA1[n], libA1[n])
								break
							}
						}
						t.Logf("pkt=%d frame=%d PredCoef[%d][%d] go=%d lib=%d", pktIdx, f, k, i, goParams[f].PredCoefQ12[k][i], libParams[f].PredCoefQ12[k][i])
						t.Fatalf("params mismatch at pkt=%d frame=%d", pktIdx, f)
					}
				}
			}
			// Compare LTPCoefQ14 and pitchL
			for i := 0; i < nbSubfr*ltpOrder; i++ {
				if goParams[f].LTPCoefQ14[i] != libParams[f].LTPCoefQ14[i] {
					t.Logf("pkt=%d frame=%d LTPCoefQ14[%d] go=%d lib=%d", pktIdx, f, i, goParams[f].LTPCoefQ14[i], libParams[f].LTPCoefQ14[i])
					t.Fatalf("params mismatch at pkt=%d frame=%d", pktIdx, f)
				}
			}
			for i := 0; i < nbSubfr; i++ {
				if int32(goParams[f].pitchL[i]) != libParams[f].PitchL[i] {
					t.Logf("pkt=%d frame=%d pitchL[%d] go=%d lib=%d", pktIdx, f, i, goParams[f].pitchL[i], libParams[f].PitchL[i])
					t.Fatalf("params mismatch at pkt=%d frame=%d", pktIdx, f)
				}
			}
			if goParams[f].LTPScaleQ14 != int32(libParams[f].LTPScaleQ14) {
				t.Logf("pkt=%d frame=%d LTPScaleQ14 go=%d lib=%d", pktIdx, f, goParams[f].LTPScaleQ14, libParams[f].LTPScaleQ14)
				t.Fatalf("params mismatch at pkt=%d frame=%d", pktIdx, f)
			}
		}
	}
}
