//go:build cgo_libopus

package silk

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/rangecoding"
)

func TestSilkLBRRIndicesPulsesMatchLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)

	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000
	if frameSamples <= 0 {
		t.Fatalf("invalid frameSamples: %d", frameSamples)
	}

	pcm0 := make([]float32, frameSamples)
	pcm1 := make([]float32, frameSamples)
	for i := 0; i < frameSamples; i++ {
		phase := 2 * math.Pi * 440 * float64(i) / float64(config.SampleRate)
		v := float32(0.3 * math.Sin(phase))
		pcm0[i] = v
		pcm1[i] = v
	}

	_ = enc.EncodePacketWithFEC(pcm0, nil, []bool{true})
	if enc.lbrrFlags[0] == 0 {
		t.Skip("LBRR not generated for first frame")
	}

	expectedIndices := enc.lbrrIndices[0]
	expectedFrameLength := enc.lbrrFrameLength[0]
	expectedNbSubfr := enc.lbrrNbSubfr[0]
	if expectedFrameLength <= 0 || expectedNbSubfr <= 0 {
		t.Fatalf("invalid LBRR metadata: frameLength=%d nbSubfr=%d", expectedFrameLength, expectedNbSubfr)
	}

	expectedPulses := make([]int8, expectedFrameLength)
	copy(expectedPulses, enc.lbrrPulses[0][:expectedFrameLength])

	// Sanity check: pulse-only encoding should round-trip via libopus.
	pulseEnc := NewEncoder(BandwidthWideband)
	pulseOut := ensureByteSlice(&pulseEnc.scratchOutput, 2048)
	pulseEnc.scratchRangeEncoder.Init(pulseOut)
	pulseEnc.rangeEncoder = &pulseEnc.scratchRangeEncoder
	pulse32 := make([]int32, expectedFrameLength)
	for i := 0; i < expectedFrameLength; i++ {
		pulse32[i] = int32(expectedPulses[i])
	}
	pulseEnc.encodePulses(pulse32, int(expectedIndices.signalType), int(expectedIndices.quantOffsetType))
	pulseData := pulseEnc.rangeEncoder.Done()
	pulseEnc.rangeEncoder = nil

	decodedPulseOnly, err := cgowrap.SilkDecodePulsesOnly(pulseData, int(expectedIndices.signalType), int(expectedIndices.quantOffsetType), expectedFrameLength)
	if err != nil {
		t.Fatalf("libopus decode pulse-only: %v", err)
	}
	if decodedPulseOnly == nil {
		t.Fatalf("libopus decode pulse-only returned nil")
	}
	for i := 0; i < expectedFrameLength; i++ {
		if decodedPulseOnly[i] != int16(expectedPulses[i]) {
			t.Fatalf("pulse-only[%d] mismatch: go=%d lib=%d", i, expectedPulses[i], decodedPulseOnly[i])
		}
	}

	pkt1 := enc.EncodePacketWithFEC(pcm1, nil, []bool{true})
	if len(pkt1) == 0 {
		t.Fatalf("second packet empty")
	}

	fsKHz := config.SampleRate / 1000
	bitsLib, err := cgowrap.SilkDecodeLBRRIndexBits(pkt1, fsKHz, expectedNbSubfr, 1, 0)
	if err != nil {
		t.Fatalf("libopus decode LBRR index bits: %v", err)
	}
	if bitsLib == 0 {
		t.Fatalf("libopus decode LBRR index bits returned 0")
	}

	var rd rangecoding.Decoder
	rd.Init(pkt1)
	st := &NewDecoder().state[0]
	st.nFramesDecoded = 0
	st.nFramesPerPacket = 1
	st.nbSubfr = expectedNbSubfr
	silkDecoderSetFs(st, fsKHz)
	decodeVADAndLBRRFlags(&rd, st, 1)
	if st.VADFlags[0] == 0 {
		t.Fatalf("expected VAD flag set for bit count check")
	}
	if st.LBRRFlag == 0 || st.LBRRFlags[0] == 0 {
		t.Fatalf("expected LBRR flags set for bit count check")
	}
	silkDecodeIndices(st, &rd, true, codeIndependently)
	bitsGo := rd.Tell()
	if bitsGo != bitsLib {
		t.Fatalf("LBRR index bits mismatch: go=%d lib=%d", bitsGo, bitsLib)
	}
	rngGo, valGo := rd.State()
	rngLib, valLib, err := cgowrap.SilkDecodeLBRRIndexState(pkt1, fsKHz, expectedNbSubfr, 1, 0)
	if err != nil {
		t.Fatalf("libopus decode LBRR index state: %v", err)
	}
	if rngLib == 0 && valLib == 0 {
		t.Fatalf("libopus decode LBRR index state returned zero")
	}
	if rngGo != rngLib || valGo != valLib {
		t.Fatalf("LBRR index state mismatch: go(rng=%d,val=%d) lib(rng=%d,val=%d)", rngGo, valGo, rngLib, valLib)
	}

	pulsesGo := make([]int16, roundUpShellFrame(st.frameLength))
	silkDecodePulses(&rd, pulsesGo, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
	goMismatchIdx := -1
	goMismatchVal := int16(0)
	for i := 0; i < expectedFrameLength; i++ {
		if pulsesGo[i] != int16(expectedPulses[i]) {
			goMismatchIdx = i
			goMismatchVal = pulsesGo[i]
			break
		}
	}

	decoded, err := cgowrap.SilkDecodeLBRRIndicesPulses(pkt1, fsKHz, expectedNbSubfr, 1, 0, expectedFrameLength)
	if err != nil {
		t.Fatalf("libopus decode LBRR: %v", err)
	}
	if decoded == nil {
		t.Fatalf("libopus decode returned nil")
	}

	if decoded.SignalType != expectedIndices.signalType {
		t.Fatalf("signalType mismatch: go=%d lib=%d", expectedIndices.signalType, decoded.SignalType)
	}
	if decoded.QuantOffsetType != expectedIndices.quantOffsetType {
		t.Fatalf("quantOffset mismatch: go=%d lib=%d", expectedIndices.quantOffsetType, decoded.QuantOffsetType)
	}
	if decoded.NLSFInterpCoef != expectedIndices.NLSFInterpCoefQ2 {
		t.Fatalf("NLSF interp mismatch: go=%d lib=%d", expectedIndices.NLSFInterpCoefQ2, decoded.NLSFInterpCoef)
	}
	if decoded.Seed != expectedIndices.Seed {
		t.Fatalf("seed mismatch: go=%d lib=%d", expectedIndices.Seed, decoded.Seed)
	}

	for i := 0; i < expectedNbSubfr; i++ {
		if decoded.GainsIndices[i] != expectedIndices.GainsIndices[i] {
			t.Fatalf("gain index[%d] mismatch: go=%d lib=%d", i, expectedIndices.GainsIndices[i], decoded.GainsIndices[i])
		}
	}

	order := enc.lpcOrder + 1
	if order > len(decoded.NLSFIndices) {
		order = len(decoded.NLSFIndices)
	}
	for i := 0; i < order; i++ {
		if decoded.NLSFIndices[i] != expectedIndices.NLSFIndices[i] {
			t.Fatalf("NLSF index[%d] mismatch: go=%d lib=%d", i, expectedIndices.NLSFIndices[i], decoded.NLSFIndices[i])
		}
	}

	if decoded.SignalType == typeVoiced {
		if decoded.LagIndex != expectedIndices.lagIndex {
			t.Fatalf("lag index mismatch: go=%d lib=%d", expectedIndices.lagIndex, decoded.LagIndex)
		}
		if decoded.ContourIndex != expectedIndices.contourIndex {
			t.Fatalf("contour index mismatch: go=%d lib=%d", expectedIndices.contourIndex, decoded.ContourIndex)
		}
		if decoded.PERIndex != expectedIndices.PERIndex {
			t.Fatalf("PER index mismatch: go=%d lib=%d", expectedIndices.PERIndex, decoded.PERIndex)
		}
		if decoded.LTPScaleIndex != expectedIndices.LTPScaleIndex {
			t.Fatalf("LTP scale mismatch: go=%d lib=%d", expectedIndices.LTPScaleIndex, decoded.LTPScaleIndex)
		}
		for i := 0; i < expectedNbSubfr; i++ {
			if decoded.LTPIndex[i] != expectedIndices.LTPIndex[i] {
				t.Fatalf("LTP index[%d] mismatch: go=%d lib=%d", i, expectedIndices.LTPIndex[i], decoded.LTPIndex[i])
			}
		}
	}

	if len(decoded.Pulses) < expectedFrameLength {
		t.Fatalf("decoded pulses too short: %d", len(decoded.Pulses))
	}
	for i := 0; i < expectedFrameLength; i++ {
		if decoded.Pulses[i] != int16(expectedPulses[i]) {
			t.Fatalf("pulse[%d] mismatch: go=%d lib=%d", i, expectedPulses[i], decoded.Pulses[i])
		}
	}
	if goMismatchIdx >= 0 {
		t.Fatalf("go decode pulse[%d] mismatch: go=%d exp=%d", goMismatchIdx, goMismatchVal, expectedPulses[goMismatchIdx])
	}
}
