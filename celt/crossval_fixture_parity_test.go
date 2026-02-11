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
	numFrames         int
	ogg               []byte
	input             []float64
	expectNearSilence bool
	minEnergyRatio    float64
	minSNRDB          float64
	minCorr           float64
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

func buildMonoSineFrames(freqHz float64, frameSize, numFrames int) [][]float64 {
	total := frameSize * numFrames
	all := make([]float64, total)
	for i := 0; i < total; i++ {
		t := float64(i) / 48000.0
		all[i] = 0.5 * math.Sin(2*math.Pi*freqHz*t)
	}
	frames := make([][]float64, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		frame := make([]float64, frameSize)
		copy(frame, all[start:end])
		frames[i] = frame
	}
	return frames
}

func buildMonoChirpFrames(frameSize, numFrames int, startHz, endHz float64) [][]float64 {
	total := frameSize * numFrames
	all := make([]float64, total)
	for i := 0; i < total; i++ {
		t := float64(i) / 48000.0
		progress := float64(i) / float64(total-1)
		freq := startHz + (endHz-startHz)*progress
		amp := 0.48 * (0.7 + 0.3*math.Sin(2*math.Pi*2.0*t))
		all[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	frames := make([][]float64, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		frame := make([]float64, frameSize)
		copy(frame, all[start:end])
		frames[i] = frame
	}
	return frames
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

	makeScenario := func(name string, channels int, packets [][]byte, input []float64, expectNearSilence bool, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		var ogg bytes.Buffer
		if err := writeOggOpus(&ogg, packets, 48000, channels); err != nil {
			t.Fatalf("%s: writeOggOpus failed: %v", name, err)
		}
		return crossvalFixtureScenario{
			name:              name,
			sampleRate:        48000,
			channels:          channels,
			numFrames:         len(packets),
			ogg:               ogg.Bytes(),
			input:             input,
			expectNearSilence: expectNearSilence,
			minEnergyRatio:    minEnergyRatio,
			minSNRDB:          minSNRDB,
			minCorr:           minCorr,
		}
	}

	encodeMonoFrame := func(name string, bitrate int, input []float64, expectNearSilence bool, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(1)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(input, 960)
		if err != nil {
			t.Fatalf("%s: encode failed: %v", name, err)
		}
		return makeScenario(name, 1, [][]byte{clonePacket(packet)}, input, expectNearSilence, minEnergyRatio, minSNRDB, minCorr)
	}

	encodeStereoFrame := func(name string, bitrate int, input []float64, expectNearSilence bool, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(2)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(input, 960)
		if err != nil {
			t.Fatalf("%s: encode failed: %v", name, err)
		}
		return makeScenario(name, 2, [][]byte{clonePacket(packet)}, input, expectNearSilence, minEnergyRatio, minSNRDB, minCorr)
	}

	encodeMonoFrames := func(name string, bitrate int, frames [][]float64, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
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
		return makeScenario(name, 1, packets, flattenFrames(frames), false, minEnergyRatio, minSNRDB, minCorr)
	}

	encodeStereoFramesCBR := func(name string, bitrate int, frames [][]float64, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(2)
		enc.SetBitrate(bitrate)
		enc.SetVBR(false)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(frame, 960)
			if err != nil {
				t.Fatalf("%s frame %d: encode failed: %v", name, i, err)
			}
			packets[i] = clonePacket(packet)
		}
		return makeScenario(name, 2, packets, flattenFrames(frames), false, minEnergyRatio, minSNRDB, minCorr)
	}

	return []crossvalFixtureScenario{
		encodeMonoFrames("mono_20ms_single", 64000, buildMonoSineFrames(440.0, 960, 3), 0.20, 22.5, 0.995),
		encodeStereoFrame("stereo_20ms_single", 128000, generateStereoSineWave(440.0, 880.0, 960), false, 0.20, 24.0, 0.995),
		encodeMonoFrame("mono_20ms_silence", 64000, make([]float64, 960), true, 0, 0, 0),
		encodeMonoFrames("mono_20ms_multiframe", 64000, [][]float64{
			generateSineWave(440.0, 960),
			generateSineWave(540.0, 960),
			generateSineWave(640.0, 960),
			generateSineWave(740.0, 960),
			generateSineWave(840.0, 960),
		}, 0.20, 2.0, 0.68),
		// Standalone CELT 20ms mono chirps are a challenging transient sweep case;
		// keep thresholds strict enough to catch major drift, but calibrated to
		// current CELT quality for this corner scenario.
		encodeMonoFrames("mono_20ms_chirp", 64000, buildMonoChirpFrames(960, 3, 180.0, 5200.0), 0.20, 4.0, 0.80),
		encodeMonoFrame("mono_20ms_impulse", 48000, buildMonoFrameImpulse(960), false, 0.10, 6.0, 0.879),
		encodeMonoFrame("mono_20ms_noise", 32000, buildMonoFramePseudoNoise(960), false, 0.08, 0.8, 0.55),
		encodeMonoFrame("mono_20ms_lowamp", 24000, scaleSignal(generateSineWave(880.0, 960), 0.12), false, 0.05, 18.0, 0.99),
		encodeStereoFrame("stereo_20ms_chirp", 96000, buildStereoFrameChirp(960), false, 0.15, 19.9, 0.99),
		encodeStereoFrame("stereo_20ms_silence", 96000, make([]float64, 960*2), true, 0, 0, 0),
		encodeStereoFramesCBR("stereo_20ms_multiframe", 96000, [][]float64{
			buildStereoFrameDualTone(960, 300.0, 500.0),
			buildStereoFrameDualTone(960, 520.0, 920.0),
			buildStereoFrameDualTone(960, 760.0, 1240.0),
			buildStereoFrameDualTone(960, 990.0, 1670.0),
		}, 0.15, 19.8, 0.99),
	}
}

func scaleSignal(in []float64, gain float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * gain
	}
	return out
}

