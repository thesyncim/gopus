package encoder

import "testing"

func legacyUpdateDelayBufferInternal(delayBuffer, pcm, tail []float64, frameSamples, delaySamples, encoderBufferSamples int) {
	if delaySamples <= 0 || frameSamples <= 0 {
		return
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}

	if encoderBufferSamples > frameSamples+delaySamples {
		keep := encoderBufferSamples - (frameSamples + delaySamples)
		if keep > 0 {
			copy(delayBuffer[:keep], delayBuffer[frameSamples:frameSamples+keep])
		}
		copy(delayBuffer[keep:keep+delaySamples], tail)
		copy(delayBuffer[keep+delaySamples:], pcm[:frameSamples])
		return
	}

	start := delaySamples + frameSamples - encoderBufferSamples
	if start < delaySamples {
		nTail := delaySamples - start
		if nTail > encoderBufferSamples {
			nTail = encoderBufferSamples
		}
		copy(delayBuffer[:nTail], tail[start:start+nTail])
		remaining := encoderBufferSamples - nTail
		if remaining > 0 {
			copy(delayBuffer[nTail:], pcm[:remaining])
		}
		return
	}

	pcmStart := start - delaySamples
	if pcmStart < 0 {
		pcmStart = 0
	}
	if pcmStart+encoderBufferSamples > len(pcm) {
		pcmStart = len(pcm) - encoderBufferSamples
		if pcmStart < 0 {
			pcmStart = 0
		}
	}
	copy(delayBuffer, pcm[pcmStart:pcmStart+encoderBufferSamples])
}

func legacyApplyDelayCompensationState(delayBuffer, pcm, out, prefill, tail []float64, sampleRate, channels, frameSize int) int {
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		copy(out, pcm)
		return 0
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	frameSamples := frameSize * channels
	if len(pcm) < frameSamples {
		frameSamples = len(pcm)
	}
	if delaySamples <= 0 || frameSamples <= 0 {
		return 0
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}

	tailStart := encoderBufferSamples - delaySamples
	tail = tail[:delaySamples]
	copy(tail, delayBuffer[tailStart:])

	prefillFrameSize := sampleRate / 400
	prefillSamples := prefillFrameSize * channels
	prefillStart := encoderBufferSamples - delaySamples - prefillSamples
	if prefillSamples > 0 && prefillStart >= 0 && prefillStart+prefillSamples <= len(delayBuffer) {
		copy(prefill[:prefillSamples], delayBuffer[prefillStart:prefillStart+prefillSamples])
	} else {
		prefillSamples = 0
	}

	clear(out)
	if frameSamples <= delaySamples {
		copy(out, tail[:frameSamples])
	} else {
		copy(out, tail)
		copy(out[delaySamples:], pcm[:frameSamples-delaySamples])
	}

	legacyUpdateDelayBufferInternal(delayBuffer, pcm, tail, frameSamples, delaySamples, encoderBufferSamples)
	return prefillSamples
}

func requireEqualFloat64Slices(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want=%d", name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s[%d]=%.9f want %.9f", name, i, got[i], want[i])
		}
	}
}

func runDelayCompStream(frameSize, channels, totalFrames int) ([]float64, []float64) {
	e := NewEncoder(48000, channels)
	// Disable dcReject so test exercises only delay compensation behavior.
	e.sampleRate = 48000
	delaySamples := (e.sampleRate / 250) * channels
	frameSamples := frameSize * channels
	totalSamples := totalFrames * frameSamples

	in := make([]float64, totalSamples)
	for i := range in {
		in[i] = float64(i + 1)
	}

	out := make([]float64, 0, totalSamples)
	for f := 0; f < totalFrames; f++ {
		start := f * frameSamples
		end := start + frameSamples
		block := e.applyDelayCompensation(in[start:end], frameSize)
		out = append(out, block...)
	}

	want := make([]float64, totalSamples)
	for i := range want {
		src := i - delaySamples
		if src >= 0 {
			want[i] = in[src]
		}
	}
	return out, want
}

func TestDelayCompensation_StreamDelayMono(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		out, want := runDelayCompStream(frameSize, 1, 24)
		if len(out) != len(want) {
			t.Fatalf("frame=%d: output len=%d want=%d", frameSize, len(out), len(want))
		}
		for i := range out {
			if out[i] != want[i] {
				t.Fatalf("frame=%d sample=%d: got=%.0f want=%.0f", frameSize, i, out[i], want[i])
			}
		}
	}
}

func TestDelayCompensation_StreamDelayStereo(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		out, want := runDelayCompStream(frameSize, 2, 24)
		if len(out) != len(want) {
			t.Fatalf("frame=%d: output len=%d want=%d", frameSize, len(out), len(want))
		}
		for i := range out {
			if out[i] != want[i] {
				t.Fatalf("frame=%d sample=%d: got=%.0f want=%.0f", frameSize, i, out[i], want[i])
			}
		}
	}
}

