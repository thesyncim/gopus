//go:build cgo_libopus
// +build cgo_libopus

package encoder

import (
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/silk"
)

func TestVADAgainstLibopus(t *testing.T) {
	const (
		fs        = 16000
		frames    = 50
		frameMs   = 20
		amplitude = 0.6
		noiseAmp  = 0.05
		freq1     = 220.0
		freq2     = 440.0
		freq3     = 880.0
	)
	frameSamples := fs * frameMs / 1000
	fsKHz := fs / 1000

	signal := make([]float32, frames*frameSamples)
	var seed uint32 = 1
	for i := 0; i < len(signal); i++ {
		tm := float64(i) / float64(fs)
		base := amplitude*math.Sin(2*math.Pi*freq1*tm) + 0.3*math.Sin(2*math.Pi*freq2*tm) + 0.1*math.Sin(2*math.Pi*freq3*tm)
		seed = seed*1664525 + 1013904223
		noise := noiseAmp * (float64((seed>>9)&0x3FF)/512.0 - 1.0)
		signal[i] = float32(base + noise)
	}

	goVAD := NewVADState()
	libVAD := silk.NewLibopusVADState()

	pcm16 := make([]int16, frameSamples)
	var mismatches int
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		goAct, _ := goVAD.GetSpeechActivity(pcm, frameSamples, fsKHz)
		for i, v := range pcm {
			sample := float64(v) * 32768.0
			pcm16[i] = float64ToInt16Round(sample)
		}
		libRes := libVAD.GetSpeechActivity(pcm16, frameSamples, fsKHz)

		if goAct != libRes.SpeechActivityQ8 || goVAD.InputTiltQ15 != libRes.InputTiltQ15 {
			mismatches++
			if mismatches <= 5 {
				t.Logf("frame %d: activity go=%d lib=%d tilt go=%d lib=%d", frame, goAct, libRes.SpeechActivityQ8, goVAD.InputTiltQ15, libRes.InputTiltQ15)
			}
			continue
		}
		for b := 0; b < 4; b++ {
			if goVAD.InputQualityBandsQ15[b] != libRes.InputQualityBandsQ15[b] {
				mismatches++
				if mismatches <= 5 {
					t.Logf("frame %d band %d: quality go=%d lib=%d nrgRatio go=%d lib=%d nl go=%d lib=%d", frame, b, goVAD.InputQualityBandsQ15[b], libRes.InputQualityBandsQ15[b], goVAD.NrgRatioSmthQ8[b], libRes.NrgRatioSmthQ8[b], goVAD.NL[b], libRes.NL[b])
				}
				break
			}
		}
	}

	if mismatches > 0 {
		t.Fatalf("VAD mismatches: %d/%d frames", mismatches, frames)
	}
}

