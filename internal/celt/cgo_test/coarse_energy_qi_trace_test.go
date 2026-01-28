// Package cgo provides coarse energy qi value trace tests for comparing gopus vs libopus.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// QITraceEntry holds trace data for one band's qi computation
type QITraceEntry struct {
	Band      int
	Channel   int
	X         float64 // input energy
	OldE      float64 // previous frame energy (clamped)
	OldEBand  float64 // previous frame energy (unclamped)
	Prev      float64 // inter-band predictor
	F         float64 // prediction residual
	QI0       int     // initial qi (before adjustments)
	QI        int     // final qi (after Laplace encoding)
	Q         float64 // quantized delta
	Quantized float64 // final quantized energy
}

// traceCoarseEnergyQI manually computes qi values step-by-step to trace the encoding process
// This mirrors EncodeCoarseEnergy but captures intermediate values for debugging
func traceCoarseEnergyQI(energies []float64, nbBands int, intra bool, lm int, targetBits int) ([]QITraceEntry, []byte) {
	channels := 1 // Assume mono for now

	// Constants matching libopus (in log2 scale)
	DB6 := 1.0 // 6dB = 1.0 in log2 scale

	// Prediction coefficients from libopus
	alphaCoef := []float64{0.75, 0.822727, 0.857143, 0.875}
	betaCoefInter := []float64{0.039062, 0.070313, 0.101563, 0.132813}
	betaIntra := 0.149902

	var coef, beta float64
	if intra {
		coef = 0.0
		beta = betaIntra
	} else {
		coef = alphaCoef[lm]
		beta = betaCoefInter[lm]
	}

	// Get probability model
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0]
	if intra {
		prob = probModel[lm][1]
	}

	// Setup range encoder
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	budget := targetBits

	// Max decay bound
	maxDecay := 16.0 * DB6
	nbAvailableBytes := budget / 8
	if nbBands > 10 {
		limit := 0.125 * float64(nbAvailableBytes) * DB6
		if limit < maxDecay {
			maxDecay = limit
		}
	}

	// Encode intra flag (matches libopus quant_coarse_energy_impl line 171-172)
	re.EncodeBit(1, 3) // intra=1

	traces := make([]QITraceEntry, 0, nbBands*channels)
	prevBandEnergy := make([]float64, channels)
	prevEnergy := make([]float64, celt.MaxBands*channels) // Start with zeros (first frame)

	// Create a temporary encoder to use for Laplace encoding
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetRangeEncoder(re)
	encoder.SetFrameBitsForTest(targetBits)

	for band := 0; band < nbBands; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band

			x := energies[idx]

			// Previous frame energy (for prediction and decay bound)
			oldEBand := prevEnergy[c*celt.MaxBands+band]
			oldE := oldEBand
			minEnergy := -9.0 * DB6
			if oldE < minEnergy {
				oldE = minEnergy
			}

			// Prediction residual (this is the key calculation)
			// f = x - coef*oldE - prev[c]
			f := x - coef*oldE - prevBandEnergy[c]

			// Quantize to integer: qi = round(f)
			// Rounding to nearest integer is critical! (libopus line 199/204)
			qi0 := int(math.Floor(f/DB6 + 0.5))
			qi := qi0

			// Prevent energy from decaying too quickly (libopus lines 208-214)
			decayBound := math.Max(-28.0*DB6, oldEBand) - maxDecay
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / DB6)
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			// Bit budget constraints (libopus lines 218-226)
			tell := re.Tell()
			bitsLeft := budget - tell - 3*channels*(nbBands-band)
			if band != 0 && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}

			// Encode with Laplace (libopus lines 229-247)
			qiBeforeEncode := qi
			if budget-tell >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = encoder.TestEncodeLaplace(qi, fs, decay)
			} else if budget-tell >= 2 {
				if qi > 1 {
					qi = 1
				}
				if qi < -1 {
					qi = -1
				}
				// Zigzag encoding
				var s int
				if qi < 0 {
					s = -2*qi - 1
				} else {
					s = 2 * qi
				}
				re.EncodeICDF(s, []byte{2, 1, 0}, 2)
			} else if budget-tell >= 1 {
				if qi > 0 {
					qi = 0
				}
				re.EncodeBit(-qi, 1)
			} else {
				qi = -1
			}

			_ = qiBeforeEncode

			q := float64(qi) * DB6
			quantizedEnergy := coef*oldE + prevBandEnergy[c] + q

			// Record trace
			trace := QITraceEntry{
				Band:      band,
				Channel:   c,
				X:         x,
				OldE:      oldE,
				OldEBand:  oldEBand,
				Prev:      prevBandEnergy[c],
				F:         f,
				QI0:       qi0,
				QI:        qi,
				Q:         q,
				Quantized: quantizedEnergy,
			}
			traces = append(traces, trace)

			// Update inter-band predictor (libopus line 257)
			// prev[c] = prev[c] + q - beta*q
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	bytes := re.Done()
	return traces, bytes
}