func TestPrepareCELTPCM_DelayCompensationGatedByLowDelay(t *testing.T) {
	const frameSize = 960
	in := make([]float64, frameSize)
	for i := range in {
		in[i] = float64(i + 1)
	}

	normal := NewEncoder(48000, 1)
	normal.SetMode(ModeCELT)
	normal.SetLowDelay(false)
	outNormal := normal.prepareCELTPCM(in, frameSize)
	delaySamples := 48000 / 250
	for i := range outNormal {
		var want float64
		if i >= delaySamples {
			want = in[i-delaySamples]
		}
		if outNormal[i] != want {
			t.Fatalf("normal sample %d: got=%.0f want=%.0f", i, outNormal[i], want)
		}
	}

	lowDelay := NewEncoder(48000, 1)
	lowDelay.SetMode(ModeCELT)
	lowDelay.SetLowDelay(true)
	outLowDelay := lowDelay.prepareCELTPCM(in, frameSize)
	for i := range outLowDelay {
		if outLowDelay[i] != in[i] {
			t.Fatalf("lowdelay sample %d: got=%.0f want=%.0f", i, outLowDelay[i], in[i])
		}
	}
}

func TestApplyDelayCompensationMatchesLegacyState(t *testing.T) {
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{120, 240, 480, 960} {
			t.Run(testName(channels, frameSize), func(t *testing.T) {
				enc := NewEncoder(48000, channels)
				encoderBufferSamples := (enc.sampleRate / 100) * channels
				delaySamples := (enc.sampleRate / 250) * channels
				prefillSamples := (enc.sampleRate / 400) * channels
				frameSamples := frameSize * channels

				enc.delayBuffer = make([]float64, encoderBufferSamples)
				legacyDelay := make([]float64, encoderBufferSamples)
				for i := range legacyDelay {
					v := float64((i%29)-14) / 8.0
					enc.delayBuffer[i] = v
					legacyDelay[i] = v
				}

				legacyOut := make([]float64, frameSamples)
				legacyPrefill := make([]float64, prefillSamples)
				legacyTail := make([]float64, delaySamples)

				for iter := 0; iter < 12; iter++ {
					pcm := make([]float64, frameSamples)
					for i := range pcm {
						pcm[i] = float64((iter+1)*1000 + i*channels + 1)
					}

					wantPrefillLen := legacyApplyDelayCompensationState(
						legacyDelay,
						pcm,
						legacyOut,
						legacyPrefill,
						legacyTail,
						enc.sampleRate,
						channels,
						frameSize,
					)
					got := enc.applyDelayCompensation(pcm, frameSize)

					requireEqualFloat64Slices(t, "out", got[:frameSamples], legacyOut)
					requireEqualFloat64Slices(t, "delayBuffer", enc.delayBuffer, legacyDelay)
					requireEqualFloat64Slices(t, "prefill", enc.scratchTransitionPrefill[:wantPrefillLen], legacyPrefill[:wantPrefillLen])
				}
			})
		}
	}
}

func BenchmarkApplyDelayCompensation(b *testing.B) {
	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "Mono480", frameSize: 480, channels: 1},
		{name: "Mono960", frameSize: 960, channels: 1},
		{name: "Stereo480", frameSize: 480, channels: 2},
		{name: "Stereo960", frameSize: 960, channels: 2},
	} {
		b.Run(tc.name+"/current", func(b *testing.B) {
			enc := NewEncoder(48000, tc.channels)
			encoderBufferSamples := (enc.sampleRate / 100) * tc.channels
			seed := make([]float64, encoderBufferSamples)
			for i := range seed {
				seed[i] = float64((i%31)-15) / 16.0
			}
			enc.delayBuffer = make([]float64, encoderBufferSamples)
			pcm := make([]float64, tc.frameSize*tc.channels)
			for i := range pcm {
				pcm[i] = float64((i%37)-18) / 16.0
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(enc.delayBuffer, seed)
				_ = enc.applyDelayCompensation(pcm, tc.frameSize)
			}
		})

		b.Run(tc.name+"/legacy", func(b *testing.B) {
			const sampleRate = 48000
			encoderBufferSamples := (sampleRate / 100) * tc.channels
			delaySamples := (sampleRate / 250) * tc.channels
			prefillSamples := (sampleRate / 400) * tc.channels
			seed := make([]float64, encoderBufferSamples)
			for i := range seed {
				seed[i] = float64((i%31)-15) / 16.0
			}
			delayBuffer := make([]float64, encoderBufferSamples)
			pcm := make([]float64, tc.frameSize*tc.channels)
			for i := range pcm {
				pcm[i] = float64((i%37)-18) / 16.0
			}
			out := make([]float64, tc.frameSize*tc.channels)
			prefill := make([]float64, prefillSamples)
			tail := make([]float64, delaySamples)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(delayBuffer, seed)
				legacyApplyDelayCompensationState(delayBuffer, pcm, out, prefill, tail, sampleRate, tc.channels, tc.frameSize)
			}
		})
	}
}

func testName(channels, frameSize int) string {
	if channels == 1 {
		return "mono_" + frameSizeString(frameSize)
	}
	return "stereo_" + frameSizeString(frameSize)
}

func frameSizeString(v int) string {
	switch v {
	case 120:
		return "120"
	case 240:
		return "240"
	case 480:
		return "480"
	case 960:
		return "960"
	default:
		return "x"
	}
}