func computeAlignedQualityMetrics(input []float64, decoded []float32, channels int) (snrDB float64, corr float64, energyRatio float64) {
	in := float32Slice(input)
	if len(in) == 0 || len(decoded) == 0 {
		return 0, 0, 0
	}

	if channels < 1 {
		channels = 1
	}

	// Search for best overlap lag first; Opus pre-skip and lookahead can shift
	// the decoded stream relative to the original frame-aligned source.
	const minOverlap = 128
	maxLag := 1024
	if maxPossible := min(len(in), len(decoded)) - minOverlap; maxPossible < maxLag {
		maxLag = maxPossible
	}
	if maxLag < 0 {
		return 0, 0, 0
	}

	bestLag := 0
	bestErr := math.Inf(1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		inStart, decStart := 0, 0
		if lag > 0 {
			decStart = lag
		} else if lag < 0 {
			inStart = -lag
		}
		n := len(in) - inStart
		if m := len(decoded) - decStart; m < n {
			n = m
		}
		if n < minOverlap {
			continue
		}
		trimStart := 64 * channels
		if trimStart > n/4 {
			trimStart = n / 4
		}
		trimEnd := 32 * channels
		if trimEnd > n/8 {
			trimEnd = n / 8
		}
		start := trimStart
		end := n - trimEnd
		if end-start < minOverlap {
			start = 0
			end = n
		}
		if end <= start {
			continue
		}

		var sigPow, noisePow float64
		for i := start; i < end; i++ {
			x := float64(in[inStart+i])
			y := float64(decoded[decStart+i])
			sigPow += x * x
			d := y - x
			noisePow += d * d
		}
		if sigPow <= 0 {
			continue
		}
		err := noisePow / sigPow
		if err < bestErr {
			bestErr = err
			bestLag = lag
		}
	}

	inStart, decStart := 0, 0
	if bestLag > 0 {
		decStart = bestLag
	} else if bestLag < 0 {
		inStart = -bestLag
	}
	n := len(in) - inStart
	if m := len(decoded) - decStart; m < n {
		n = m
	}
	if n <= 0 {
		return 0, 0, 0
	}
	trimStart := 64 * channels
	if trimStart > n/4 {
		trimStart = n / 4
	}
	trimEnd := 32 * channels
	if trimEnd > n/8 {
		trimEnd = n / 8
	}
	start := trimStart
	end := n - trimEnd
	if end-start < minOverlap {
		start = 0
		end = n
	}
	if end <= start {
		return 0, 0, 0
	}

	var sigPow, noisePow float64
	var sx, sy, sxx, syy, sxy float64
	for i := start; i < end; i++ {
		x := float64(in[inStart+i])
		y := float64(decoded[decStart+i])
		sigPow += x * x
		d := y - x
		noisePow += d * d
		sx += x
		sy += y
		sxx += x * x
		syy += y * y
		sxy += x * y
	}
	count := float64(end - start)
	if sigPow > 0 && noisePow > 0 {
		snrDB = 10 * math.Log10(sigPow/noisePow)
	} else if noisePow == 0 {
		snrDB = 120
	}

	num := count*sxy - sx*sy
	denX := count*sxx - sx*sx
	denY := count*syy - sy*sy
	if denX > 0 && denY > 0 {
		corr = num / math.Sqrt(denX*denY)
	}

	inE := computeEnergy(in[inStart+start : inStart+end])
	outE := computeEnergy(decoded[decStart+start : decStart+end])
	if inE > 0 {
		energyRatio = outE / inE
	}
	return snrDB, corr, energyRatio
}