// traceLibopusCoarseEnergyQI uses the existing Laplace encoding functions to simulate libopus behavior
// We encode each qi value individually and track the sequence
func traceLibopusCoarseEnergyQI(energies []float64, nbBands int, intra bool, lm int, targetBits int) ([]QITraceEntry, []byte, error) {
	channels := 1

	DB6 := 1.0

	// Prediction coefficients from libopus
	alphaCoef := []float64{0.75, 0.822727, 0.857143, 0.875}
	betaCoefInter := []float64{0.039062, 0.070313, 0.101563, 0.132813}
	betaIntra := 0.149902

	var coef, beta float64
	if intra {
		coef = 0.0
		beta = betaIntra
	} else {
		coef = alphaCoef[lm]
		beta = betaCoefInter[lm]
	}

	// Get probability model
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0]
	if intra {
		prob = probModel[lm][1]
	}

	budget := targetBits

	// Max decay bound
	maxDecay := 16.0 * DB6
	nbAvailableBytes := budget / 8
	if nbBands > 10 {
		limit := 0.125 * float64(nbAvailableBytes) * DB6
		if limit < maxDecay {
			maxDecay = limit
		}
	}

	// Collect qi values and fs/decay for Laplace encoding
	qiValues := make([]int, 0, nbBands)
	fsValues := make([]int, 0, nbBands)
	decayValues := make([]int, 0, nbBands)

	traces := make([]QITraceEntry, 0, nbBands*channels)
	prevBandEnergy := make([]float64, channels)
	prevEnergy := make([]float64, celt.MaxBands*channels)

	// First pass: compute qi values (before Laplace encoding might clamp them)
	for band := 0; band < nbBands; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band

			x := energies[idx]

			oldEBand := prevEnergy[c*celt.MaxBands+band]
			oldE := oldEBand
			minEnergy := -9.0 * DB6
			if oldE < minEnergy {
				oldE = minEnergy
			}

			f := x - coef*oldE - prevBandEnergy[c]
			qi0 := int(math.Floor(f/DB6 + 0.5))
			qi := qi0

			// Decay bound adjustment
			decayBound := math.Max(-28.0*DB6, oldEBand) - maxDecay
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / DB6)
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			// We can't easily compute exact bit budget constraints here without
			// knowing exact tell values, so we'll just record the qi
			pi := 2 * band
			if pi > 40 {
				pi = 40
			}
			fs := int(prob[pi]) << 7
			decay := int(prob[pi+1]) << 6

			qiValues = append(qiValues, qi)
			fsValues = append(fsValues, fs)
			decayValues = append(decayValues, decay)

			trace := QITraceEntry{
				Band:     band,
				Channel:  c,
				X:        x,
				OldE:     oldE,
				OldEBand: oldEBand,
				Prev:     prevBandEnergy[c],
				F:        f,
				QI0:      qi0,
				QI:       qi, // Will be updated after encoding
			}
			traces = append(traces, trace)

			// Assume encoding succeeds and update state
			q := float64(qi) * DB6
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Use libopus to encode the sequence and get the actual qi values
	libBytes, libQiValues, err := EncodeLaplaceSequence(qiValues, fsValues, decayValues)
	if err != nil {
		return nil, nil, err
	}

	// Update traces with actual qi values from libopus
	for i := range traces {
		if i < len(libQiValues) {
			traces[i].QI = libQiValues[i]
			traces[i].Q = float64(libQiValues[i]) * DB6
		}
	}

	return traces, libBytes, nil
}