func TestVADTraceAgainstLibopus(t *testing.T) {
	if os.Getenv("VAD_TRACE") == "" {
		t.Skip("set VAD_TRACE=1 to run trace comparison")
	}
	const (
		fs        = 16000
		frames    = 50
		frameMs   = 20
		amplitude = 0.6
		noiseAmp  = 0.05
		freq1     = 220.0
		freq2     = 440.0
		freq3     = 880.0
	)
	frameSamples := fs * frameMs / 1000
	fsKHz := fs / 1000

	signal := make([]float32, frames*frameSamples)
	var seed uint32 = 1
	for i := 0; i < len(signal); i++ {
		tm := float64(i) / float64(fs)
		base := amplitude*math.Sin(2*math.Pi*freq1*tm) + 0.3*math.Sin(2*math.Pi*freq2*tm) + 0.1*math.Sin(2*math.Pi*freq3*tm)
		seed = seed*1664525 + 1013904223
		noise := noiseAmp * (float64((seed>>9)&0x3FF)/512.0 - 1.0)
		signal[i] = float32(base + noise)
	}

	goVAD := NewVADState()
	libVAD := silk.NewLibopusVADState()

	pcm16 := make([]int16, frameSamples)
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		goAct, _, goTrace := goVAD.GetSpeechActivityTrace(pcm, frameSamples, fsKHz)
		for i, v := range pcm {
			sample := float64(v) * 32768.0
			pcm16[i] = float64ToInt16Round(sample)
		}
		libTrace := libVAD.GetSpeechActivityTrace(pcm16, frameSamples, fsKHz)

		if goTrace.HPState != int16(libTrace.HPState) {
			t.Fatalf("frame %d: HPState go=%d lib=%d", frame, goTrace.HPState, libTrace.HPState)
		}

		for b := 0; b < 4; b++ {
			if goTrace.Xnrg[b] != int32(libTrace.Xnrg[b]) {
				t.Fatalf("frame %d band %d: Xnrg go=%d lib=%d", frame, b, goTrace.Xnrg[b], libTrace.Xnrg[b])
			}
			if goTrace.XnrgSubfr[b] != int32(libTrace.XnrgSubfr[b]) {
				t.Fatalf("frame %d band %d: XnrgSubfr go=%d lib=%d", frame, b, goTrace.XnrgSubfr[b], libTrace.XnrgSubfr[b])
			}
			for s := 0; s < 4; s++ {
				if goTrace.SubfrEnergy[b][s] != int32(libTrace.SubfrEnergy[b][s]) {
					t.Fatalf("frame %d band %d sub %d: subfrEnergy go=%d lib=%d", frame, b, s, goTrace.SubfrEnergy[b][s], libTrace.SubfrEnergy[b][s])
				}
			}
			if goTrace.NL[b] != int32(libTrace.NL[b]) {
				t.Fatalf("frame %d band %d: NL go=%d lib=%d", frame, b, goTrace.NL[b], libTrace.NL[b])
			}
			if goTrace.InvNL[b] != int32(libTrace.InvNL[b]) {
				t.Fatalf("frame %d band %d: InvNL go=%d lib=%d", frame, b, goTrace.InvNL[b], libTrace.InvNL[b])
			}
			if goTrace.NrgToNoiseRatioQ8[b] != int32(libTrace.NrgToNoiseRatioQ8[b]) {
				t.Fatalf("frame %d band %d: NrgToNoiseRatioQ8 go=%d lib=%d", frame, b, goTrace.NrgToNoiseRatioQ8[b], libTrace.NrgToNoiseRatioQ8[b])
			}
			if goTrace.SNRQ7[b] != int32(libTrace.SNRQ7[b]) {
				t.Fatalf("frame %d band %d: SNRQ7 go=%d lib=%d", frame, b, goTrace.SNRQ7[b], libTrace.SNRQ7[b])
			}
			if goTrace.SNRQ7Tilt[b] != int32(libTrace.SNRQ7Tilt[b]) {
				t.Fatalf("frame %d band %d: SNRQ7Tilt go=%d lib=%d", frame, b, goTrace.SNRQ7Tilt[b], libTrace.SNRQ7Tilt[b])
			}
		}

		if goTrace.SumSquared != int32(libTrace.SumSquared) {
			t.Fatalf("frame %d: sumSquared go=%d lib=%d", frame, goTrace.SumSquared, libTrace.SumSquared)
		}
		if goTrace.PSNRdBQ7 != int32(libTrace.PSNRdBQ7) {
			t.Fatalf("frame %d: pSNRdBQ7 go=%d lib=%d", frame, goTrace.PSNRdBQ7, libTrace.PSNRdBQ7)
		}
		if goTrace.SAQ15 != int32(libTrace.SAQ15) {
			t.Fatalf("frame %d: SAQ15 go=%d lib=%d", frame, goTrace.SAQ15, libTrace.SAQ15)
		}
		if goTrace.InputTilt != int32(libTrace.InputTilt) {
			t.Fatalf("frame %d: inputTilt go=%d lib=%d", frame, goTrace.InputTilt, libTrace.InputTilt)
		}
		if goTrace.SpeechNrgPre != int32(libTrace.SpeechNrgPre) {
			t.Fatalf("frame %d: speechNrgPre go=%d lib=%d", frame, goTrace.SpeechNrgPre, libTrace.SpeechNrgPre)
		}
		if goTrace.SpeechNrgPost != int32(libTrace.SpeechNrgPost) {
			t.Fatalf("frame %d: speechNrgPost go=%d lib=%d", frame, goTrace.SpeechNrgPost, libTrace.SpeechNrgPost)
		}
		if goTrace.SmoothCoefQ16 != int32(libTrace.SmoothCoefQ16) {
			t.Fatalf("frame %d: smoothCoefQ16 go=%d lib=%d", frame, goTrace.SmoothCoefQ16, libTrace.SmoothCoefQ16)
		}

		for b := 0; b < 4; b++ {
			if goTrace.NrgRatioSmthQ8[b] != int32(libTrace.NrgRatioSmthQ8[b]) {
				t.Fatalf("frame %d band %d: NrgRatioSmthQ8 go=%d lib=%d", frame, b, goTrace.NrgRatioSmthQ8[b], libTrace.NrgRatioSmthQ8[b])
			}
			if goTrace.SNRQ7Smth[b] != int32(libTrace.SNRQ7Smth[b]) {
				t.Fatalf("frame %d band %d: SNRQ7Smth go=%d lib=%d", frame, b, goTrace.SNRQ7Smth[b], libTrace.SNRQ7Smth[b])
			}
			if goTrace.InputQualityQ15[b] != int32(libTrace.InputQualityBandsQ15[b]) {
				t.Fatalf("frame %d band %d: InputQualityQ15 go=%d lib=%d", frame, b, goTrace.InputQualityQ15[b], libTrace.InputQualityBandsQ15[b])
			}
		}

		if goAct != libTrace.SpeechActivityQ8 {
			t.Fatalf("frame %d: speech_activity_Q8 go=%d lib=%d", frame, goAct, libTrace.SpeechActivityQ8)
		}
		if goVAD.InputTiltQ15 != libTrace.InputTiltQ15 {
			t.Fatalf("frame %d: input_tilt_Q15 go=%d lib=%d", frame, goVAD.InputTiltQ15, libTrace.InputTiltQ15)
		}
	}
}

func TestLin2LogAgainstLibopus(t *testing.T) {
	var mismatches int
	for v := int32(1); v < 1<<24; v += 12345 {
		goVal := lin2log(v)
		libVal := silk.LibopusLin2Log(v)
		if goVal != libVal {
			mismatches++
			if mismatches <= 5 {
				t.Logf("lin2log(%d): go=%d lib=%d", v, goVal, libVal)
			}
		}
	}
	if mismatches > 0 {
		t.Fatalf("lin2log mismatches: %d", mismatches)
	}
}
