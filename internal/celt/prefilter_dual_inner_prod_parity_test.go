package celt

import (
	"encoding/json"
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"math/rand"
	"os"
	"testing"
)

func prefilterDualInnerProdF32Ref(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1 [4]float32
	var acc2 [4]float32
	i := 0
	for ; i < length-7; i += 8 {
		acc1[0] = opusmath.FMA32(x[i], y1[i], acc1[0])
		acc1[1] = opusmath.FMA32(x[i+1], y1[i+1], acc1[1])
		acc1[2] = opusmath.FMA32(x[i+2], y1[i+2], acc1[2])
		acc1[3] = opusmath.FMA32(x[i+3], y1[i+3], acc1[3])
		acc2[0] = opusmath.FMA32(x[i], y2[i], acc2[0])
		acc2[1] = opusmath.FMA32(x[i+1], y2[i+1], acc2[1])
		acc2[2] = opusmath.FMA32(x[i+2], y2[i+2], acc2[2])
		acc2[3] = opusmath.FMA32(x[i+3], y2[i+3], acc2[3])

		acc1[0] = opusmath.FMA32(x[i+4], y1[i+4], acc1[0])
		acc1[1] = opusmath.FMA32(x[i+5], y1[i+5], acc1[1])
		acc1[2] = opusmath.FMA32(x[i+6], y1[i+6], acc1[2])
		acc1[3] = opusmath.FMA32(x[i+7], y1[i+7], acc1[3])
		acc2[0] = opusmath.FMA32(x[i+4], y2[i+4], acc2[0])
		acc2[1] = opusmath.FMA32(x[i+5], y2[i+5], acc2[1])
		acc2[2] = opusmath.FMA32(x[i+6], y2[i+6], acc2[2])
		acc2[3] = opusmath.FMA32(x[i+7], y2[i+7], acc2[3])
	}
	if length-i >= 4 {
		acc1[0] = opusmath.FMA32(x[i], y1[i], acc1[0])
		acc1[1] = opusmath.FMA32(x[i+1], y1[i+1], acc1[1])
		acc1[2] = opusmath.FMA32(x[i+2], y1[i+2], acc1[2])
		acc1[3] = opusmath.FMA32(x[i+3], y1[i+3], acc1[3])
		acc2[0] = opusmath.FMA32(x[i], y2[i], acc2[0])
		acc2[1] = opusmath.FMA32(x[i+1], y2[i+1], acc2[1])
		acc2[2] = opusmath.FMA32(x[i+2], y2[i+2], acc2[2])
		acc2[3] = opusmath.FMA32(x[i+3], y2[i+3], acc2[3])
		i += 4
	}
	xy10 := math.Float32frombits(math.Float32bits(acc1[0] + acc1[2]))
	xy11 := math.Float32frombits(math.Float32bits(acc1[1] + acc1[3]))
	xy20 := math.Float32frombits(math.Float32bits(acc2[0] + acc2[2]))
	xy21 := math.Float32frombits(math.Float32bits(acc2[1] + acc2[3]))
	sum1 := math.Float32frombits(math.Float32bits(xy10 + xy11))
	sum2 := math.Float32frombits(math.Float32bits(xy20 + xy21))
	for ; i < length; i++ {
		sum1 = opusmath.FMA32(x[i], y1[i], sum1)
		sum2 = opusmath.FMA32(x[i], y2[i], sum2)
	}
	return sum1, sum2
}

func TestPrefilterDualInnerProdMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x55aa55aa))
	lengths := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 15, 16, 17, 23, 24, 31, 32, 33, 47, 48, 63, 64, 65, 96, 120, 240, 320}
	for _, n := range lengths {
		for trial := range 64 {
			x := make([]float32, n)
			y1 := make([]float32, n)
			y2 := make([]float32, n)
			for i := range x {
				x[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(16)-8)))
				y1[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(16)-8)))
				y2[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(16)-8)))
			}
			w1, w2 := prefilterDualInnerProdF32Ref(x, y1, y2, n)
			g1, g2 := prefilterDualInnerProdAsm(x, y1, y2, n)
			if math.Float32bits(g1) != math.Float32bits(w1) || math.Float32bits(g2) != math.Float32bits(w2) {
				t.Fatalf("n=%d trial=%d: got (0x%08x,0x%08x) want (0x%08x,0x%08x)",
					n, trial, math.Float32bits(g1), math.Float32bits(g2),
					math.Float32bits(w1), math.Float32bits(w2))
			}
		}
	}
}

