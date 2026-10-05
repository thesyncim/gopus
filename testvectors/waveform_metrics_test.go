package testvectors

import (
	"math"
	"math/rand"
	"testing"
)

func referenceBestWaveformDelayByCorrelation(decoded, reference []float32, maxDelay int) (int, waveformStats) {
	bestDelay := 0
	bestStats := computeWaveformStats(decoded, reference)
	for delay := -maxDelay; delay <= maxDelay; delay++ {
		alignedDecoded, alignedReference := alignFloat32ForDelay(decoded, reference, delay)
		stats := computeWaveformStats(alignedDecoded, alignedReference)
		if stats.Samples == 0 {
			continue
		}
		if stats.Correlation > bestStats.Correlation || (stats.Correlation == bestStats.Correlation && qualityAbsInt(delay) < qualityAbsInt(bestDelay)) {
			bestDelay = delay
			bestStats = stats
		}
	}
	return bestDelay, bestStats
}

func sameStats(a, b waveformStats) bool {
	return a.Samples == b.Samples && same(a.Correlation, b.Correlation) && same(a.RMSRatio, b.RMSRatio) &&
		same(a.MeanAbsErr, b.MeanAbsErr) && same(a.RMSErr, b.RMSErr) && same(a.MaxAbsErr, b.MaxAbsErr)
}

func same(a, b float64) bool { return a == b || math.IsNaN(a) && math.IsNaN(b) }

func TestWaveformBoundSearchMatchesFullReference(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2048 + rng.Intn(2048)
		reference := make([]float32, n)
		decoded := make([]float32, n)
		for i := range reference {
			reference[i] = float32(rng.Intn(65535)-32767) / 32768
			decoded[i] = float32(rng.Intn(65535)-32767) / 32768
		}
		if seed%2 == 0 {
			copy(decoded[1:], reference[:n-1])
		}
		for _, maxDelay := range []int{-4, 0, 1, 4, 9} {
			gotDelay, gotStats := bestWaveformDelayByCorrelation(decoded, reference, maxDelay)
			wantDelay, wantStats := referenceBestWaveformDelayByCorrelation(decoded, reference, maxDelay)
			if gotDelay != wantDelay || !sameStats(gotStats, wantStats) {
				t.Fatalf("seed=%d maxDelay=%d got=(%d,%+v) want=(%d,%+v)", seed, maxDelay, gotDelay, gotStats, wantDelay, wantStats)
			}
		}
	}

	cases := []struct {
		decoded, reference []float32
		maxDelay           int
	}{
		{nil, nil, 5},
		{make([]float32, 3000), make([]float32, 3000), 8},
		{[]float32{1, 2, 3}, []float32{1, 2, 3}, 9},
		{[]float32{1, float32(math.NaN()), 3}, []float32{2, 4, 6}, 4},
		{[]float32{1, float32(math.Inf(1)), 3}, []float32{2, 4, 6}, 4},
		{[]float32{math.Float32frombits(1), 0, -math.Float32frombits(1)}, []float32{0, math.Float32frombits(1), 0}, 2},
		{[]float32{3.4028235e38, 3.4028235e38, -3.4028235e38}, []float32{3.4028235e38, -3.4028235e38, 3.4028235e38}, 2},
	}
	for i, tc := range cases {
		gotDelay, gotStats := bestWaveformDelayByCorrelation(tc.decoded, tc.reference, tc.maxDelay)
		wantDelay, wantStats := referenceBestWaveformDelayByCorrelation(tc.decoded, tc.reference, tc.maxDelay)
		if gotDelay != wantDelay || !sameStats(gotStats, wantStats) {
			t.Fatalf("case=%d maxDelay=%d got=(%d,%+v) want=(%d,%+v)", i, tc.maxDelay, gotDelay, gotStats, wantDelay, wantStats)
		}
	}
	tieDecoded := []float32{1, 0, 1}
	tieReference := []float32{0, 1, 0}
	tieDelay, _ := bestWaveformDelayByCorrelation(tieDecoded, tieReference, 1)
	if tieDelay != -1 {
		t.Fatalf("equal-scoring signed delays select %d, want -1", tieDelay)
	}
}