func TestOpusdecCrossvalFixtureCoverage(t *testing.T) {
	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		t.Fatalf("load opusdec crossval fixture: %v", err)
	}

	scenarios := buildCrossvalFixtureScenarios(t)
	if len(entries) != len(scenarios) {
		t.Fatalf("fixture entry count mismatch: got %d want %d", len(entries), len(scenarios))
	}

	scenarioByName := make(map[string]crossvalFixtureScenario, len(scenarios))
	for _, sc := range scenarios {
		if _, ok := scenarioByName[sc.name]; ok {
			t.Fatalf("duplicate scenario name %q", sc.name)
		}
		scenarioByName[sc.name] = sc
	}

	for _, sc := range scenarios {
		hash := oggSHA256Hex(sc.ogg)
		entry, ok := entries[hash]
		if !ok {
			t.Fatalf("%s: missing fixture entry for ogg sha256=%s", sc.name, hash)
		}
		if entry.Name != sc.name {
			t.Fatalf("%s: fixture name mismatch for sha %s: got %q", sc.name, hash, entry.Name)
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
		expected := (sc.numFrames*960 - 312) * sc.channels
		if len(samples) != expected {
			t.Fatalf("%s: fixture decoded length mismatch: got %d want %d", sc.name, len(samples), expected)
		}
	}

	for _, e := range entries {
		if _, ok := scenarioByName[e.Name]; !ok {
			t.Fatalf("fixture has stale or unknown entry name %q", e.Name)
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
			expected := (sc.numFrames*960 - 312) * sc.channels
			if len(decoded) != expected {
				t.Fatalf("decoded length mismatch: got %d want %d", len(decoded), expected)
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

			snrDB, corr, alignedRatio := computeAlignedQualityMetrics(sc.input, decoded, sc.channels)
			if snrDB < sc.minSNRDB {
				t.Fatalf(
					"SNR too low: got %.3f dB want >= %.3f dB (corr=%.5f alignedEnergy=%.4f)",
					snrDB, sc.minSNRDB, corr, alignedRatio,
				)
			}
			if corr < sc.minCorr {
				t.Fatalf("correlation too low: got %.5f want >= %.5f", corr, sc.minCorr)
			}
			if alignedRatio < sc.minEnergyRatio {
				t.Fatalf("aligned energy ratio too low: got %.4f want >= %.4f", alignedRatio, sc.minEnergyRatio)
			}
		})
	}
}