var dualInnerProdBenchSink float32

func benchmarkDualInnerProd(b *testing.B, n int, asm bool) {
	rng := rand.New(rand.NewSource(int64(n)))
	x := make([]float32, n)
	y1 := make([]float32, n)
	y2 := make([]float32, n)
	for i := range x {
		x[i] = rng.Float32()*2 - 1
		y1[i] = rng.Float32()*2 - 1
		y2[i] = rng.Float32()*2 - 1
	}
	b.ResetTimer()
	var s1, s2 float32
	if asm {
		for i := 0; i < b.N; i++ {
			a, c := prefilterDualInnerProdAsm(x, y1, y2, n)
			s1, s2 = a, c
		}
	} else {
		for i := 0; i < b.N; i++ {
			a, c := prefilterDualInnerProdF32Ref(x, y1, y2, n)
			s1, s2 = a, c
		}
	}
	dualInnerProdBenchSink = s1 + s2
}

func BenchmarkDualInnerProd_Asm_N240(b *testing.B) { benchmarkDualInnerProd(b, 240, true) }
func BenchmarkDualInnerProd_Ref_N240(b *testing.B) { benchmarkDualInnerProd(b, 240, false) }

const prefilterLibopusFixturePath = "testdata/prefilter_libopus_ref_fixture.json"

type prefilterFixtureFile struct {
	Version int                    `json:"version"`
	Cases   []prefilterFixtureCase `json:"cases"`
}

type prefilterFixtureCase struct {
	ID               int     `json:"id"`
	Channels         int     `json:"channels"`
	FrameSize        int     `json:"frame_size"`
	Complexity       int     `json:"complexity"`
	PrevPeriod       int     `json:"prev_period"`
	PrevGain         float64 `json:"prev_gain"`
	PrevTapset       int     `json:"prev_tapset"`
	Tapset           int     `json:"tapset"`
	Enabled          bool    `json:"enabled"`
	TFEstimate       float64 `json:"tf_estimate"`
	NBAvailableBytes int     `json:"nb_available_bytes"`
	ToneFreq         float64 `json:"tone_freq"`
	Toneishness      float64 `json:"toneishness"`
	MaxPitchRatio    float64 `json:"max_pitch_ratio"`
	SignalSeed       int64   `json:"signal_seed"`
	ExpectedOn       bool    `json:"expected_on"`
	ExpectedPitch    int     `json:"expected_pitch"`
	ExpectedQG       int     `json:"expected_qg"`
	ExpectedGain     float64 `json:"expected_gain"`
}

func buildPrefilterFixtureSignal(seed int64, channels, frameSize int) (prefilterMem, preemph, pre []float64) {
	rng := rand.New(rand.NewSource(seed))
	prefilterMem = make([]float64, combFilterMaxPeriod*channels)
	for i := range prefilterMem {
		prefilterMem[i] = (rng.Float64()*2 - 1) * CELTSigScale
	}

	preemph = make([]float64, frameSize*channels)
	for i := range preemph {
		preemph[i] = (rng.Float64()*2 - 1) * CELTSigScale
	}

	pre = make([]float64, channels*(combFilterMaxPeriod+frameSize))
	perChanLen := combFilterMaxPeriod + frameSize
	for ch := range channels {
		copy(pre[ch*perChanLen:ch*perChanLen+combFilterMaxPeriod],
			prefilterMem[ch*combFilterMaxPeriod:(ch+1)*combFilterMaxPeriod])
		for i := range frameSize {
			pre[ch*perChanLen+combFilterMaxPeriod+i] = preemph[i*channels+ch]
		}
	}
	return prefilterMem, preemph, pre
}

func loadPrefilterLibopusFixture() (prefilterFixtureFile, error) {
	data, err := os.ReadFile(prefilterLibopusFixturePath)
	if err != nil {
		return prefilterFixtureFile{}, err
	}
	var fixture prefilterFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		return prefilterFixtureFile{}, err
	}
	return fixture, nil
}