func TestWaveformCorrelationUpperBoundNeverUnderestimates(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed * 1777))
		n := 4096 + rng.Intn(4096)
		decoded := make([]float32, n)
		reference := make([]float32, n)
		for i := 0; i < n; i++ {
			decoded[i] = float32(rng.Intn(65535)-32767) / 32768
			reference[i] = float32(rng.Intn(65535)-32767) / 32768
		}
		decodedBounds, ok := makeWaveformEnergyBounds(decoded)
		if !ok {
			t.Fatal("finite decoded samples rejected")
		}
		referenceBounds, ok := makeWaveformEnergyBounds(reference)
		if !ok {
			t.Fatal("finite reference samples rejected")
		}
		for delay := -7; delay <= 7; delay++ {
			alignedDecoded, alignedReference := alignFloat32ForDelay(decoded, reference, delay)
			stats := computeWaveformStats(alignedDecoded, alignedReference)
			if stats.Samples < 2048 {
				continue
			}
			decodedStart, referenceStart := 0, 0
			if delay > 0 {
				decodedStart = delay
			} else if delay < 0 {
				referenceStart = -delay
			}
			for processed := 1024; processed < stats.Samples; processed += 1024 {
				var dot, refPower, decPower float64
				for i := 0; i < processed; i++ {
					ref := float64(alignedReference[i])
					dec := float64(alignedDecoded[i])
					dot += ref * dec
					refPower += ref * ref
					decPower += dec * dec
				}
				if refPower == 0 || decPower == 0 {
					continue
				}
				refTail, _ := referenceBounds.rangeUpper(referenceStart+processed, referenceStart+stats.Samples)
				decTail, _ := decodedBounds.rangeUpper(decodedStart+processed, decodedStart+stats.Samples)
				refEnergy, _ := referenceBounds.rangeLower(referenceStart, referenceStart+stats.Samples)
				decEnergy, _ := decodedBounds.rangeLower(decodedStart, decodedStart+stats.Samples)
				refFullLower, _ := waveformAccumulatedEnergyLower(refEnergy, stats.Samples)
				decFullLower, _ := waveformAccumulatedEnergyLower(decEnergy, stats.Samples)
				upper, ok := waveformCorrelationUpperBound(dot, math.Max(refPower, refFullLower), math.Max(decPower, decFullLower), stats.Samples-processed, refTail, decTail)
				if ok && upper < stats.Correlation {
					t.Fatalf("seed=%d delay=%d processed=%d upper=%.17g exact=%.17g", seed, delay, processed, upper, stats.Correlation)
				}
			}
		}
	}
}

func TestWaveformRoundedPowerLowerBoundNeverExceedsActual(t *testing.T) {
	rng := rand.New(rand.NewSource(993))
	for seed := 0; seed < 40; seed++ {
		n := 1 + rng.Intn(8192)
		samples := make([]float32, n)
		for i := range samples {
			switch i % 3 {
			case 0:
				samples[i] = math.Float32frombits(uint32(rng.Intn(1<<24)) | 0x3f000000)
			case 1:
				samples[i] = float32(rng.Intn(65535)-32767) / 32768
			default:
				samples[i] = math.Float32frombits(uint32(1 + rng.Intn(1<<23)))
			}
		}
		bounds, ok := makeWaveformEnergyBounds(samples)
		if !ok {
			t.Fatal("finite samples rejected")
		}
		start := rng.Intn(n)
		end := start + rng.Intn(n-start+1)
		energy, ok := bounds.rangeLower(start, end)
		if !ok {
			t.Fatal("finite energy range rejected")
		}
		powerLower, ok := waveformAccumulatedEnergyLower(energy, end-start)
		if !ok {
			t.Fatal("energy power lower bound rejected")
		}
		var actual float64
		for _, sample := range samples[start:end] {
			value := float64(sample)
			actual += value * value
		}
		if powerLower > actual {
			t.Fatalf("seed=%d range=[%d:%d] lower=%.17g actual=%.17g", seed, start, end, powerLower, actual)
		}
	}
}

var (
	boundBenchmarkDelay int
	boundBenchmarkStats waveformStats
)

func BenchmarkBestWaveformDelayByCorrelation(b *testing.B) {
	fixture, err := loadLibopusDecoderRateMatrixFixture()
	if err != nil {
		b.Fatal(err)
	}
	var selected *libopusDecoderRateMatrixCaseFile
	selectedDelay := 0
	selectedWork := int64(-1)
	for i := range fixture.Cases {
		c := &fixture.Cases[i]
		if c.APIRate == 48000 {
			continue
		}
		apiFrameSize := c.FrameSize * c.APIRate / 48000
		if apiFrameSize < 1 {
			apiFrameSize = c.APIRate / 50
		}
		maxDelay := max(4*apiFrameSize*c.Channels, c.APIRate/50*c.Channels)
		work := int64(len(c.decodedSamples)) * int64(maxDelay)
		if work > selectedWork {
			selected = c
			selectedDelay = maxDelay
			selectedWork = work
		}
	}
	if selected == nil || len(selected.decodedSamples) < 2 {
		b.Fatal("no non-48 kHz fixture row is available for the delay-search benchmark")
	}
	reference := selected.decodedSamples
	decoded := make([]float32, len(reference))
	decoded[0] = 0
	copy(decoded[1:], reference[:len(reference)-1])
	b.Logf("fixture=%s@%dHz samples=%d maxDelay=%d", selected.Name, selected.APIRate, len(reference), selectedDelay)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		boundBenchmarkDelay, boundBenchmarkStats = bestWaveformDelayByCorrelation(decoded, reference, selectedDelay)
	}
}