// TestCoarseEnergyQITrace compares qi values between gopus and libopus
func TestCoarseEnergyQITrace(t *testing.T) {
	t.Log("=== Coarse Energy QI Value Comparison ===")
	t.Log("Comparing quantized energy indices (qi) between gopus and libopus")
	t.Log("")

	// Test configuration
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate test signal (440 Hz sine wave)
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Compute band energies using gopus
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdct := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	rawEnergies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Logf("Frame size: %d, LM: %d, Bands: %d", frameSize, lm, nbBands)
	t.Log("")

	// Display raw energies for first 10 bands
	t.Log("Input band energies (log2 scale, mean-relative):")
	for band := 0; band < 10 && band < nbBands; band++ {
		t.Logf("  Band %2d: %.4f", band, rawEnergies[band])
	}
	t.Log("")

	// Trace gopus encoding
	targetBits := 64000 * frameSize / 48000
	intra := true

	gopusTraces, goBytes := traceCoarseEnergyQI(rawEnergies, nbBands, intra, lm, targetBits)

	t.Logf("Gopus encoded %d bytes", len(goBytes))

	// Trace libopus encoding using the Laplace sequence function
	libTraces, libBytes, err := traceLibopusCoarseEnergyQI(rawEnergies, nbBands, intra, lm, targetBits)
	if err != nil {
		t.Logf("Warning: libopus trace failed: %v", err)
		t.Log("Continuing with gopus-only analysis...")
	} else {
		t.Logf("Libopus encoded %d bytes (Laplace sequence only)", len(libBytes))
	}
	t.Log("")

	// Compare qi values for bands 0-9
	t.Log("=== QI Value Comparison (Bands 0-9) ===")
	t.Log("")
	t.Logf("%-6s | %-10s | %-10s | %-8s | %-8s | %-8s | %-8s | %s",
		"Band", "x", "f", "qi0(go)", "qi(go)", "qi(lib)", "Laplace", "Match")
	t.Log("-------+------------+------------+----------+----------+----------+----------+-------")

	mismatchCount := 0
	for i := 0; i < 10 && i < len(gopusTraces); i++ {
		goT := gopusTraces[i]

		libQI := "N/A"
		match := "N/A"
		if libTraces != nil && i < len(libTraces) {
			libQI = fmt.Sprintf("%d", libTraces[i].QI)
			if goT.QI == libTraces[i].QI {
				match = "YES"
			} else {
				match = "NO"
				mismatchCount++
			}
		}

		// Show Laplace encoding params
		probModel := celt.GetEProbModel()
		prob := probModel[lm][1] // intra mode
		pi := 2 * goT.Band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6

		t.Logf("%6d | %10.4f | %10.4f | %8d | %8d | %8s | fs=%d d=%d | %s",
			goT.Band, goT.X, goT.F, goT.QI0, goT.QI, libQI, fs, decay, match)
	}

	t.Log("")
	if libTraces != nil {
		if mismatchCount > 0 {
			t.Logf("RESULT: %d qi value mismatches found in bands 0-9", mismatchCount)
		} else {
			t.Log("RESULT: All qi values match for bands 0-9")
		}
	}

	// Show encoded bytes
	t.Log("")
	t.Log("=== Encoded Byte Comparison ===")
	showBytes := 10
	if showBytes > len(goBytes) {
		showBytes = len(goBytes)
	}
	t.Logf("Gopus bytes [0:%d]: %v", showBytes, goBytes[:showBytes])

	if libBytes != nil {
		showLib := showBytes
		if showLib > len(libBytes) {
			showLib = len(libBytes)
		}
		t.Logf("Libopus Laplace bytes [0:%d]: %v", showLib, libBytes[:showLib])
	}
}

// TestCoarseEnergyQITraceSimple tests with simple/known energy values
func TestCoarseEnergyQITraceSimple(t *testing.T) {
	t.Log("=== Simple QI Trace Test with Known Values ===")
	t.Log("")

	// Use simple known energy values to make debugging easier
	nbBands := 21
	lm := 3 // 20ms frame
	intra := true

	// Create simple test energies: linearly increasing values
	energies := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		energies[i] = float64(i) * 0.5 // 0.0, 0.5, 1.0, 1.5, ...
	}

	t.Log("Test energies (log2 scale, bands 0-9):")
	for i := 0; i < 10; i++ {
		t.Logf("  Band %d: %.2f", i, energies[i])
	}
	t.Log("")

	targetBits := 1280 // 64kbps * 20ms

	// Get gopus trace
	gopusTraces, goBytes := traceCoarseEnergyQI(energies, nbBands, intra, lm, targetBits)

	// Get libopus trace
	libTraces, libBytes, err := traceLibopusCoarseEnergyQI(energies, nbBands, intra, lm, targetBits)
	if err != nil {
		t.Logf("Warning: libopus trace failed: %v", err)
	}

	// Compare
	t.Log("QI comparison for bands 0-9:")
	t.Logf("%-6s | %-10s | %-10s | %-6s | %-6s | %s",
		"Band", "f(gopus)", "prev", "qi(go)", "qi(lib)", "Match")
	t.Log("-------+------------+------------+--------+--------+------")

	allMatch := true
	for i := 0; i < 10 && i < len(gopusTraces); i++ {
		goT := gopusTraces[i]

		libQI := "N/A"
		match := "N/A"
		if libTraces != nil && i < len(libTraces) {
			libQI = fmt.Sprintf("%d", libTraces[i].QI)
			if goT.QI == libTraces[i].QI {
				match = "YES"
			} else {
				match = "NO"
				allMatch = false
			}
		}

		t.Logf("%6d | %10.4f | %10.4f | %6d | %6s | %s",
			goT.Band, goT.F, goT.Prev, goT.QI, libQI, match)
	}

	t.Log("")
	if libTraces != nil {
		if allMatch {
			t.Log("All qi values MATCH")
		} else {
			t.Log("Some qi values DIFFER")
		}
	}

	t.Log("")
	t.Logf("Gopus output: %d bytes", len(goBytes))
	if libBytes != nil {
		t.Logf("Libopus output: %d bytes", len(libBytes))
	}

	showLen := 20
	if showLen > len(goBytes) {
		showLen = len(goBytes)
	}
	t.Logf("Gopus bytes:   %v", goBytes[:showLen])
	if libBytes != nil {
		showLib := showLen
		if showLib > len(libBytes) {
			showLib = len(libBytes)
		}
		t.Logf("Libopus bytes: %v", libBytes[:showLib])
	}
}