func TestRunPrefilterParityAgainstLibopusFixture(t *testing.T) {
	fixture, err := loadPrefilterLibopusFixture()
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported fixture version %d", fixture.Version)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("empty fixture")
	}

	var (
		onMismatch    int
		pitchMismatch int
		qgMismatch    int
		gainMismatch  int
		maxPitchDiff  int
		maxGainDiff   float64
	)

	for _, c := range fixture.Cases {
		enc := NewEncoder(c.Channels)
		enc.prefilterPeriod = c.PrevPeriod
		enc.prefilterGain = float32(c.PrevGain)
		enc.prefilterTapset = c.PrevTapset
		enc.complexity = int32(c.Complexity)

		prefilterMem, preemph, _ := buildPrefilterFixtureSignal(c.SignalSeed, c.Channels, c.FrameSize)
		copyFloat64ToSig(enc.prefilterMem, prefilterMem)
		// runPrefilter takes celt_encode_with_ec's planar in buffer: per
		// channel, the overlap head followed by the frame.
		overlap := min(Overlap, c.FrameSize)
		stride := c.FrameSize + overlap
		goInput := make([]float32, c.Channels*stride)
		for i, v := range preemph {
			goInput[(i%c.Channels)*stride+overlap+i/c.Channels] = float32(v)
		}

		got := enc.runPrefilter(goInput, c.FrameSize, c.Tapset, c.Enabled, float32(c.TFEstimate), c.NBAvailableBytes, float32(c.ToneFreq), float32(c.Toneishness), float32(c.MaxPitchRatio))

		if got.on != c.ExpectedOn {
			onMismatch++
		}
		pd := int(math.Abs(float64(got.pitch - c.ExpectedPitch)))
		if pd > 1 {
			pitchMismatch++
			if pd > maxPitchDiff {
				maxPitchDiff = pd
			}
		}
		if got.qg != c.ExpectedQG {
			qgMismatch++
		}
		gd := math.Abs(float64(got.gain) - c.ExpectedGain)
		if gd > 1e-5 {
			gainMismatch++
			if gd > maxGainDiff {
				maxGainDiff = gd
			}
		}
	}

	t.Logf("cases=%d onMismatch=%d pitchMismatch=%d qgMismatch=%d gainMismatch=%d maxPitchDiff=%d maxGainDiff=%.6f",
		len(fixture.Cases), onMismatch, pitchMismatch, qgMismatch, gainMismatch, maxPitchDiff, maxGainDiff)

	if onMismatch != 0 || pitchMismatch != 0 || qgMismatch != 0 || gainMismatch != 0 {
		t.Fatalf("runPrefilter libopus fixture mismatch: on=%d pitch=%d qg=%d gain=%d maxPitchDiff=%d maxGainDiff=%.6f",
			onMismatch, pitchMismatch, qgMismatch, gainMismatch, maxPitchDiff, maxGainDiff)
	}
}

func pitchDownsample(x []float64, xLP []float32, length, channels, factor int) {
	if length <= 0 || factor <= 0 || len(xLP) < length {
		return
	}
	const (
		firQuarter = float32(0.25)
		firHalf    = float32(0.5)
	)
	handled := false
	if factor == 2 {
		switch channels {
		case 1:
			idx := 2
			for i := 1; i < length; i++ {
				v := firQuarter*float32(x[idx-1]) + firQuarter*float32(x[idx+1]) + firHalf*float32(x[idx])
				xLP[i] = v
				idx += 2
			}
			xLP[0] = firQuarter*float32(x[1]) + firHalf*float32(x[0])
		case 2:
			chStride := len(x) / 2
			x0 := x[:chStride]
			x1 := x[chStride:]
			idx := 2
			for i := 1; i < length; i++ {
				v0 := firQuarter*float32(x0[idx-1]) + firQuarter*float32(x0[idx+1]) + firHalf*float32(x0[idx])
				v1 := firQuarter*float32(x1[idx-1]) + firQuarter*float32(x1[idx+1]) + firHalf*float32(x1[idx])
				xLP[i] = v0
				xLP[i] += v1
				idx += 2
			}
			v0 := firQuarter*float32(x0[1]) + firHalf*float32(x0[0])
			v1 := firQuarter*float32(x1[1]) + firHalf*float32(x1[0])
			xLP[0] = v0
			xLP[0] += v1
		}
		handled = true
	}
	if !handled {
		offset := max(factor/2, 1)
		for i := 1; i < length; i++ {
			idx := factor * i
			v := firQuarter*float32(x[idx-offset]) +
				firQuarter*float32(x[idx+offset]) +
				firHalf*float32(x[idx])
			xLP[i] = v
		}
		xLP[0] = firQuarter*float32(x[offset]) + firHalf*float32(x[0])
		if channels == 2 {
			chStride := len(x) / 2
			x1 := x[chStride:]
			for i := 1; i < length; i++ {
				idx := factor * i
				v := firQuarter*float32(x1[idx-offset]) +
					firQuarter*float32(x1[idx+offset]) +
					firHalf*float32(x1[idx])
				xLP[i] += v
			}
			v := firQuarter*float32(x1[offset]) + firHalf*float32(x1[0])
			xLP[0] += v
		}
	}

	var ac [5]float32
	pitchAutocorr5F32(xLP[:length], length, &ac)
	applyCELTPitchLagWindow32(ac[:], 4)

	lpc := lpcFromAutocorr32(ac)
	tmp := float32(1.0)
	for i := range 4 {
		tmp *= float32(0.9)
		lpc[i] *= tmp
	}
	c1 := float32(0.8)
	lpc2 := [5]float32{
		lpc[0] + float32(0.8),
		lpc[1] + c1*lpc[0],
		lpc[2] + c1*lpc[1],
		lpc[3] + c1*lpc[2],
		c1 * lpc[3],
	}
	celtFIR5F32(xLP, lpc2)
}

