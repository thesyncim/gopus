//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares gopus DownsamplingResampler with libopus silk_resampler
// for 48kHz -> 16kHz encoder downsampling, feeding identical int16 samples to
// both implementations and comparing output sample-by-sample.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
)

// TestSilkResamplerCompare48to16 feeds the EXACT same int16 samples to both
// gopus DownsamplingResampler and libopus silk_resampler (48kHz -> 16kHz
// encoder downsampling), then compares the output sample-by-sample over
// 50 frames to detect both immediate and accumulated divergence.
func TestSilkResamplerCompare48to16(t *testing.T) {
	const (
		fsIn     = 48000
		fsOut    = 16000
		nFrames  = 50
		frameIn  = 960 // 20ms at 48kHz
		frameOut = 320 // 20ms at 16kHz
	)

	totalIn := nFrames * frameIn
	totalOut := nFrames * frameOut

	// --- Generate a multi-frequency test signal in int16 ---
	// Use frequencies that are not harmonically related to avoid
	// ambiguous correlation alignment: 173 Hz, 911 Hz, 3011 Hz, 5501 Hz.
	inInt16 := make([]int16, totalIn)
	for i := range inInt16 {
		tm := float64(i) / float64(fsIn)
		v := 0.4*math.Sin(2*math.Pi*173*tm) +
			0.25*math.Sin(2*math.Pi*911*tm) +
			0.2*math.Sin(2*math.Pi*3011*tm) +
			0.15*math.Sin(2*math.Pi*5501*tm)
		// Scale to int16 range and clamp.
		scaled := v * 32767.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		inInt16[i] = int16(scaled)
	}

	// --- libopus: process all frames through a single persistent state ---
	outLibopus, ret := ProcessLibopusResamplerEncMultiframe(
		inInt16, nFrames, frameIn, frameOut, fsIn, fsOut,
	)
	if ret != 0 {
		t.Fatalf("libopus silk_resampler failed: %d", ret)
	}

	// --- gopus: process frame-by-frame through DownsamplingResampler ---
	goResampler := silk.NewDownsamplingResampler(fsIn, fsOut)
	outGopus := make([]int16, totalOut)

	for f := 0; f < nFrames; f++ {
		inStart := f * frameIn
		outStart := f * frameOut

		// Convert this frame's int16 to float32 in [-1,1] range.
		// float32 has 24-bit mantissa, so int16 values round-trip exactly
		// through float32(s)/32768.0 * 32768.0.
		frameFloat := make([]float32, frameIn)
		for i := 0; i < frameIn; i++ {
			frameFloat[i] = float32(inInt16[inStart+i]) / 32768.0
		}

		// ProcessInto returns float32 output in [-1,1]
		outFloat := make([]float32, frameOut)
		n := goResampler.ProcessInto(frameFloat, outFloat)
		if n != frameOut {
			t.Fatalf("frame %d: gopus output length %d, expected %d", f, n, frameOut)
		}

		// Convert gopus float32 output back to int16 using the same
		// rounding as libopus (FLOAT2INT16): scale by 32768, clamp,
		// then truncate to int16.
		for i := 0; i < frameOut; i++ {
			scaled := float64(outFloat[i]) * 32768.0
			if scaled > 32767.0 {
				scaled = 32767.0
			} else if scaled < -32768.0 {
				scaled = -32768.0
			}
			outGopus[outStart+i] = int16(scaled)
		}
	}

	// --- Compare sample-by-sample ---
	var (
		maxAbsDiff  int
		maxDiffIdx  int
		firstDivIdx = -1
		sumDiff     int64 // signed sum to detect systematic offset
		diffCount   int   // number of samples that differ
	)

	for i := 0; i < totalOut; i++ {
		diff := int(outGopus[i]) - int(outLibopus[i])
		absDiff := diff
		if absDiff < 0 {
			absDiff = -absDiff
		}

		if absDiff > 0 {
			diffCount++
			sumDiff += int64(diff)
			if firstDivIdx < 0 {
				firstDivIdx = i
			}
		}

		if absDiff > maxAbsDiff {
			maxAbsDiff = absDiff
			maxDiffIdx = i
		}
	}

	// --- Report results ---
	t.Logf("Total output samples: %d", totalOut)
	t.Logf("Samples with diff != 0: %d / %d (%.2f%%)",
		diffCount, totalOut, 100.0*float64(diffCount)/float64(totalOut))
	t.Logf("Max abs diff: %d (at sample %d, frame %d, offset %d)",
		maxAbsDiff, maxDiffIdx, maxDiffIdx/frameOut, maxDiffIdx%frameOut)

	if firstDivIdx >= 0 {
		t.Logf("First divergence at sample %d (frame %d, offset %d): gopus=%d libopus=%d",
			firstDivIdx, firstDivIdx/frameOut, firstDivIdx%frameOut,
			outGopus[firstDivIdx], outLibopus[firstDivIdx])
	}

	if diffCount > 0 {
		meanDiff := float64(sumDiff) / float64(diffCount)
		t.Logf("Mean signed diff (among differing samples): %.4f", meanDiff)
		if math.Abs(meanDiff) > 0.5 {
			t.Logf("SYSTEMATIC OFFSET DETECTED: mean signed diff %.4f suggests a consistent bias", meanDiff)
		} else {
			t.Logf("No significant systematic offset (mean diff close to zero)")
		}
	}

	// Print first 10 divergences for debugging
	printed := 0
	for i := 0; i < totalOut && printed < 10; i++ {
		diff := int(outGopus[i]) - int(outLibopus[i])
		if diff != 0 {
			t.Logf("  sample %5d (frame %2d, off %3d): gopus=%6d  libopus=%6d  diff=%d",
				i, i/frameOut, i%frameOut, outGopus[i], outLibopus[i], diff)
			printed++
		}
	}

	// Also check if divergence grows over time (accumulated state drift)
	if diffCount > 0 {
		// Compare max abs diff in first half vs second half
		var maxFirst, maxSecond int
		half := totalOut / 2
		for i := 0; i < half; i++ {
			d := int(outGopus[i]) - int(outLibopus[i])
			if d < 0 {
				d = -d
			}
			if d > maxFirst {
				maxFirst = d
			}
		}
		for i := half; i < totalOut; i++ {
			d := int(outGopus[i]) - int(outLibopus[i])
			if d < 0 {
				d = -d
			}
			if d > maxSecond {
				maxSecond = d
			}
		}
		t.Logf("Max abs diff first half: %d, second half: %d", maxFirst, maxSecond)
		if maxSecond > maxFirst*2 && maxSecond > 2 {
			t.Logf("WARNING: divergence appears to GROW over time (possible state accumulation drift)")
		}
	}

	// Compute SNR between gopus and libopus outputs
	var signalEnergy, noiseEnergy float64
	for i := 0; i < totalOut; i++ {
		s := float64(outLibopus[i])
		n := float64(outGopus[i]) - s
		signalEnergy += s * s
		noiseEnergy += n * n
	}
	if noiseEnergy > 0 && signalEnergy > 0 {
		snr := 10 * math.Log10(signalEnergy/noiseEnergy)
		t.Logf("SNR (gopus vs libopus): %.2f dB", snr)
	} else if noiseEnergy == 0 {
		t.Logf("SNR: infinite (perfect match)")
	}

	// Threshold: allow at most 1 LSB difference per sample (int16 quantization)
	// and no more than 10% of samples differing
	if maxAbsDiff > 1 {
		t.Errorf("FAIL: max abs diff %d exceeds 1 LSB threshold", maxAbsDiff)
	}
	if float64(diffCount)/float64(totalOut) > 0.10 {
		t.Errorf("FAIL: %.2f%% of samples differ (threshold: 10%%)",
			100.0*float64(diffCount)/float64(totalOut))
	}
	if diffCount == 0 {
		t.Logf("PERFECT MATCH: gopus and libopus resamplers produce identical int16 output")
	}
}