// TestCoarseEnergyQITraceZero tests with zero energy values
func TestCoarseEnergyQITraceZero(t *testing.T) {
	t.Log("=== Zero Energy QI Trace Test ===")
	t.Log("")

	nbBands := 21
	lm := 3
	intra := true

	// All zero energies (silence)
	energies := make([]float64, nbBands)

	t.Log("Test: All-zero energies (silence)")
	t.Log("")

	targetBits := 1280

	// Get gopus trace
	gopusTraces, goBytes := traceCoarseEnergyQI(energies, nbBands, intra, lm, targetBits)

	// Get libopus trace
	libTraces, libBytes, err := traceLibopusCoarseEnergyQI(energies, nbBands, intra, lm, targetBits)
	if err != nil {
		t.Logf("Warning: libopus trace failed: %v", err)
	}

	// Compare bands 0-5
	t.Log("QI comparison for bands 0-5:")
	for i := 0; i < 6 && i < len(gopusTraces); i++ {
		goT := gopusTraces[i]

		libQI := "N/A"
		match := "N/A"
		if libTraces != nil && i < len(libTraces) {
			libQI = fmt.Sprintf("%d", libTraces[i].QI)
			if goT.QI == libTraces[i].QI {
				match = "MATCH"
			} else {
				match = "DIFFER"
			}
		}

		t.Logf("  Band %d: f=%.4f, gopus qi=%d, libopus qi=%s  [%s]", i, goT.F, goT.QI, libQI, match)
	}

	t.Log("")
	libBytesLen := 0
	if libBytes != nil {
		libBytesLen = len(libBytes)
	}
	t.Logf("Encoded: gopus=%d bytes, libopus=%d bytes", len(goBytes), libBytesLen)
}

// TestCoarseEnergyQITraceAllBands traces all 21 bands for complete comparison
func TestCoarseEnergyQITraceAllBands(t *testing.T) {
	t.Log("=== Complete QI Trace for All Bands ===")
	t.Log("")

	// Test configuration
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate test signal (440 Hz sine wave)
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Compute band energies using gopus
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdct := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	rawEnergies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)

	// Trace gopus encoding
	targetBits := 64000 * frameSize / 48000
	intra := true

	gopusTraces, _ := traceCoarseEnergyQI(rawEnergies, nbBands, intra, lm, targetBits)

	// Trace libopus encoding
	libTraces, _, err := traceLibopusCoarseEnergyQI(rawEnergies, nbBands, intra, lm, targetBits)
	if err != nil {
		t.Fatalf("libopus trace failed: %v", err)
	}

	// Compare all bands
	t.Log("Complete QI comparison for all 21 bands:")
	t.Log("")
	t.Logf("%-6s | %-10s | %-10s | %-6s | %-6s | %s",
		"Band", "x (energy)", "f (resid)", "qi(go)", "qi(lib)", "Match")
	t.Log("-------+------------+------------+--------+--------+------")

	mismatchCount := 0
	for i := 0; i < len(gopusTraces) && i < len(libTraces); i++ {
		goT := gopusTraces[i]
		libT := libTraces[i]

		match := "YES"
		if goT.QI != libT.QI {
			match = "NO"
			mismatchCount++
		}

		t.Logf("%6d | %10.4f | %10.4f | %6d | %6d | %s",
			goT.Band, goT.X, goT.F, goT.QI, libT.QI, match)
	}

	t.Log("")
	if mismatchCount > 0 {
		t.Errorf("FAIL: %d qi value mismatches found", mismatchCount)
	} else {
		t.Logf("SUCCESS: All %d qi values match between gopus and libopus", len(gopusTraces))
	}
}
