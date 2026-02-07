//go:build cgo_libopus

package silk

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func genTmpModulatedSignal(samples int) []float32 {
	out := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	mod := []float64{1.3, 2.7, 0.9}
	amp := 0.3
	for i := 0; i < samples; i++ {
		tm := float64(i) / 48000.0
		var v float64
		for fi, f := range freqs {
			md := 0.5 + 0.5*math.Sin(2*math.Pi*mod[fi]*tm)
			v += amp * md * math.Sin(2*math.Pi*f*tm)
		}
		if i < 480 {
			x := float64(i) / 480.0
			v *= x * x * x
		}
		out[i] = float32(v)
	}
	return out
}

func TestTmpNSQFirstFailureModulatedSignal(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		bitrate    = 32000
		frameSize  = 960
		frames     = 50
	)

	signal := genTmpModulatedSignal(frames * frameSize)
	for frame := 0; frame < frames; frame++ {
		snapIn, ok := cgowrap.CaptureOpusNSQInputsAtFrame(signal, sampleRate, channels, bitrate, frameSize, frame)
		if !ok || snapIn.FrameLength == 0 {
			t.Fatalf("frame %d: failed to capture opus NSQ inputs", frame)
		}
		snapState, ok := cgowrap.CaptureOpusSilkNSQStateBeforeFrame(signal, sampleRate, channels, bitrate, frameSize, frame)
		if !ok {
			t.Fatalf("frame %d: failed to capture opus NSQ state", frame)
		}

		nsq := NewNSQState()
		copy(nsq.xq[:], snapState.XQ)
		copy(nsq.sLTPShpQ14[:], snapState.SLTPShpQ14)
		copy(nsq.sLPCQ14[:], snapState.SLPCQ14)
		copy(nsq.sAR2Q14[:], snapState.SAR2Q14)
		nsq.sLFARShpQ14 = snapState.LFARQ14
		nsq.sDiffShpQ14 = snapState.DiffQ14
		nsq.lagPrev = snapState.LagPrev
		nsq.sLTPBufIdx = snapState.SLTPBufIdx
		nsq.sLTPShpBufIdx = snapState.SLTPShpBufIdx
		nsq.randSeed = snapState.RandSeed
		nsq.prevGainQ16 = snapState.PrevGainQ16
		nsq.rewhiteFlag = snapState.RewhiteFlag

		params := &NSQParams{
			SignalType:             snapIn.SignalType,
			QuantOffsetType:        snapIn.QuantOffsetType,
			PredCoefQ12:            snapIn.PredCoefQ12,
			NLSFInterpCoefQ2:       snapIn.NLSFInterpCoefQ2,
			LTPCoefQ14:             snapIn.LTPCoefQ14,
			ARShpQ13:               snapIn.ARQ13,
			HarmShapeGainQ14:       snapIn.HarmShapeGainQ14,
			TiltQ14:                snapIn.TiltQ14,
			LFShpQ14:               snapIn.LFShpQ14,
			GainsQ16:               snapIn.GainsQ16,
			PitchL:                 snapIn.PitchL,
			LambdaQ10:              snapIn.LambdaQ10,
			LTPScaleQ14:            snapIn.LTPScaleQ14,
			FrameLength:            snapIn.FrameLength,
			SubfrLength:            snapIn.SubfrLength,
			NbSubfr:                snapIn.NumSubframes,
			LTPMemLength:           snapIn.LTPMemLength,
			PredLPCOrder:           snapIn.PredLPCOrder,
			ShapeLPCOrder:          snapIn.ShapeLPCOrder,
			WarpingQ16:             snapIn.WarpingQ16,
			NStatesDelayedDecision: snapIn.NStatesDelayedDecision,
			Seed:                   snapIn.SeedIn,
		}

		goPulses, goXQ, goSeed := NoiseShapeQuantizeDelDec(nsq, snapIn.X16, params)
		libPulses, libXQ, libSeed, _, _, _, _ := cgowrap.SilkNSQDelDecCaptureWithStateFinal(
			snapIn.FrameLength, snapIn.SubfrLength, snapIn.NumSubframes, snapIn.LTPMemLength,
			snapIn.PredLPCOrder, snapIn.ShapeLPCOrder, snapIn.WarpingQ16, snapIn.NStatesDelayedDecision,
			snapIn.SignalType, snapIn.QuantOffsetType, snapIn.NLSFInterpCoefQ2, snapIn.SeedIn,
			snapIn.X16,
			snapIn.PredCoefQ12,
			snapIn.LTPCoefQ14,
			snapIn.ARQ13,
			snapIn.HarmShapeGainQ14,
			snapIn.TiltQ14,
			snapIn.LFShpQ14,
			snapIn.GainsQ16,
			snapIn.PitchL,
			snapIn.LambdaQ10, snapIn.LTPScaleQ14,
			snapState.XQ,
			snapState.SLTPShpQ14,
			snapState.SLPCQ14,
			snapState.SAR2Q14,
			snapState.LFARQ14,
			snapState.DiffQ14,
			snapState.LagPrev,
			snapState.SLTPBufIdx,
			snapState.SLTPShpBufIdx,
			snapState.RandSeed,
			snapState.PrevGainQ16,
			snapState.RewhiteFlag,
		)

		if idx := firstInt8Mismatch(goPulses, libPulses); idx >= 0 {
			t.Fatalf("first pulse mismatch frame=%d idx=%d go=%d lib=%d", frame, idx, goPulses[idx], libPulses[idx])
		}
		if idx := firstInt16Mismatch(goXQ, libXQ); idx >= 0 {
			t.Fatalf("first xq mismatch frame=%d idx=%d go=%d lib=%d", frame, idx, goXQ[idx], libXQ[idx])
		}
		if goSeed != libSeed {
			t.Fatalf("first seed mismatch frame=%d go=%d lib=%d", frame, goSeed, libSeed)
		}
	}
}

