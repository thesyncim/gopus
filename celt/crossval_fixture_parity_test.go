package celt

import (
	"bytes"
	"math"
	"testing"
)

type crossvalFixtureScenario struct {
	name              string
	sampleRate        int
	channels          int
	ogg               []byte
	input             []float64
	expectNearSilence bool
	minEnergyRatio    float64
}

func clonePacket(packet []byte) []byte {
	out := make([]byte, len(packet))
	copy(out, packet)
	return out
}

func flattenFrames(frames [][]float64) []float64 {
	total := 0
	for _, f := range frames {
		total += len(f)
	}
	out := make([]float64, 0, total)
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

func buildMonoFrameChirp(samples int, startHz, endHz float64) []float64 {
	out := make([]float64, samples)
	for i := 0; i < samples; i++ {
		t := float64(i) / 48000.0
		progress := float64(i) / float64(samples-1)
		freq := startHz + (endHz-startHz)*progress
		amp := 0.48 * (0.7 + 0.3*math.Sin(2*math.Pi*2.0*t))
		out[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	return out
}

func buildMonoFrameImpulse(samples int) []float64 {
	out := make([]float64, samples)
	for i := 0; i < samples; i++ {
		if i%120 == 0 {
			out[i] = 0.9
			continue
		}
		decay := math.Exp(-float64(i%120) / 22.0)
		out[i] = 0.25 * decay * math.Sin(2*math.Pi*2200.0*float64(i)/48000.0)
	}
	return out
}

func buildMonoFramePseudoNoise(samples int) []float64 {
	out := make([]float64, samples)
	var x uint32 = 0x1badf00d
	for i := 0; i < samples; i++ {
		x = 1664525*x + 1013904223
		v := float64((x>>9)&0x7fffff) / float64(0x7fffff)
		out[i] = 0.42 * (2.0*v - 1.0)
	}
	return out
}

func buildStereoFrameChirp(samples int) []float64 {
	out := make([]float64, samples*2)
	left := buildMonoFrameChirp(samples, 220.0, 4200.0)
	right := buildMonoFrameChirp(samples, 4200.0, 220.0)
	for i := 0; i < samples; i++ {
		out[i*2] = left[i]
		out[i*2+1] = right[i] * 0.95
	}
	return out
}

func buildStereoFrameDualTone(samples int, fL, fR float64) []float64 {
	out := make([]float64, samples*2)
	for i := 0; i < samples; i++ {
		t := float64(i) / 48000.0
		out[i*2] = 0.5*math.Sin(2*math.Pi*fL*t) + 0.18*math.Sin(2*math.Pi*3.0*fL*t)
		out[i*2+1] = 0.5*math.Sin(2*math.Pi*fR*t) + 0.15*math.Sin(2*math.Pi*2.0*fR*t)
	}
	return out
}

func buildCrossvalFixtureScenarios(t *testing.T) []crossvalFixtureScenario {
	t.Helper()

	makeScenario := func(name string, channels int, packets [][]byte, input []float64, expectNearSilence bool, minEnergyRatio float64) crossvalFixtureScenario {
		var ogg bytes.Buffer
		if err := writeOggOpus(&ogg, packets, 48000, channels); err != nil {
			t.Fatalf("%s: writeOggOpus failed: %v", name, err)
		}
		return crossvalFixtureScenario{
			name:              name,
			sampleRate:        48000,
			channels:          channels,
			ogg:               ogg.Bytes(),
			input:             input,
			expectNearSilence: expectNearSilence,
			minEnergyRatio:    minEnergyRatio,
		}
	}

	encodeMonoFrame := func(name string, bitrate int, input []float64, expectNearSilence bool, minEnergyRatio float64) crossvalFixtureScenario {
		enc := NewEncoder(1)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(input, 960)
		if err != nil {
			t.Fatalf("%s: encode failed: %v", name, err)
		}
		return makeScenario(name, 1, [][]byte{clonePacket(packet)}, input, expectNearSilence, minEnergyRatio)
	}

	encodeStereoFrame := func(name string, bitrate int, input []float64, expectNearSilence bool, minEnergyRatio float64) crossvalFixtureScenario {
		enc := NewEncoder(2)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(input, 960)
		if err != nil {
			t.Fatalf("%s: encode failed: %v", name, err)
		}
		return makeScenario(name, 2, [][]byte{clonePacket(packet)}, input, expectNearSilence, minEnergyRatio)
	}

	encodeMonoFrames := func(name string, bitrate int, frames [][]float64, minEnergyRatio float64) crossvalFixtureScenario {
		enc := NewEncoder(1)
		enc.SetBitrate(bitrate)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(frame, 960)
			if err != nil {
				t.Fatalf("%s frame %d: encode failed: %v", name, i, err)
			}
			packets[i] = clonePacket(packet)
		}
		return makeScenario(name, 1, packets, flattenFrames(frames), false, minEnergyRatio)
	}

	encodeStereoFrames := func(name string, bitrate int, frames [][]float64, minEnergyRatio float64) crossvalFixtureScenario {
		enc := NewEncoder(2)
		enc.SetBitrate(bitrate)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(frame, 960)
			if err != nil {
				t.Fatalf("%s frame %d: encode failed: %v", name, i, err)
			}
			packets[i] = clonePacket(packet)
		}
		return makeScenario(name, 2, packets, flattenFrames(frames), false, minEnergyRatio)
	}

	return []crossvalFixtureScenario{
		encodeMonoFrame("mono_20ms_single", 64000, generateSineWave(440.0, 960), false, 0.20),
		encodeStereoFrame("stereo_20ms_single", 128000, generateStereoSineWave(440.0, 880.0, 960), false, 0.20),
		encodeMonoFrame("mono_20ms_silence", 64000, make([]float64, 960), true, 0),
		encodeMonoFrames("mono_20ms_multiframe", 64000, [][]float64{
			generateSineWave(440.0, 960),
			generateSineWave(540.0, 960),
			generateSineWave(640.0, 960),
			generateSineWave(740.0, 960),
			generateSineWave(840.0, 960),
		}, 0.20),
		encodeMonoFrame("mono_20ms_chirp", 64000, buildMonoFrameChirp(960, 180.0, 5200.0), false, 0.20),
		encodeMonoFrame("mono_20ms_impulse", 48000, buildMonoFrameImpulse(960), false, 0.10),
		encodeMonoFrame("mono_20ms_noise", 32000, buildMonoFramePseudoNoise(960), false, 0.08),
		encodeMonoFrame("mono_20ms_lowamp", 24000, scaleSignal(generateSineWave(880.0, 960), 0.12), false, 0.05),
		encodeStereoFrame("stereo_20ms_chirp", 96000, buildStereoFrameChirp(960), false, 0.15),
		encodeStereoFrame("stereo_20ms_silence", 96000, make([]float64, 960*2), true, 0),
		encodeStereoFrames("stereo_20ms_multiframe", 96000, [][]float64{
			buildStereoFrameDualTone(960, 300.0, 500.0),
			buildStereoFrameDualTone(960, 520.0, 920.0),
			buildStereoFrameDualTone(960, 760.0, 1240.0),
			buildStereoFrameDualTone(960, 990.0, 1670.0),
		}, 0.15),
	}
}

func scaleSignal(in []float64, gain float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * gain
	}
	return out
}