func findBestPitch(xcorr []float64, y []float64, length, maxPitch int, bestPitch *[2]int) {
	xcorr32 := make([]float32, maxPitch)
	y32 := make([]float32, length+maxPitch)
	for i := range xcorr32 {
		xcorr32[i] = float32(xcorr[i])
	}
	for i := range y32 {
		y32[i] = float32(y[i])
	}
	findBestPitchF32(xcorr32, y32, length, maxPitch, bestPitch)
}

func findBestPitchInRanges(xcorr []float64, y []float64, length int, ranges [2]pitchSearchRange, bestPitch *[2]int) {
	maxPitch := len(xcorr)
	xcorr32 := make([]float32, maxPitch)
	y32 := make([]float32, len(y))
	for i := range xcorr32 {
		xcorr32[i] = float32(xcorr[i])
	}
	for i := range y32 {
		y32[i] = float32(y[i])
	}
	findBestPitchInRangesF32(xcorr32, y32, length, ranges, bestPitch)
}

func goPrefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	for i := range maxPitch {
		sum := float32(0)
		for j := range length {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}

// f32Close checks if two float64 values (representing widened float32 results)
// are close enough. SIMD accumulation reorders float32 additions, producing
// differences bounded by n*eps(float32) where n is the vector length.
func f32Close(got, want float64) bool {
	diff := math.Abs(got - want)
	if diff <= 1e-5 {
		return true
	}
	denom := math.Abs(want)
	if denom < 1e-10 {
		return diff < 1e-5
	}
	return diff/denom < 1e-5
}

func TestPrefilterPitchXcorr(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for trial := range 200 {
		length := rng.Intn(300) + 1
		maxPitch := rng.Intn(100) + 1
		x := make([]float64, length)
		y := make([]float64, length+maxPitch)
		for i := range x {
			x[i] = rng.Float64()*2 - 1
		}
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}
		got := make([]float64, maxPitch)
		want := make([]float64, maxPitch)
		prefilterPitchXcorr(x, y, got, length, maxPitch)
		goPrefilterPitchXcorr(x, y, want, length, maxPitch)
		for i := range maxPitch {
			if !f32Close(got[i], want[i]) {
				t.Fatalf("trial %d (length=%d, maxPitch=%d): xcorr[%d] got %v, want %v (diff=%e)",
					trial, length, maxPitch, i, got[i], want[i], math.Abs(got[i]-want[i]))
			}
		}
	}
}

func TestPrefilterPitchXcorrEdge(t *testing.T) {
	// maxPitch < 4 (no 4-way outer loop)
	x := []float64{1, 1, 1, 1, 1}
	y := []float64{1, 2, 3, 4, 5, 6, 7}
	got := make([]float64, 3)
	want := make([]float64, 3)
	prefilterPitchXcorr(x, y, got, 5, 3)
	goPrefilterPitchXcorr(x, y, want, 5, 3)
	for i := range 3 {
		if got[i] != want[i] {
			t.Fatalf("xcorr[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func BenchmarkPrefilterPitchXcorr(b *testing.B) {
	rng := rand.New(rand.NewSource(99))
	length := 240
	maxPitch := 256
	x := make([]float64, length)
	y := make([]float64, length+maxPitch)
	xcorr := make([]float64, maxPitch)
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	for i := range y {
		y[i] = rng.Float64()*2 - 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefilterPitchXcorr(x, y, xcorr, length, maxPitch)
	}
}