// TestSilkResamplerCompare48to16SingleFrame tests a single frame to isolate
// initial-state behavior from accumulated-state behavior.
func TestSilkResamplerCompare48to16SingleFrame(t *testing.T) {
	const (
		fsIn     = 48000
		fsOut    = 16000
		frameIn  = 960
		frameOut = 320
	)

	// Generate a chirp signal (frequency sweeps from 100 Hz to 7000 Hz)
	// in int16 format. Chirps are better than pure tones for resampler testing
	// because they exercise the filter across many frequencies.
	inInt16 := make([]int16, frameIn)
	for i := range inInt16 {
		tm := float64(i) / float64(fsIn)
		// Linear chirp: freq goes from 100 to 7000 Hz over 20ms
		frac := float64(i) / float64(frameIn)
		phase := 2 * math.Pi * (100.0*tm + 0.5*6900.0*tm*frac)
		v := 0.8 * math.Sin(phase)
		scaled := v * 32767.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		inInt16[i] = int16(scaled)
	}

	// libopus single frame
	outLibopus, ret := ProcessLibopusResamplerEncSingle(inInt16, fsIn, fsOut)
	if ret != 0 {
		t.Fatalf("libopus silk_resampler failed: %d", ret)
	}

	// gopus single frame
	goResampler := silk.NewDownsamplingResampler(fsIn, fsOut)
	frameFloat := make([]float32, frameIn)
	for i := 0; i < frameIn; i++ {
		frameFloat[i] = float32(inInt16[i]) / 32768.0
	}
	outFloat := make([]float32, frameOut)
	n := goResampler.ProcessInto(frameFloat, outFloat)
	if n != frameOut {
		t.Fatalf("gopus output length %d, expected %d", n, frameOut)
	}

	outGopus := make([]int16, frameOut)
	for i := 0; i < frameOut; i++ {
		scaled := float64(outFloat[i]) * 32768.0
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		outGopus[i] = int16(scaled)
	}

	// Compare
	var maxAbsDiff int
	firstDiv := -1
	for i := 0; i < frameOut; i++ {
		diff := int(outGopus[i]) - int(outLibopus[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > 0 && firstDiv < 0 {
			firstDiv = i
		}
		if diff > maxAbsDiff {
			maxAbsDiff = diff
		}
	}

	t.Logf("Single frame (chirp): max abs diff = %d", maxAbsDiff)
	if firstDiv >= 0 {
		t.Logf("First divergence at sample %d: gopus=%d libopus=%d",
			firstDiv, outGopus[firstDiv], outLibopus[firstDiv])

		// Print neighbourhood around first divergence
		start := firstDiv - 3
		if start < 0 {
			start = 0
		}
		end := firstDiv + 4
		if end > frameOut {
			end = frameOut
		}
		for i := start; i < end; i++ {
			marker := " "
			if i == firstDiv {
				marker = ">"
			}
			t.Logf("  %s [%3d] gopus=%6d  libopus=%6d  diff=%d",
				marker, i, outGopus[i], outLibopus[i],
				int(outGopus[i])-int(outLibopus[i]))
		}
	} else {
		t.Logf("PERFECT MATCH on single frame")
	}

	if maxAbsDiff > 1 {
		t.Errorf("Single frame max abs diff %d exceeds 1 LSB", maxAbsDiff)
	}
}

// TestSilkResamplerCompare48to16DirectInt16 is the most comprehensive
// comparison. It uses a different test signal (musical note harmonics) and
// provides detailed per-frame analysis to detect accumulated state drift.
func TestSilkResamplerCompare48to16DirectInt16(t *testing.T) {
	const (
		fsIn     = 48000
		fsOut    = 16000
		nFrames  = 50
		frameIn  = 960
		frameOut = 320
	)

	totalIn := nFrames * frameIn
	totalOut := nFrames * frameOut

	// Multi-frequency test signal in int16 using musical note harmonics
	inInt16 := make([]int16, totalIn)
	for i := range inInt16 {
		tm := float64(i) / float64(fsIn)
		v := 0.35*math.Sin(2*math.Pi*261.63*tm) + // C4
			0.25*math.Sin(2*math.Pi*523.25*tm) + // C5
			0.2*math.Sin(2*math.Pi*1046.5*tm) + // C6
			0.1*math.Sin(2*math.Pi*2093*tm) + // C7
			0.1*math.Sin(2*math.Pi*4186*tm) // C8
		scaled := v * 32767.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		inInt16[i] = int16(scaled)
	}

	// libopus multi-frame
	outLibopus, ret := ProcessLibopusResamplerEncMultiframe(
		inInt16, nFrames, frameIn, frameOut, fsIn, fsOut,
	)
	if ret != 0 {
		t.Fatalf("libopus silk_resampler failed: %d", ret)
	}

	// gopus multi-frame: feed float32 converted from exact int16
	goResampler := silk.NewDownsamplingResampler(fsIn, fsOut)
	outGopus := make([]int16, totalOut)
	for f := 0; f < nFrames; f++ {
		inStart := f * frameIn
		outStart := f * frameOut

		frameFloat := make([]float32, frameIn)
		for i := 0; i < frameIn; i++ {
			frameFloat[i] = float32(inInt16[inStart+i]) / 32768.0
		}

		outFloat := make([]float32, frameOut)
		n := goResampler.ProcessInto(frameFloat, outFloat)
		if n != frameOut {
			t.Fatalf("frame %d: gopus output %d samples, expected %d", f, n, frameOut)
		}

		for i := 0; i < frameOut; i++ {
			scaled := float64(outFloat[i]) * 32768.0
			if scaled > 32767.0 {
				scaled = 32767.0
			} else if scaled < -32768.0 {
				scaled = -32768.0
			}
			outGopus[outStart+i] = int16(scaled)
		}
	}

	// Comprehensive per-frame analysis
	type frameStat struct {
		maxDiff  int
		nDiff    int
		sumDiff  int64
		rmsFloat float64
	}
	stats := make([]frameStat, nFrames)

	globalMax := 0
	globalDiffCount := 0
	var globalSumDiff int64

	for f := 0; f < nFrames; f++ {
		off := f * frameOut
		var fs frameStat
		var sumSq float64
		for i := 0; i < frameOut; i++ {
			diff := int(outGopus[off+i]) - int(outLibopus[off+i])
			ad := diff
			if ad < 0 {
				ad = -ad
			}
			if ad > fs.maxDiff {
				fs.maxDiff = ad
			}
			if ad > 0 {
				fs.nDiff++
				fs.sumDiff += int64(diff)
			}
			sumSq += float64(diff) * float64(diff)
		}
		fs.rmsFloat = math.Sqrt(sumSq / float64(frameOut))
		stats[f] = fs

		if fs.maxDiff > globalMax {
			globalMax = fs.maxDiff
		}
		globalDiffCount += fs.nDiff
		globalSumDiff += fs.sumDiff
	}

	// Summary
	t.Logf("=== DIRECT INT16 COMPARISON (48kHz -> 16kHz, %d frames) ===", nFrames)
	t.Logf("Total samples: %d, differing: %d (%.2f%%)",
		totalOut, globalDiffCount, 100.0*float64(globalDiffCount)/float64(totalOut))
	t.Logf("Global max abs diff: %d", globalMax)

	if globalDiffCount > 0 {
		meanDiff := float64(globalSumDiff) / float64(globalDiffCount)
		t.Logf("Global mean signed diff: %.4f", meanDiff)
		if math.Abs(meanDiff) > 0.5 {
			t.Logf("SYSTEMATIC OFFSET: mean signed diff %.4f", meanDiff)
		}
	}

	// Per-frame breakdown (show first 5, last 5, and any with diffs)
	t.Logf("Per-frame stats (frame: maxDiff, nDiff/320, rms, meanSignedDiff):")
	for f := 0; f < nFrames; f++ {
		s := stats[f]
		show := f < 5 || f >= nFrames-5 || s.maxDiff > 0
		if !show {
			continue
		}
		meanStr := "n/a"
		if s.nDiff > 0 {
			meanStr = fmt.Sprintf("%.3f", float64(s.sumDiff)/float64(s.nDiff))
		}
		t.Logf("  frame %2d: max=%d  diff=%3d/%d  rms=%.4f  meanDiff=%s",
			f, s.maxDiff, s.nDiff, frameOut, s.rmsFloat, meanStr)
	}

	// Compute SNR
	var signalEnergy, noiseEnergy float64
	for i := 0; i < totalOut; i++ {
		s := float64(outLibopus[i])
		n := float64(outGopus[i]) - s
		signalEnergy += s * s
		noiseEnergy += n * n
	}
	if noiseEnergy > 0 && signalEnergy > 0 {
		snr := 10 * math.Log10(signalEnergy/noiseEnergy)
		t.Logf("SNR (gopus vs libopus): %.2f dB", snr)
	} else if noiseEnergy == 0 {
		t.Logf("SNR: infinite (perfect match)")
	}

	// Failure criteria
	if globalMax > 1 {
		t.Errorf("FAIL: max abs diff %d exceeds 1 LSB threshold", globalMax)
	}
	if float64(globalDiffCount)/float64(totalOut) > 0.10 {
		t.Errorf("FAIL: %.2f%% of samples differ (threshold: 10%%)",
			100.0*float64(globalDiffCount)/float64(totalOut))
	}
	if globalDiffCount == 0 {
		t.Logf("PERFECT MATCH: resamplers produce identical int16 output across all %d frames", nFrames)
	}
}