func TestOpusdecCrossvalFixtureCoverage(t *testing.T) {
	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		t.Fatalf("load opusdec crossval fixture: %v", err)
	}

	scenarios := buildCrossvalFixtureScenarios(t)
	for _, sc := range scenarios {
		hash := oggSHA256Hex(sc.ogg)
		entry, ok := entries[hash]
		if !ok {
			t.Fatalf("%s: missing fixture entry for ogg sha256=%s", sc.name, hash)
		}
		if entry.SampleRate != sc.sampleRate {
			t.Fatalf("%s: fixture sample_rate=%d want %d", sc.name, entry.SampleRate, sc.sampleRate)
		}
		if entry.Channels != sc.channels {
			t.Fatalf("%s: fixture channels=%d want %d", sc.name, entry.Channels, sc.channels)
		}
		samples, err := decodeFloat32LEBase64(entry.DecodedF32Base64)
		if err != nil {
			t.Fatalf("%s: decode fixture samples: %v", sc.name, err)
		}
		if len(samples) == 0 {
			t.Fatalf("%s: fixture samples are empty", sc.name)
		}
	}
}

func TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec(t *testing.T) {
	if !checkOpusdecAvailable() {
		t.Skip("opusdec not available; fixture honesty check requires live opusdec")
	}

	scenarios := buildCrossvalFixtureScenarios(t)
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			live, err := decodeWithOpusdecCLI(sc.ogg)
			if err != nil {
				t.Fatalf("live opusdec decode failed: %v", err)
			}
			fixture, err := decodeWithOpusdecFixture(sc.ogg)
			if err != nil {
				t.Fatalf("fixture decode failed: %v", err)
			}
			if len(live) != len(fixture) {
				t.Fatalf("decoded length mismatch: live=%d fixture=%d", len(live), len(fixture))
			}

			maxAbs := float64(0)
			var sumSq float64
			for i := range live {
				diff := float64(live[i] - fixture[i])
				if abs := math.Abs(diff); abs > maxAbs {
					maxAbs = abs
				}
				sumSq += diff * diff
			}
			rmse := math.Sqrt(sumSq / float64(len(live)))
			if maxAbs > 2e-4 {
				t.Fatalf("fixture drift vs live opusdec: max abs diff %.6f > 0.0002", maxAbs)
			}
			if rmse > 2e-5 {
				t.Fatalf("fixture drift vs live opusdec: RMSE %.7f > 0.00002", rmse)
			}
		})
	}
}

func TestOpusdecCrossvalFixtureMatrix(t *testing.T) {
	t.Setenv("GOPUS_DISABLE_OPUSDEC", "1")

	scenarios := buildCrossvalFixtureScenarios(t)
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			decoded, err := decodeWithOpusdec(sc.ogg)
			if err != nil {
				t.Fatalf("fixture decode failed: %v", err)
			}
			if len(decoded) == 0 {
				t.Fatal("fixture decode returned no samples")
			}
			if len(decoded)%sc.channels != 0 {
				t.Fatalf("decoded sample count %d not divisible by channels %d", len(decoded), sc.channels)
			}
			for i, s := range decoded {
				if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
					t.Fatalf("decoded sample[%d] is non-finite: %v", i, s)
				}
			}

			outputEnergy := computeEnergy(decoded)
			if sc.expectNearSilence {
				if outputEnergy > 1e-6 {
					t.Fatalf("expected near silence, got energy %.9f", outputEnergy)
				}
				return
			}

			if len(sc.input) == 0 {
				t.Fatal("test scenario missing input samples")
			}
			inputEnergy := computeEnergy(float32Slice(sc.input))
			if inputEnergy <= 0 {
				t.Fatalf("invalid input energy %.9f", inputEnergy)
			}
			ratio := outputEnergy / inputEnergy
			if ratio < sc.minEnergyRatio {
				t.Fatalf("energy ratio too low: got %.4f want >= %.4f", ratio, sc.minEnergyRatio)
			}
		})
	}
}
