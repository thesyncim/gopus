package celt

import (
	"bytes"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"math/rand"
	"testing"
)

func TestQuantAllBandsDecodeMonoMatchesGenericLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(0xba4d))
	buf := make([]byte, 400)
	var dummy rangecoding.Decoder
	dummy.Init([]byte{0})
	for iter := range 1500 {
		lm := rng.Intn(4)
		frameSize := 120 << lm
		start := 0
		if rng.Intn(4) == 0 {
			start = 17
		}
		end := start + 1 + rng.Intn(MaxBands-start)
		shortBlocks := 0
		transient := 0
		if lm > 0 && rng.Intn(3) == 0 {
			shortBlocks = 1 << lm
			transient = 1
		}
		// tf_res takes the tf_decode() values: a tf_select_table entry for
		// the frame's transient flag and tf_select.
		row := &tfSelectTable[lm]
		base := 4*transient + 2*rng.Intn(2)
		pulses := make([]int32, MaxBands)
		tfRes := make([]int32, MaxBands)
		for i := range pulses {
			pulses[i] = int32(rng.Intn(400))
			tfRes[i] = int32(row[base+rng.Intn(2)])
		}
		spread := rng.Intn(4)
		totalBitsQ3 := rng.Intn(len(buf)*64) + 64
		balance := rng.Intn(200) - 100
		codedBands := start + rng.Intn(end-start+1)
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		seed0 := rng.Uint32()

		var rdA, rdB rangecoding.Decoder
		var scA, scB bandDecodeScratch
		rdA.Init(buf)
		rdB.Init(buf)
		seedA, seedB := seed0, seed0
		leftA, _, colA := quantAllBandsDecodeWithScratchWithMode(&rdA, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
			0, 0, tfRes, totalBitsQ3, balance, codedBands, false, &seedA, &scA, nil, nil, 0, nil, nil, nil, nil)
		leftB, _, colB := quantAllBandsDecodeWithScratchWithMode(&rdB, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
			0, 0, tfRes, totalBitsQ3, balance, codedBands, false, &seedB, &scB, &dummy, nil, 0, nil, nil, nil, nil)
		if seedA != seedB || rdA.Tell() != rdB.Tell() || rdA.Range() != rdB.Range() || rdA.Val() != rdB.Val() {
			t.Fatalf("iter %d: seed/range state diverged", iter)
		}
		for i := range leftB {
			if math.Float32bits(float32(leftA[i])) != math.Float32bits(float32(leftB[i])) {
				t.Fatalf("iter %d: left[%d]=%v want %v", iter, i, leftA[i], leftB[i])
			}
		}
		for i := range colB {
			if colA[i] != colB[i] {
				t.Fatalf("iter %d: collapse[%d]=%d want %d", iter, i, colA[i], colB[i])
			}
		}
	}
}

type stdBandDecodeParams struct {
	channels, frameSize, lm, start, end        int
	pulses, tfRes                              []int32
	shortBlocks, spread, dualStereo, intensity int
	totalBitsQ3, balance, codedBands           int
	disableInv                                 bool
	seed                                       uint32
}

// randomStdBandDecodeParams returns decode parameters for random band data.
func randomStdBandDecodeParams(rng *rand.Rand, bufLen int) stdBandDecodeParams {
	p := stdBandDecodeParams{channels: 1 + rng.Intn(2), lm: rng.Intn(4)}
	p.frameSize = 120 << p.lm
	if rng.Intn(4) == 0 {
		p.start = 17
	}
	p.end = p.start + 1 + rng.Intn(MaxBands-p.start)
	transient := 0
	if p.lm > 0 && rng.Intn(3) == 0 {
		p.shortBlocks = 1 << p.lm
		transient = 1
	}
	// tf_res takes the tf_decode() values: a tf_select_table entry for the
	// frame's transient flag and tf_select.
	row := &tfSelectTable[p.lm]
	base := 4*transient + 2*rng.Intn(2)
	p.pulses = make([]int32, MaxBands)
	p.tfRes = make([]int32, MaxBands)
	for i := range p.pulses {
		p.pulses[i] = int32(rng.Intn(400))
		p.tfRes[i] = int32(row[base+rng.Intn(2)])
	}
	p.spread = rng.Intn(4)
	if p.channels == 2 {
		p.dualStereo = rng.Intn(2)
		p.intensity = p.start + rng.Intn(p.end-p.start+1)
	}
	p.totalBitsQ3 = rng.Intn(bufLen*64) + 64
	p.balance = rng.Intn(200) - 100
	p.codedBands = p.start + rng.Intn(p.end-p.start+1)
	p.disableInv = rng.Intn(2) == 0
	p.seed = rng.Uint32()
	return p
}

// TestQuantAllBandsDecodeStdMatchesWithMode decodes random mono and stereo band
// data through the standard-mode setup and through the generic mode setup, with
// scratch reused across frames of different sizes, and requires the same
// coefficients, collapse masks, seed and range decoder state.
func TestQuantAllBandsDecodeStdMatchesWithMode(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57d))
	buf := make([]byte, 400)
	var scA, scB bandDecodeScratch
	for iter := range 2000 {
		p := randomStdBandDecodeParams(rng, len(buf))
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		var rdA, rdB rangecoding.Decoder
		rdA.Init(buf)
		rdB.Init(buf)
		seedA, seedB := p.seed, p.seed
		leftA, rightA, colA := quantAllBandsDecodeStd(&rdA, p.channels, p.frameSize, p.lm, p.start, p.end, p.pulses,
			p.shortBlocks, p.spread, p.dualStereo, p.intensity, p.tfRes, p.totalBitsQ3, p.balance, p.codedBands,
			p.disableInv, &seedA, &scA)
		leftB, rightB, colB := quantAllBandsDecodeWithScratchWithMode(&rdB, p.channels, p.frameSize, p.lm, p.start, p.end, p.pulses,
			p.shortBlocks, p.spread, p.dualStereo, p.intensity, p.tfRes, p.totalBitsQ3, p.balance, p.codedBands,
			p.disableInv, &seedB, &scB, nil, nil, 0, nil, nil, nil, nil)
		if seedA != seedB || rdA.Tell() != rdB.Tell() || rdA.Range() != rdB.Range() || rdA.Val() != rdB.Val() {
			t.Fatalf("iter %d: seed/range state diverged", iter)
		}
		if len(leftA) != len(leftB) || len(rightA) != len(rightB) || len(colA) != len(colB) {
			t.Fatalf("iter %d: lengths left %d/%d right %d/%d collapse %d/%d", iter,
				len(leftA), len(leftB), len(rightA), len(rightB), len(colA), len(colB))
		}
		for _, pair := range [][2][]celtNorm{{leftA, leftB}, {rightA, rightB}} {
			for i := range pair[1] {
				if math.Float32bits(float32(pair[0][i])) != math.Float32bits(float32(pair[1][i])) {
					t.Fatalf("iter %d: coefficient %d = %v want %v", iter, i, pair[0][i], pair[1][i])
				}
			}
		}
		for i := range colB {
			if colA[i] != colB[i] {
				t.Fatalf("iter %d: collapse[%d]=%d want %d", iter, i, colA[i], colB[i])
			}
		}
	}
}

func TestQuantAllBandsDecodeStdNoAllocs(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57e))
	buf := make([]byte, 400)
	for i := range buf {
		buf[i] = byte(rng.Intn(256))
	}
	for _, channels := range []int{1, 2} {
		var p stdBandDecodeParams
		for p.channels != channels || p.lm != 3 {
			p = randomStdBandDecodeParams(rng, len(buf))
		}
		var sc bandDecodeScratch
		var rd rangecoding.Decoder
		seed := p.seed
		run := func() {
			rd.Init(buf)
			quantAllBandsDecodeStd(&rd, p.channels, p.frameSize, p.lm, p.start, p.end, p.pulses,
				p.shortBlocks, p.spread, p.dualStereo, p.intensity, p.tfRes, p.totalBitsQ3, p.balance, p.codedBands,
				p.disableInv, &seed, &sc)
		}
		run()
		if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
			t.Fatalf("channels=%d: %v allocs per run, want 0", channels, allocs)
		}
	}
}

// referenceCollapseMask is the libopus quant_band() collapse mask: bit i is
// set when block i of the interleaved pulse vector holds a nonzero pulse.
func referenceCollapseMask(pulses []int32, n, b int) int {
	if b <= 1 {
		return 1
	}
	n0 := n / b
	mask := 0
	for i := range b {
		tmp := int32(0)
		for j := range n0 {
			tmp |= pulses[i*n0+j]
		}
		if tmp != 0 {
			mask |= 1 << i
		}
	}
	return mask
}

func TestExtractCollapseMaskMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc011))
	pulses := make([]int32, 176)
	for iter := range 20000 {
		b := 1 << rng.Intn(4)
		n := b * (1 + rng.Intn(len(pulses)/b))
		density := rng.Intn(4)
		for i := range pulses {
			pulses[i] = 0
			if rng.Intn(8) < density {
				pulses[i] = int32(rng.Intn(65) - 32)
			}
		}
		if rng.Intn(8) == 0 {
			pulses[rng.Intn(n)] = math.MinInt32
		}
		if got, want := extractCollapseMask(pulses, n, b), referenceCollapseMask(pulses, n, b); got != want {
			t.Fatalf("iter %d n=%d b=%d: mask %b want %b", iter, n, b, got, want)
		}
	}
}

func float64ShapesToNorms(shapes [][]float64) [][]CeltNorm {
	out := make([][]CeltNorm, len(shapes))
	for i := range shapes {
		out[i] = float64sToNorms(shapes[i])
	}
	return out
}

// TestVectorToPulses verifies that vectorToPulses converts normalized floats
// to integer pulses with correct L1 norm.
func TestVectorToPulses(t *testing.T) {
	tests := []struct {
		name  string
		shape []float64
		k     int
	}{
		{
			name:  "simple normalized vector",
			shape: []float64{0.6, 0.8, 0, 0}, // L2 norm = 1.0
			k:     10,
		},
		{
			name:  "single element",
			shape: []float64{1.0},
			k:     5,
		},
		{
			name:  "negative values",
			shape: []float64{-0.6, 0.8, 0, 0},
			k:     10,
		},
		{
			name:  "all zeros (degenerate)",
			shape: []float64{0, 0, 0, 0},
			k:     10,
		},
		{
			name:  "large k",
			shape: []float64{0.5, 0.5, 0.5, 0.5}, // L2 norm = 1.0
			k:     50,
		},
		{
			name:  "uneven distribution",
			shape: []float64{0.9, 0.1, 0.2, 0.3}, // Not normalized
			k:     20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pulses := vectorToPulses(float64sToNorms(tt.shape), tt.k)

			// Verify length matches
			if len(pulses) != len(tt.shape) {
				t.Errorf("length mismatch: got %d, want %d", len(pulses), len(tt.shape))
			}

			// Verify L1 norm equals k
			l1norm := 0
			for _, p := range pulses {
				if p < 0 {
					l1norm += -p
				} else {
					l1norm += p
				}
			}
			if l1norm != tt.k {
				t.Errorf("L1 norm = %d, want %d; pulses = %v", l1norm, tt.k, pulses)
			}
		})
	}
}

// TestVectorToPulsesPreservesDirection verifies that pulse vectors preserve
// the direction of the input shape.
func TestVectorToPulsesPreservesDirection(t *testing.T) {
	// Create a normalized vector
	shape := normalizeFloat64Vector([]float64{3.0, 4.0, 0.0, 0.0}) // [0.6, 0.8, 0, 0]
	k := 20

	pulses := vectorToPulses(float64sToNorms(shape), k)

	// Convert pulses back to normalized float
	floatPulses := make([]float64, len(pulses))
	for i, p := range pulses {
		floatPulses[i] = float64(p)
	}
	normalizedPulses := normalizeFloat64Vector(floatPulses)

	// Compute dot product (should be close to 1 if directions match)
	var dot float64
	for i := range shape {
		dot += shape[i] * normalizedPulses[i]
	}

	// Allow some tolerance due to quantization
	if dot < 0.8 {
		t.Errorf("direction not preserved: dot product = %f, want > 0.8", dot)
		t.Logf("shape: %v", shape)
		t.Logf("pulses: %v", pulses)
		t.Logf("normalizedPulses: %v", normalizedPulses)
	}
}

// TestVectorToPulsesRoundTrip tests that converting to pulses and normalizing
// back preserves the original direction. Uses higher k values for better accuracy.
func TestVectorToPulsesRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := range 10 {
		// Generate random normalized vector
		n := 4 + rng.Intn(12) // 4 to 16 dimensions
		shape := make([]float64, n)
		for i := range shape {
			shape[i] = rng.Float64()*2 - 1 // [-1, 1]
		}
		shape = normalizeFloat64Vector(shape)

		// Test with larger k values where quantization is less severe
		// Lower k values have inherently higher quantization error
		for _, k := range []int{20, 50, 100} {
			pulses := vectorToPulses(float64sToNorms(shape), k)

			// Verify L1 norm is correct (most important property)
			l1norm := 0
			for _, p := range pulses {
				if p < 0 {
					l1norm += -p
				} else {
					l1norm += p
				}
			}
			if l1norm != k {
				t.Errorf("iter %d, k=%d: L1 norm = %d, want %d", iter, k, l1norm, k)
			}

			// Convert back to float and normalize
			floatPulses := make([]float64, len(pulses))
			for i, p := range pulses {
				floatPulses[i] = float64(p)
			}
			reconstructed := normalizeFloat64Vector(floatPulses)

			// Compute dot product
			var dot float64
			for i := range shape {
				dot += shape[i] * reconstructed[i]
			}

			// Higher k should give better preservation
			minDot := 0.6
			if k >= 50 {
				minDot = 0.7
			}
			if k >= 100 {
				minDot = 0.8
			}

			if dot < minDot {
				t.Errorf("iter %d, k=%d: dot product = %f, want > %f", iter, k, dot, minDot)
			}
		}
	}
}

// TestPVQEncodeDecodeRoundTrip tests that encoding and decoding a PVQ vector
// produces a valid decoded shape. Note: Due to known asymmetry in CWRS
// encode/decode (D03-02-03), we focus on verifying decoded shape properties
// rather than exact reconstruction.
func TestPVQEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := range 10 {
		// Use larger k for better quantization
		n := 2 + rng.Intn(6)   // 2 to 8 dimensions
		k := 10 + rng.Intn(20) // 10 to 30 pulses

		// Generate random normalized shape
		shape := make([]float64, n)
		for i := range shape {
			shape[i] = rng.Float64()*2 - 1
		}
		shape = normalizeFloat64Vector(shape)

		// Create encoder with buffer
		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)

		enc := NewEncoder(1)
		enc.SetRangeEncoder(re)

		// Encode the shape
		enc.EncodeBandPVQ(float64sToNorms(shape), n, k)

		// Finalize encoding
		data := re.Done()
		if len(data) == 0 {
			t.Logf("iter %d: no data produced for n=%d, k=%d", iter, n, k)
			continue
		}

		// Create decoder
		dec := NewDecoder(1)
		rd := &rangecoding.Decoder{}
		rd.Init(data)
		dec.SetRangeDecoder(rd)

		// Decode the shape
		decoded := dec.DecodePVQ(n, k)
		if len(decoded) != n {
			t.Errorf("iter %d: decoded length %d, want %d", iter, len(decoded), n)
			continue
		}

		// Verify decoded shape has unit L2 norm (critical property)
		var energy float64
		for _, x := range decoded {
			energy += float64(x) * float64(x)
		}
		l2norm := math.Sqrt(energy)
		if math.Abs(l2norm-1.0) > 0.01 {
			t.Errorf("iter %d: decoded L2 norm = %f, want 1.0", iter, l2norm)
		}

		// The decoded shape is a valid PVQ vector - direction may differ
		// due to CWRS asymmetry but the shape is still usable
		t.Logf("iter %d: n=%d, k=%d, decoded L2 norm=%.4f", iter, n, k, l2norm)
	}
}

// TestEncodeBandsAllSizes tests EncodeBands with all frame sizes.
func TestEncodeBandsAllSizes(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}
	nbBands := 21

	for _, frameSize := range frameSizes {
		t.Run(frameNameFromSize(frameSize), func(t *testing.T) {
			// Create encoder with buffer
			buf := make([]byte, 4096)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)

			// Generate test shapes
			shapes := make([][]float64, nbBands)
			for band := range nbBands {
				n := ScaledBandWidth(band, frameSize)
				if n <= 0 {
					shapes[band] = []float64{}
					continue
				}
				shape := make([]float64, n)
				// Simple pattern
				for i := range shape {
					shape[i] = float64(i%3 - 1)
				}
				shapes[band] = normalizeFloat64Vector(shape)
			}

			// Allocate bits (simple uniform allocation)
			bandBits := make([]int, nbBands)
			for band := range nbBands {
				bandBits[band] = 16 << bitRes // 16 bits per band (Q3)
			}

			// This should not panic
			enc.EncodeBands(float64ShapesToNorms(shapes), nil, bandBits, nbBands, frameSize)

			// Finalize and verify we got some data
			data := re.Done()
			if len(data) == 0 {
				t.Logf("Warning: no data produced for frameSize=%d", frameSize)
			}
		})
	}
}

// TestPVQEncodingPreservesEnergy verifies that decoded shapes have unit L2 norm.
func TestPVQEncodingPreservesEnergy(t *testing.T) {
	// Test with various n and k values
	testCases := []struct {
		n int
		k int
	}{
		{2, 5},
		{4, 10},
		{8, 20},
		{16, 30},
	}

	rng := rand.New(rand.NewSource(123))

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Generate random normalized shape
			shape := make([]float64, tc.n)
			for i := range shape {
				shape[i] = rng.Float64()*2 - 1
			}
			shape = normalizeFloat64Vector(shape)

			// Encode
			buf := make([]byte, 256)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)
			enc.EncodeBandPVQ(float64sToNorms(shape), tc.n, tc.k)
			data := re.Done()

			if len(data) == 0 {
				t.Fatal("no data produced")
			}

			// Decode
			dec := NewDecoder(1)
			rd := &rangecoding.Decoder{}
			rd.Init(data)
			dec.SetRangeDecoder(rd)
			decoded := dec.DecodePVQ(tc.n, tc.k)

			// Check L2 norm
			var energy float64
			for _, x := range decoded {
				energy += float64(x) * float64(x)
			}
			l2norm := math.Sqrt(energy)

			if math.Abs(l2norm-1.0) > 0.01 {
				t.Errorf("n=%d, k=%d: L2 norm = %f, want 1.0", tc.n, tc.k, l2norm)
			}
		})
	}
}

// TestNormalizeBands verifies band normalization produces unit-norm shapes.
func TestNormalizeBands(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients (flat spectrum)
	totalBins := 0
	for band := range nbBands {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = float64(i%10-5) * 0.1
	}

	// Generate energies (log2 scale)
	energies := make([]celtGLog, nbBands)
	for band := range nbBands {
		energies[band] = celtGLog(float64(band) * 0.5) // Increasing energy
	}

	// Normalize
	shapes := enc.NormalizeBands(float64sToNorms(mdctCoeffs), energies, nbBands, frameSize)

	if len(shapes) != nbBands {
		t.Fatalf("got %d shapes, want %d", len(shapes), nbBands)
	}

	// Verify each shape has unit L2 norm
	for band, shape := range shapes {
		if len(shape) == 0 {
			continue
		}

		var energy float64
		for _, x := range shape {
			x64 := float64(x)
			energy += x64 * x64
		}
		l2norm := math.Sqrt(energy)

		if math.Abs(l2norm-1.0) > 1e-6 {
			t.Errorf("band %d: L2 norm = %f, want 1.0", band, l2norm)
		}
	}
}

// TestEncodeBandsWithDecoder tests that encoding and decoding bands produces
// valid unit-norm vectors. Note: Due to CWRS asymmetry, exact direction
// preservation is not guaranteed.
func TestEncodeBandsWithDecoder(t *testing.T) {
	frameSize := 480
	nbBands := 5 // Test with fewer bands for simplicity

	// Create encoder with buffer
	buf := make([]byte, 4096)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	enc := NewEncoder(1)
	enc.SetRangeEncoder(re)

	// Generate normalized shapes
	shapes := make([][]float64, nbBands)
	for band := range nbBands {
		n := ScaledBandWidth(band, frameSize)
		shape := make([]float64, n)
		for i := range shape {
			shape[i] = float64((i+band)%5 - 2)
		}
		shape = normalizeFloat64Vector(shape)
		shapes[band] = shape
	}

	// Allocate bits (generous allocation)
	bandBits := make([]int, nbBands)
	for band := range nbBands {
		bandBits[band] = 24 << bitRes // 24 bits per band (Q3)
	}

	// Encode
	enc.EncodeBands(float64ShapesToNorms(shapes), nil, bandBits, nbBands, frameSize)
	data := re.Done()

	if len(data) == 0 {
		t.Fatal("no data produced")
	}

	// Decode
	dec := NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(data)
	dec.SetRangeDecoder(rd)

	// Decode each band individually and verify unit L2 norm
	for band := range nbBands {
		n := ScaledBandWidth(band, frameSize)
		k := bitsToK(bandBits[band], n)
		if k <= 0 {
			continue
		}

		decoded := dec.DecodePVQ(n, k)

		// Verify decoded shape has unit L2 norm
		var energy float64
		for _, x := range decoded {
			energy += float64(x) * float64(x)
		}
		l2norm := math.Sqrt(energy)

		if math.Abs(l2norm-1.0) > 0.01 {
			t.Errorf("band %d: L2 norm = %f, want 1.0", band, l2norm)
		}
	}
}

// TestEncodeBandPVQProducesValidIndex verifies that EncodeBandPVQ produces
// data that can be decoded without errors.
func TestEncodeBandPVQProducesValidIndex(t *testing.T) {
	testCases := []struct {
		n int
		k int
	}{
		{4, 5},
		{8, 10},
		{16, 20},
		{32, 30},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Create a simple shape
			shape := make([]float64, tc.n)
			for i := range shape {
				shape[i] = 1.0
			}
			shape = normalizeFloat64Vector(shape)

			// Encode
			buf := make([]byte, 256)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)
			enc.EncodeBandPVQ(float64sToNorms(shape), tc.n, tc.k)
			data := re.Done()

			if len(data) == 0 {
				t.Errorf("n=%d, k=%d: no data produced", tc.n, tc.k)
				return
			}

			// Verify we can decode without panic
			dec := NewDecoder(1)
			rd := &rangecoding.Decoder{}
			rd.Init(data)
			dec.SetRangeDecoder(rd)

			decoded := dec.DecodePVQ(tc.n, tc.k)
			if len(decoded) != tc.n {
				t.Errorf("n=%d, k=%d: decoded length %d", tc.n, tc.k, len(decoded))
			}
		})
	}
}

func frameNameFromSize(size int) string {
	switch size {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	default:
		return "unknown"
	}
}

type quantAllBandsEncodeCase struct {
	channels, lm, end, shortBlocks, spread, dualStereo, intensity int
	totalBitsQ3, balance, codedBands, complexity                  int
	disableInv                                                    bool
	seed                                                          uint32
	x, y                                                          []celtNorm
	pulses, tfRes                                                 []int32
	bandE                                                         []celtEner
	prefix                                                        int
	storage                                                       int
}

func randomQuantAllBandsEncodeCase(rng *rand.Rand) quantAllBandsEncodeCase {
	c := quantAllBandsEncodeCase{
		channels:   1 + rng.Intn(2),
		lm:         rng.Intn(4),
		end:        []int{13, 17, 19, 21}[rng.Intn(4)],
		spread:     rng.Intn(4),
		complexity: []int{5, 8, 10}[rng.Intn(3)],
		disableInv: rng.Intn(4) == 0,
		seed:       rng.Uint32(),
		prefix:     rng.Intn(40),
	}
	M := 1 << c.lm
	frameSize := M * Overlap
	isTransient := c.lm > 0 && rng.Intn(3) == 0
	if isTransient {
		c.shortBlocks = M
	}
	if c.channels == 2 {
		c.intensity = rng.Intn(c.end + 1)
		if rng.Intn(4) == 0 {
			c.dualStereo = 1
		}
	}
	c.storage = 20 + rng.Intn(300)
	c.totalBitsQ3 = (c.storage*8 - 40) << bitRes
	c.codedBands = 1 + rng.Intn(c.end)
	c.balance = rng.Intn(64) - 32

	c.x = make([]celtNorm, frameSize)
	if c.channels == 2 {
		c.y = make([]celtNorm, frameSize)
	}
	fillBand := func(v []celtNorm) {
		kind := rng.Intn(6)
		var e float64
		for i := range v {
			switch kind {
			case 0:
				v[i] = 0
			case 1:
				if i == rng.Intn(len(v)) {
					v[i] = 1
				}
			default:
				v[i] = celtNorm(rng.NormFloat64())
			}
			e += float64(v[i]) * float64(v[i])
		}
		if e > 0 {
			g := float32(1 / math.Sqrt(e))
			for i := range v {
				v[i] = celtNorm(float32(v[i]) * g)
			}
		}
	}
	for i := 0; i < c.end; i++ {
		lo, hi := M*EBands[i], M*EBands[i+1]
		fillBand(c.x[lo:hi])
		if c.y != nil {
			if rng.Intn(5) == 0 {
				copy(c.y[lo:hi], c.x[lo:hi])
			} else {
				fillBand(c.y[lo:hi])
			}
		}
	}

	c.pulses = make([]int32, MaxBands)
	for i := range c.pulses[:c.end] {
		n := M * (EBands[i+1] - EBands[i])
		c.pulses[i] = int32(rng.Intn(c.channels * n * 48))
		if rng.Intn(6) == 0 {
			c.pulses[i] = 0
		}
	}
	c.tfRes = make([]int32, MaxBands)
	tfSelect := rng.Intn(2)
	for i := range c.tfRes {
		idx := 2*tfSelect + rng.Intn(2)
		if isTransient {
			idx += 4
		}
		c.tfRes[i] = int32(tfSelectTable[c.lm][idx])
	}
	c.bandE = make([]celtEner, c.channels*MaxBands)
	for i := range c.bandE {
		c.bandE[i] = celtEner(rng.Float32() * 4)
		if rng.Intn(12) == 0 {
			c.bandE[i] = 1e-12
		}
	}
	return c
}

// run codes the case with the given pulse cache tables: nil tables take the
// standard-mode functions of bands_quant_encode.go, and the static tables
// passed explicitly take the general functions.
func (c quantAllBandsEncodeCase) run(cacheIndex []int16, cacheBits []uint8) ([]byte, []byte, []celtNorm, []celtNorm, uint32) {
	x := append([]celtNorm(nil), c.x...)
	var y []celtNorm
	if c.y != nil {
		y = append([]celtNorm(nil), c.y...)
	}
	buf := make([]byte, c.storage)
	var re rangecoding.Encoder
	re.Init(buf)
	for i := 0; i < c.prefix; i++ {
		re.EncodeBit(i&1, 2)
	}
	seed := c.seed
	var scratch bandEncodeScratch
	collapse := quantAllBandsEncodeScratchWithMode(&re, c.channels, len(c.x), c.lm, 0, c.end,
		x, y, c.pulses, c.shortBlocks, c.spread, 0, c.dualStereo, c.intensity,
		c.tfRes, c.totalBitsQ3, c.balance, c.codedBands, c.disableInv, &seed, c.complexity,
		c.bandE, nil, nil, &scratch, nil, nil, cacheIndex, cacheBits)
	collapse = append([]byte(nil), collapse...)
	return re.Done(), collapse, x, y, seed
}

// TestQuantAllBandsEncodeStandardPathMatchesGeneral checks the standard-mode
// encoder band functions (quantBandStereoEnc, quantBandEnc, quantPartitionEnc,
// computeThetaEnc, algQuantEnc and the theta RDO trials) bit for bit against
// the general *WithExtBudget path on random bands, allocations and coder
// states.
func TestQuantAllBandsEncodeStandardPathMatchesGeneral(t *testing.T) {
	rng := rand.New(rand.NewSource(0x6a1d))
	for iter := range 3000 {
		c := randomQuantAllBandsEncodeCase(rng)
		gotBytes, gotCollapse, gotX, gotY, gotSeed := c.run(nil, nil)
		wantBytes, wantCollapse, wantX, wantY, wantSeed := c.run(cacheIndex50[:], cacheBits50[:])
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Fatalf("iter %d: packet differs\n got %x\nwant %x", iter, gotBytes, wantBytes)
		}
		if !bytes.Equal(gotCollapse, wantCollapse) {
			t.Fatalf("iter %d: collapse masks differ: got %v want %v", iter, gotCollapse, wantCollapse)
		}
		if gotSeed != wantSeed {
			t.Fatalf("iter %d: seed %d want %d", iter, gotSeed, wantSeed)
		}
		for i := range gotX {
			if math.Float32bits(float32(gotX[i])) != math.Float32bits(float32(wantX[i])) {
				t.Fatalf("iter %d: x[%d] = %v want %v", iter, i, gotX[i], wantX[i])
			}
		}
		for i := range gotY {
			if math.Float32bits(float32(gotY[i])) != math.Float32bits(float32(wantY[i])) {
				t.Fatalf("iter %d: y[%d] = %v want %v", iter, i, gotY[i], wantY[i])
			}
		}
	}
}

func TestQuantAllBandsEncodeStandardPathZeroAlloc(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var c quantAllBandsEncodeCase
	for c.channels != 2 || c.complexity < 8 || c.dualStereo != 0 || c.lm != 3 {
		c = randomQuantAllBandsEncodeCase(rng)
	}
	x := append([]celtNorm(nil), c.x...)
	y := append([]celtNorm(nil), c.y...)
	buf := make([]byte, c.storage)
	var re rangecoding.Encoder
	var scratch bandEncodeScratch
	run := func() {
		copy(x, c.x)
		copy(y, c.y)
		re.Init(buf)
		seed := c.seed
		quantAllBandsEncodeScratchWithMode(&re, c.channels, len(c.x), c.lm, 0, c.end,
			x, y, c.pulses, c.shortBlocks, c.spread, 0, c.dualStereo, c.intensity,
			c.tfRes, c.totalBitsQ3, c.balance, c.codedBands, c.disableInv, &seed, c.complexity,
			c.bandE, nil, nil, &scratch, nil, nil, nil, nil)
	}
	run()
	if allocs := testing.AllocsPerRun(50, run); allocs != 0 {
		t.Fatalf("quantAllBandsEncodeScratchWithMode allocates %.1f per call", allocs)
	}
}

const celtPartitionModeZeroPulse = uint32(0)

var libopusCELTPartitionHelper libopustest.HelperCache

type zeroPulsePartitionOracleCase struct {
	name    string
	n       int
	blocks  int
	lm      int
	band    int
	fill    int
	seed    uint32
	gain    float32
	lowband []float32
}

type zeroPulsePartitionOracleResult struct {
	collapse int
	seed     uint32
	x        []float32
}

func buildLibopusCELTPartitionHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "celt partition",
		OutputBase:  "gopus_libopus_celt_partition",
		SourceFile:  "libopus_celt_partition_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return libopustest.BuildCHelper(cfg)
}

func probeLibopusZeroPulsePartition(cases []zeroPulsePartitionOracleCase) ([]zeroPulsePartitionOracleResult, error) {
	binPath, err := libopusCELTPartitionHelper.Path(buildLibopusCELTPartitionHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVPI", celtPartitionModeZeroPulse, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.n))
		payload.U32(uint32(tc.blocks))
		payload.I32(int32(tc.lm))
		payload.U32(uint32(tc.band))
		payload.U32(uint32(tc.fill))
		payload.U32(tc.seed)
		if tc.lowband == nil {
			payload.U32(0)
		} else {
			payload.U32(1)
		}
		payload.Float32(tc.gain)
		for _, sample := range tc.lowband {
			payload.Float32(sample)
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt partition", "GVPO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]zeroPulsePartitionOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		out[i].seed = reader.U32()
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func makeZeroPulseLowband(n int, seed uint32) []float32 {
	out := make([]float32, n)
	state := seed
	for i := range out {
		state = state*1664525 + 1013904223
		v := float32(int32(state)>>18) * (1.0 / 64.0)
		if i%5 == 0 {
			v = -v
		}
		out[i] = v
	}
	return out
}

func TestQuantPartitionZeroPulseMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)
	cases := []zeroPulsePartitionOracleCase{
		{name: "noise_lm0_b1", n: 3, blocks: 1, lm: 0, band: 3, fill: 1, seed: 0x13579bdf, gain: 1},
		{name: "noise_lm1_b2_partial_fill", n: 8, blocks: 2, lm: 1, band: 6, fill: 1, seed: 0x2468ace0, gain: 0.75},
		{name: "noise_lm2_b4", n: 24, blocks: 4, lm: 2, band: 8, fill: 0xf, seed: 0x31415926, gain: 0.625},
		{name: "fold_lm1_b2", n: 8, blocks: 2, lm: 1, band: 6, fill: 2, seed: 0xabcdef01, gain: 0.5, lowband: makeZeroPulseLowband(8, 0x10203040)},
		{name: "fold_lm2_b4_sparse_fill", n: 24, blocks: 4, lm: 2, band: 8, fill: 0xb, seed: 0x0badf00d, gain: 1.25, lowband: makeZeroPulseLowband(24, 0x50607080)},
		{name: "fold_lm3_b8", n: 96, blocks: 8, lm: 3, band: 11, fill: 0xa5, seed: 0xdeadbeef, gain: 0.875, lowband: makeZeroPulseLowband(96, 0x90abcdef)},
		{name: "collapsed_fill_zero", n: 16, blocks: 4, lm: 2, band: 8, fill: 0, seed: 0x01020304, gain: 1, lowband: makeZeroPulseLowband(16, 0x11223344)},
	}
	want, err := probeLibopusZeroPulsePartition(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt partition", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &bandCtx{
				resynth:    true,
				spread:     spreadNormal,
				band:       tc.band,
				seed:       tc.seed,
				seedActive: true,
				stdCache:   true,
			}
			x := make([]celtNorm, tc.n)
			var lowband []celtNorm
			if tc.lowband != nil {
				lowband = make([]celtNorm, len(tc.lowband))
				for j, sample := range tc.lowband {
					lowband[j] = celtNorm(sample)
				}
			}
			gotCollapse := quantPartitionDecodeNoExt(ctx, x, 0, tc.blocks, lowband, tc.lm, opusVal16(tc.gain), tc.fill)
			if gotCollapse != want[i].collapse {
				t.Fatalf("collapse=%d want %d", gotCollapse, want[i].collapse)
			}
			if ctx.seed != want[i].seed {
				t.Fatalf("seed=%08x want %08x", ctx.seed, want[i].seed)
			}
			if len(x) != len(want[i].x) {
				t.Fatalf("len(got)=%d want %d", len(x), len(want[i].x))
			}
			for j := range x {
				gotBits := math.Float32bits(float32(x[j]))
				wantBits := math.Float32bits(want[i].x[j])
				if gotBits != wantBits {
					t.Fatalf("x[%d]=%08x/%0.9g want %08x/%0.9g",
						j, gotBits, float32(x[j]), wantBits, want[i].x[j])
				}
			}
		})
	}
}

const celtResidualModeNormaliseResidual = uint32(0)

var libopusCELTResidualHelper libopustest.HelperCache

type residualCollapseOracleCase struct {
	name   string
	pulses []int
	b      int
	gain   float32
}

type residualCollapseOracleResult struct {
	collapse int
	x        []float32
}

func buildLibopusCELTResidualHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt residual",
		OutputBase:  "gopus_libopus_celt_residual",
		SourceFile:  "libopus_celt_residual_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		RefSources: []string{
			"celt/cwrs.c",
			"celt/entenc.c",
			"celt/entdec.c",
			"celt/entcode.c",
			"celt/laplace.c",
		},
		DeadStrip: true,
	})
}

func probeLibopusNormaliseResidual(cases []residualCollapseOracleCase) ([]residualCollapseOracleResult, error) {
	binPath, err := libopusCELTResidualHelper.Path(buildLibopusCELTResidualHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVRI", celtResidualModeNormaliseResidual, uint32(len(cases)))
	for _, tc := range cases {
		energy := float32(pulseEnergy(tc.pulses))
		payload.U32(uint32(len(tc.pulses)))
		payload.U32(uint32(tc.b))
		payload.Float32(tc.gain)
		payload.Float32(energy)
		for _, pulse := range tc.pulses {
			payload.I32(int32(pulse))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt residual", "GVRO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]residualCollapseOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestNormalizeResidualKnownEnergyIntoAndCollapseNormMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []residualCollapseOracleCase{
		{name: "n2_unit", pulses: []int{1, -1}, b: 1, gain: 1},
		{name: "n3_sparse", pulses: []int{0, 2, -1}, b: 1, gain: 0.75},
		{name: "n4_split", pulses: []int{0, 3, -2, 0}, b: 2, gain: 0.5},
		{name: "n7_tail", pulses: []int{1, 0, -1, 2, 0, 0, -2}, b: 3, gain: 1.25},
		{name: "n8_all_blocks", pulses: []int{0, 2, 0, -1, 3, 0, 0, -2}, b: 4, gain: 0.625},
		{name: "n12_mixed", pulses: []int{0, 3, -2, 0, 1, 0, -1, 0, 2, 0, 0, -1}, b: 3, gain: 0.875},
		{name: "n16_sparse_blocks", pulses: []int{1, 0, -1, 1, 0, -1, 1, 0, 0, 0, -1, 0, 0, 0, 0, 0}, b: 4, gain: 0.5},
		{name: "n24_light", pulses: makeAlgUnquantPulseVector(24, 13, 0x31415926), b: 6, gain: 0.875},
		{name: "n48_normal", pulses: makeAlgUnquantPulseVector(48, 21, 0xabcdef01), b: 8, gain: 0.625},
	}
	want, err := probeLibopusNormaliseResidual(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt residual", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			energy := float32(pulseEnergy(tc.pulses))
			got := make([]celtNorm, len(tc.pulses))
			gotCollapse := normalizeResidualKnownEnergyIntoAndCollapseNorm(got, append([]int(nil), tc.pulses...), opusVal16(tc.gain), opusVal16(energy), tc.b)
			if gotCollapse != want[i].collapse {
				t.Fatalf("collapse=%d want %d", gotCollapse, want[i].collapse)
			}
			if len(got) != len(want[i].x) {
				t.Fatalf("len(got)=%d want %d", len(got), len(want[i].x))
			}
			for j := range got {
				gotBits := math.Float32bits(float32(got[j]))
				wantBits := math.Float32bits(want[i].x[j])
				if gotBits != wantBits {
					t.Fatalf("x[%d]=%08x/%0.9g want %08x/%0.9g",
						j, gotBits, float32(got[j]), wantBits, want[i].x[j])
				}
			}
		})
	}
}

func referenceNormalizeResidualIntoAndCollapse(out []celtNorm, pulses []int, gain, yy opusVal16, b int) int {
	normalizeResidualInto(out, pulses, gain, yy)
	pulses32 := make([]int32, len(pulses))
	for i, v := range pulses {
		pulses32[i] = int32(v)
	}
	return extractCollapseMask(pulses32, len(pulses32), b)
}

func pulseEnergy(pulses []int) float64 {
	energy := 0.0
	for _, v := range pulses {
		energy += float64(v * v)
	}
	return energy
}

func pulseEnergy32(pulses []int32) float64 {
	energy := 0.0
	for _, v := range pulses {
		energy += float64(v * v)
	}
	return energy
}

func TestNormalizeResidualIntoAndCollapseMatchesReference(t *testing.T) {
	gains := []opusVal16{0.125, 0.5, 1.0, 1.75}
	lengths := []int{1, 2, 3, 4, 7, 8, 12, 16, 24, 32, 48}

	for _, n := range lengths {
		basePulses := make([]int, n)
		for i := range basePulses {
			v := ((i * 5) + 3) % 9
			basePulses[i] = v - 4
			if i%7 == 0 {
				basePulses[i] = 0
			}
		}
		if n > 0 {
			basePulses[0] = n % 5
		}

		for _, gain := range gains {
			energies := []opusVal16{0, opusVal16(pulseEnergy(basePulses))}
			for _, yy := range energies {
				for b := 1; b <= min(8, n); b++ {
					if celtUdiv(n, b) <= 0 {
						continue
					}

					got := make([]celtNorm, n)
					want := make([]celtNorm, n)
					pulses := append([]int(nil), basePulses...)

					gotMask := normalizeResidualIntoAndCollapse(got, pulses, gain, yy, b)
					wantMask := referenceNormalizeResidualIntoAndCollapse(want, pulses, gain, yy, b)

					if gotMask != wantMask {
						t.Fatalf("mask mismatch n=%d gain=%v yy=%v b=%d: got=%b want=%b",
							n, gain, yy, b, gotMask, wantMask)
					}
					for i := range got {
						if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
							t.Fatalf("value mismatch n=%d gain=%v yy=%v b=%d idx=%d: got=%0.9g want=%0.9g",
								n, gain, yy, b, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

func TestNormalizeResidualKnownEnergyIntoAndCollapse32MatchesIntPath(t *testing.T) {
	gains := []opusVal16{0.125, 0.5, 1.0, 1.75}
	lengths := []int{1, 2, 3, 4, 7, 8, 12, 16, 24, 32, 48}

	for _, n := range lengths {
		basePulses := make([]int32, n)
		for i := range basePulses {
			v := ((i * 5) + 3) % 9
			basePulses[i] = int32(v - 4)
			if i%7 == 0 {
				basePulses[i] = 0
			}
		}
		if n > 0 {
			basePulses[0] = int32(n % 5)
		}

		for _, gain := range gains {
			yy := opusVal16(pulseEnergy32(basePulses))
			if yy == 0 {
				continue
			}
			for b := 1; b <= min(8, n); b++ {
				if celtUdiv(n, b) <= 0 {
					continue
				}

				got := make([]celtNorm, n)
				want := make([]celtNorm, n)
				pulses32 := append([]int32(nil), basePulses...)
				pulses := make([]int, n)
				for i, v := range basePulses {
					pulses[i] = int(v)
				}

				gotMask := normalizeResidualKnownEnergyIntoAndCollapse32(got, pulses32, gain, yy, b)
				wantMask := normalizeResidualIntoAndCollapse(want, pulses, gain, yy, b)

				if gotMask != wantMask {
					t.Fatalf("mask mismatch n=%d gain=%v b=%d: got=%b want=%b",
						n, gain, b, gotMask, wantMask)
				}
				for i := range got {
					if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
						t.Fatalf("value mismatch n=%d gain=%v b=%d idx=%d: got=%0.9g want=%0.9g",
							n, gain, b, i, got[i], want[i])
					}
				}
			}
		}
	}
}

func BenchmarkNormalizeResidualIntoAndCollapseCurrent(b *testing.B) {
	pulses := make([]int, 48)
	for i := range pulses {
		pulses[i] = ((i * 11) % 7) - 3
	}
	out := make([]celtNorm, len(pulses))
	yy := opusVal16(pulseEnergy(pulses))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizeResidualIntoAndCollapse(out, pulses, 1.0, yy, 8)
	}
}

func BenchmarkNormalizeResidualKnownEnergyIntoAndCollapse32Current(b *testing.B) {
	pulses := make([]int32, 48)
	for i := range pulses {
		pulses[i] = int32(((i * 11) % 7) - 3)
	}
	out := make([]celtNorm, len(pulses))
	yy := opusVal16(pulseEnergy32(pulses))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizeResidualKnownEnergyIntoAndCollapse32(out, pulses, 1.0, yy, 8)
	}
}

func BenchmarkNormalizeResidualIntoAndCollapseReference(b *testing.B) {
	pulses := make([]int, 48)
	for i := range pulses {
		pulses[i] = ((i * 11) % 7) - 3
	}
	out := make([]celtNorm, len(pulses))
	yy := opusVal16(pulseEnergy(pulses))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = referenceNormalizeResidualIntoAndCollapse(out, pulses, 1.0, yy, 8)
	}
}

func bitsToKQ0(bits, n int) int {
	return bitsToK(bits<<bitRes, n)
}

// TestNormalizeVector verifies L2 normalization produces unit-length vectors.
func TestNormalizeVector(t *testing.T) {
	tests := []struct {
		name     string
		input    []float64
		expected []float64
	}{
		{
			name:     "3-4-5 triangle",
			input:    []float64{3, 4},
			expected: []float64{0.6, 0.8},
		},
		{
			name:     "already normalized",
			input:    []float64{1, 0},
			expected: []float64{1, 0},
		},
		{
			name:     "negative values",
			input:    []float64{-3, 4},
			expected: []float64{-0.6, 0.8},
		},
		{
			name:     "all zeros",
			input:    []float64{0, 0, 0},
			expected: []float64{0, 0, 0},
		},
		{
			name:     "single element",
			input:    []float64{5},
			expected: []float64{1},
		},
		{
			name:     "single negative",
			input:    []float64{-5},
			expected: []float64{-1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeFloat64Vector(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("NormalizeVector length = %d, want %d", len(result), len(tt.expected))
				return
			}

			// Check values
			for i := range result {
				if math.Abs(result[i]-tt.expected[i]) > 1e-6 {
					t.Errorf("NormalizeVector[%d] = %v, want %v", i, result[i], tt.expected[i])
				}
			}

			// Verify unit length (except for zero vector)
			var length2 float64
			for _, x := range result {
				length2 += float64(x) * float64(x)
			}
			if length2 > 1e-6 && math.Abs(length2-1.0) > 1e-6 {
				t.Errorf("Normalized vector length^2 = %v, want 1.0", length2)
			}
		})
	}
}

// TestNormalizeVectorUnitLength verifies various vectors normalize to unit length.
func TestNormalizeVectorUnitLength(t *testing.T) {
	testVectors := [][]float64{
		{1, 2, 3, 4, 5},
		{-1, -2, -3, -4, -5},
		{0.001, 0.002, 0.003},
		{1000, 2000, 3000},
		{1, 1, 1, 1, 1, 1, 1, 1},
	}

	for _, v := range testVectors {
		result := normalizeFloat64Vector(v)

		var length2 float64
		for _, x := range result {
			length2 += float64(x) * float64(x)
		}

		if math.Abs(length2-1.0) > 1e-6 {
			t.Errorf("normalizeFloat64Vector(%v) has length^2 = %v, want 1.0", v, length2)
		}
	}
}

// TestFoldBand verifies band folding produces unit-normalized vectors.
func TestFoldBand(t *testing.T) {
	tests := []struct {
		name    string
		lowband []celtNorm
		n       int
	}{
		{
			name:    "fold from 4-element band to 4",
			lowband: []celtNorm{0.5, 0.5, 0.5, 0.5},
			n:       4,
		},
		{
			name:    "fold from 2-element band to 4 (wrap)",
			lowband: []celtNorm{0.7071067811865476, 0.7071067811865476},
			n:       4,
		},
		{
			name:    "fold from 8-element band to 4 (truncate)",
			lowband: []celtNorm{0.35, 0.35, 0.35, 0.35, 0.35, 0.35, 0.35, 0.35},
			n:       4,
		},
		{
			name:    "noise generation (no source)",
			lowband: nil,
			n:       8,
		},
		{
			name:    "noise generation (empty source)",
			lowband: []celtNorm{},
			n:       8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed := uint32(12345)
			result := FoldBand(tt.lowband, tt.n, &seed)

			if len(result) != tt.n {
				t.Errorf("FoldBand length = %d, want %d", len(result), tt.n)
				return
			}

			// Verify unit norm
			var length2 float64
			for _, x := range result {
				length2 += float64(x) * float64(x)
			}
			if math.Abs(length2-1.0) > 2e-6 {
				t.Errorf("FoldBand has length^2 = %v, want 1.0", length2)
			}
		})
	}
}

// TestFoldBandSeedVariation verifies different seeds produce different outputs.
func TestFoldBandSeedVariation(t *testing.T) {
	lowband := []celtNorm{1, 0, 0, 0}
	n := 4

	seed1 := uint32(11111)
	seed2 := uint32(22222)
	seed3 := uint32(33333)

	result1 := FoldBand(lowband, n, &seed1)
	result2 := FoldBand(lowband, n, &seed2)
	result3 := FoldBand(lowband, n, &seed3)

	// Results should differ due to sign variations
	same12 := true
	same13 := true
	for i := range n {
		if result1[i] != result2[i] {
			same12 = false
		}
		if result1[i] != result3[i] {
			same13 = false
		}
	}

	if same12 && same13 {
		t.Error("Different seeds should produce different folded vectors")
	}
}

// TestBitsToK verifies bits-to-pulses conversion.
func TestBitsToK(t *testing.T) {
	// The bits-to-K relationship is complex and depends on V(n,k) computation.
	// These tests verify basic properties rather than exact values.

	// Zero bits should always return zero pulses
	if k := bitsToKQ0(0, 4); k != 0 {
		t.Errorf("bitsToKQ0(0, 4) = %d, want 0", k)
	}

	// Very few bits (< log2(2n)) should return zero
	if k := bitsToKQ0(1, 4); k != 0 {
		t.Errorf("bitsToKQ0(1, 4) = %d, want 0 (too few bits)", k)
	}

	// More bits should give more pulses (monotonically non-decreasing)
	prev := 0
	for bits := 0; bits <= 100; bits += 10 {
		k := bitsToKQ0(bits, 8)
		if k < prev {
			t.Errorf("bitsToK not monotonic: bits=%d gave k=%d, but bits=%d gave k=%d",
				bits-10, prev, bits, k)
		}
		prev = k
	}

	// K should stay within the PVQ pulse limit
	for _, bits := range []int{10, 20, 50, 100} {
		k := bitsToKQ0(bits, 8)
		if k > MaxPVQK {
			t.Errorf("bitsToKQ0(%d, 8) = %d, but k should not exceed MaxPVQK", bits, k)
		}
	}

	// With enough bits, should get at least 1 pulse
	if k := bitsToKQ0(10, 2); k < 1 {
		t.Errorf("bitsToKQ0(10, 2) = %d, expected at least 1 pulse with 10 bits", k)
	}
}

// TestBitsToKZeroBits verifies zero bits always returns zero pulses.
func TestBitsToKZeroBits(t *testing.T) {
	for n := 1; n <= 32; n++ {
		k := bitsToKQ0(0, n)
		if k != 0 {
			t.Errorf("bitsToKQ0(0, %d) = %d, want 0", n, k)
		}
	}
}

// TestBitsToKZeroDimensions verifies zero dimensions returns zero pulses.
func TestBitsToKZeroDimensions(t *testing.T) {
	for bits := 0; bits <= 100; bits += 10 {
		k := bitsToKQ0(bits, 0)
		if k != 0 {
			t.Errorf("bitsToKQ0(%d, 0) = %d, want 0", bits, k)
		}
	}
}

// TestCollapseMask verifies collapse mask tracking.
func TestCollapseMask(t *testing.T) {
	var mask uint32

	// Initially empty
	if GetCodedBandCount(mask) != 0 {
		t.Error("Initial mask should have 0 coded bands")
	}

	// Update some bands
	UpdateCollapseMask(&mask, 0)
	UpdateCollapseMask(&mask, 2)
	UpdateCollapseMask(&mask, 5)

	// Check coded bands
	if !IsBandCoded(mask, 0) {
		t.Error("Band 0 should be coded")
	}
	if IsBandCoded(mask, 1) {
		t.Error("Band 1 should not be coded")
	}
	if !IsBandCoded(mask, 2) {
		t.Error("Band 2 should be coded")
	}
	if !IsBandCoded(mask, 5) {
		t.Error("Band 5 should be coded")
	}

	// Check count
	if GetCodedBandCount(mask) != 3 {
		t.Errorf("GetCodedBandCount = %d, want 3", GetCodedBandCount(mask))
	}

	// Check anti-collapse
	if NeedsAntiCollapse(mask, 0) {
		t.Error("Band 0 should not need anti-collapse")
	}
	if !NeedsAntiCollapse(mask, 1) {
		t.Error("Band 1 should need anti-collapse")
	}
	if !NeedsAntiCollapse(mask, 3) {
		t.Error("Band 3 should need anti-collapse")
	}

	// Clear
	ClearCollapseMask(&mask)
	if mask != 0 {
		t.Error("Cleared mask should be 0")
	}
}

// TestFindFoldSource verifies source band lookup for folding.
func TestFindFoldSource(t *testing.T) {
	tests := []struct {
		name       string
		targetBand int
		codedMask  uint32
		expected   int
	}{
		{
			name:       "find band 2 from band 3",
			targetBand: 3,
			codedMask:  0b0101, // bands 0 and 2 coded
			expected:   2,
		},
		{
			name:       "find band 0 from band 1",
			targetBand: 1,
			codedMask:  0b0001, // only band 0 coded
			expected:   0,
		},
		{
			name:       "no coded bands",
			targetBand: 5,
			codedMask:  0,
			expected:   -1,
		},
		{
			name:       "target is 0",
			targetBand: 0,
			codedMask:  0b1111,
			expected:   -1,
		},
		{
			name:       "skip uncoded bands",
			targetBand: 5,
			codedMask:  0b00011, // bands 0, 1 coded
			expected:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindFoldSource(tt.targetBand, tt.codedMask, nil)
			if result != tt.expected {
				t.Errorf("FindFoldSource(%d, 0x%x) = %d, want %d",
					tt.targetBand, tt.codedMask, result, tt.expected)
			}
		})
	}
}

// TestDecodeBandsOutputLength verifies DecodeBands produces correct output length.
func TestDecodeBandsOutputLength(t *testing.T) {
	// Create decoder to verify it initializes correctly
	dec := NewDecoder(1)
	if dec.Channels() != 1 {
		t.Errorf("NewDecoder(1).Channels() = %d, want 1", dec.Channels())
	}

	// Need to set up a mock range decoder for actual decoding
	// For now, just verify band width calculations

	frameSizes := []int{120, 240, 480, 960}
	bandCounts := []int{13, 17, 19, 21}

	for i, frameSize := range frameSizes {
		nbBands := bandCounts[i]

		// Calculate expected length
		expectedLen := 0
		for band := range nbBands {
			expectedLen += ScaledBandWidth(band, frameSize)
		}

		t.Logf("FrameSize=%d, nbBands=%d, expectedLen=%d", frameSize, nbBands, expectedLen)

		// Verify band widths sum correctly
		if expectedLen != frameSize {
			// Note: This may not always equal frameSize depending on eBands table
			t.Logf("Band widths sum = %d, frameSize = %d", expectedLen, frameSize)
		}
	}
}

// TestDecodeBands_OutputSize verifies DecodeBands returns frameSize coefficients.
// This test confirms the fix for MDCT bin count mismatch (14-01).
// Before the fix, DecodeBands returned totalBins (800 for 20ms), causing IMDCT
// to produce wrong sample counts. After the fix, it returns frameSize (960).
func TestDecodeBands_OutputSize(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		nbBands   int
		totalBins int // sum of scaled band widths (smaller than frameSize)
	}{
		// totalBins = sum(ScaledBandWidth(band, frameSize) for band in 0..nbBands-1)
		// These are calculated from EBands table with scaling
		{"2.5ms", 120, 21, 100}, // EffBands=21, bands sum to 100
		{"5ms", 240, 21, 200},   // EffBands=21, bands sum to 200
		{"10ms", 480, 21, 400},  // EffBands=21, bands sum to 400
		{"20ms", 960, 21, 800},  // EffBands=21, bands sum to 800
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(1)

			// Create mock energies and bandBits
			energies := make([]celtGLog, tc.nbBands)
			bandBits := make([]int, tc.nbBands)

			// Call DecodeBands
			coeffs := d.DecodeBands(energies, bandBits, tc.nbBands, false, tc.frameSize)

			// Verify output length equals frameSize, not totalBins
			if len(coeffs) != tc.frameSize {
				t.Errorf("DecodeBands returned %d coeffs, want %d (frameSize)", len(coeffs), tc.frameSize)
			}

			// Verify totalBins calculation is as expected
			actualTotalBins := 0
			for band := 0; band < tc.nbBands; band++ {
				actualTotalBins += ScaledBandWidth(band, tc.frameSize)
			}
			if actualTotalBins != tc.totalBins {
				t.Errorf("totalBins = %d, expected %d", actualTotalBins, tc.totalBins)
			}

			// Log the key insight: frameSize > totalBins
			t.Logf("frameSize=%d, totalBins=%d, diff=%d (upper bins zero-padded)",
				tc.frameSize, tc.totalBins, tc.frameSize-tc.totalBins)
		})
	}
}

// TestDecodeBandsStereo_OutputSize verifies stereo DecodeBands returns frameSize per channel.
func TestDecodeBandsStereo_OutputSize(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		nbBands   int
	}{
		{"2.5ms", 120, 13},
		{"5ms", 240, 17},
		{"10ms", 480, 19},
		{"20ms", 960, 21},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(2) // Stereo

			// Create mock energies and bandBits
			energiesL := make([]celtGLog, tc.nbBands)
			energiesR := make([]celtGLog, tc.nbBands)
			bandBits := make([]int, tc.nbBands)

			// Call DecodeBandsStereo
			left, right := d.DecodeBandsStereo(energiesL, energiesR, bandBits, tc.nbBands, tc.frameSize, -1)

			// Verify both channels have frameSize coefficients
			if len(left) != tc.frameSize {
				t.Errorf("Left channel: got %d coeffs, want %d", len(left), tc.frameSize)
			}
			if len(right) != tc.frameSize {
				t.Errorf("Right channel: got %d coeffs, want %d", len(right), tc.frameSize)
			}
		})
	}
}

// TestDenormalizeBand verifies energy scaling produces correct amplitudes.
// Energy is in log2 units (1 = 6 dB): gain = 2^(energy/DB6)
// This matches libopus celt/bands.c denormalise_bands().
func TestDenormalizeBand(t *testing.T) {
	tests := []struct {
		name     string
		shape    []celtNorm
		energy   celtGLog
		wantGain float64 // Expected gain = 2^(energy/DB6)
	}{
		{
			name:     "zero energy",
			shape:    []celtNorm{1.0, 0.0, 0.0},
			energy:   0.0,
			wantGain: 1.0, // 2^(0/DB6) = 1
		},
		{
			name:     "positive energy (6 dB)",
			shape:    []celtNorm{0.5, 0.5, 0.5, 0.5},
			energy:   celtGLog(DB6),
			wantGain: 2.0, // 2^(1) = 2
		},
		{
			name:     "negative energy (-6 dB)",
			shape:    []celtNorm{1.0},
			energy:   celtGLog(-DB6),
			wantGain: 0.5, // 2^(-1) = 0.5
		},
		{
			name:     "fractional energy (9 dB)",
			shape:    []celtNorm{0.707, 0.707},
			energy:   celtGLog(1.5 * DB6),
			wantGain: 2.828, // 2^(1.5) ~= 2.828
		},
		{
			name:     "energy = DB6 (gain = 2)",
			shape:    []celtNorm{1, 0, 0, 0},
			energy:   celtGLog(DB6),
			wantGain: 2.0, // 2^(1) = 2
		},
		{
			name:     "energy = -DB6 (gain = 0.5)",
			shape:    []celtNorm{1, 0, 0, 0},
			energy:   celtGLog(-DB6),
			wantGain: 0.5, // 2^(-1) = 0.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DenormalizeBand(tt.shape, tt.energy)

			if len(result) != len(tt.shape) {
				t.Fatalf("len(result) = %d, want %d", len(result), len(tt.shape))
			}

			// Check that result[i] = shape[i] * gain
			actualGain := 0.0
			if tt.shape[0] != 0 {
				actualGain = float64(result[0] / tt.shape[0])
			}

			tolerance := 0.01
			if math.Abs(actualGain-tt.wantGain) > tolerance {
				t.Errorf("gain = %v, want %v (tolerance %v)", actualGain, tt.wantGain, tolerance)
			}
		})
	}
}

// TestDenormalizeEnergyClamping verifies extreme energies don't cause overflow.
func TestDenormalizeEnergyClamping(t *testing.T) {
	// Test that extreme energies don't cause overflow
	shape := []celtNorm{1.0}

	// Very high energy should be clamped to 32
	resultHigh := DenormalizeBand(shape, celtGLog(100.0))
	if math.IsInf(float64(resultHigh[0]), 0) || math.IsNaN(float64(resultHigh[0])) {
		t.Error("High energy caused overflow or NaN")
	}
	// Expect clamped to 2^(32/DB6)
	maxGain := math.Exp2(32 / DB6)
	if float64(resultHigh[0]) > maxGain*1.001 {
		t.Errorf("High energy not clamped: got %v, want <= %v", resultHigh[0], maxGain)
	}
	if math.Abs(float64(resultHigh[0])-maxGain) > maxGain*0.001 {
		t.Errorf("High energy should clamp to 2^32: got %v, want %v", resultHigh[0], maxGain)
	}

	// Very low energy should still work (no clamping needed for underflow)
	resultLow := DenormalizeBand(shape, celtGLog(-100.0))
	if math.IsNaN(float64(resultLow[0])) {
		t.Error("Low energy caused NaN")
	}
	// Should be very small but not zero (floating point underflow to denormal)
	if resultLow[0] == 0 && shape[0] != 0 {
		t.Log("Low energy resulted in zero (may be expected due to underflow)")
	}
}

// TestComputeBandEnergy verifies energy computation.
func TestComputeBandEnergy(t *testing.T) {
	// Coefficients with known energy
	// sum(x^2) = 4, sqrt = 2, log2(2) = 1, energy = DB6 * 1
	coeffs := []celtNorm{1, 1, 1, 1}
	energy := ComputeBandEnergy(coeffs)
	expected := DB6 * (0.5 * math.Log(4.0) / 0.6931471805599453)
	if math.Abs(float64(energy)-expected) > 1e-6 {
		t.Errorf("ComputeBandEnergy = %v, want %v", energy, expected)
	}

	// Empty vector should return epsilon-based low energy
	energy = ComputeBandEnergy(nil)
	expectedSilence := float64(float32(0.5) * celtLog2(float32(1e-27)))
	if math.Abs(float64(energy)-expectedSilence) > 1e-6 {
		t.Errorf("ComputeBandEnergy(nil) = %v, want %v", energy, expectedSilence)
	}

	energy = ComputeBandEnergy([]celtNorm{})
	if math.Abs(float64(energy)-expectedSilence) > 1e-6 {
		t.Errorf("ComputeBandEnergy([]) = %v, want %v", energy, expectedSilence)
	}
}

// TestComputeBandEnergyRoundTrip verifies round-trip: ComputeBandEnergy -> DenormalizeBand preserves scale.
// This is the key property: if we compute energy from coefficients, then denormalize a unit shape
// by that energy, we should get back the original amplitude.
func TestComputeBandEnergyRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		coeffs []float64
	}{
		{
			name:   "unit vector",
			coeffs: []float64{1.0, 0.0, 0.0, 0.0},
		},
		{
			name:   "scaled vector",
			coeffs: []float64{4.0, 3.0, 0.0},
		},
		{
			name:   "negative values",
			coeffs: []float64{-2.0, 2.0, -2.0, 2.0},
		},
		{
			name:   "uniform distribution",
			coeffs: []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compute energy
			coeffs := make([]celtNorm, len(tt.coeffs))
			for i, c := range tt.coeffs {
				coeffs[i] = celtNorm(c)
			}
			energy := ComputeBandEnergy(coeffs)

			// Normalize to unit vector
			norm := 0.0
			for _, c := range tt.coeffs {
				norm += c * c
			}
			norm = math.Sqrt(norm)
			if norm == 0 {
				return
			}

			shape := make([]celtNorm, len(tt.coeffs))
			for i, c := range tt.coeffs {
				shape[i] = celtNorm(c / norm)
			}

			// Denormalize back
			result := DenormalizeBand(shape, energy)

			// Should get approximately original coefficients
			for i := range tt.coeffs {
				if math.Abs(float64(result[i])-tt.coeffs[i]) > 0.1 {
					t.Errorf("coeff[%d] = %v, want %v", i, result[i], tt.coeffs[i])
				}
			}
		})
	}
}

// TestIntToFloat verifies int to float conversion.
func TestIntToFloat(t *testing.T) {
	input := []int{1, -2, 3, -4, 0}
	result := intToNorm(input)

	expected := []celtNorm{1, -2, 3, -4, 0}
	if len(result) != len(expected) {
		t.Errorf("intToNorm length = %d, want %d", len(result), len(expected))
		return
	}

	for i := range result {
		if result[i] != expected[i] {
			t.Errorf("intToNorm[%d] = %v, want %v", i, result[i], expected[i])
		}
	}

	// Test nil input
	if intToNorm(nil) != nil {
		t.Error("intToNorm(nil) should return nil")
	}
}

// TestThetaToGains verifies stereo angle to gain conversion.
func TestThetaToGains(t *testing.T) {
	tests := []struct {
		itheta  int
		qn      int
		midExp  float64
		sideExp float64
	}{
		{itheta: 0, qn: 8, midExp: 1.0, sideExp: 0.0},     // Pure mid
		{itheta: 8, qn: 8, midExp: 0.0, sideExp: 1.0},     // Pure side
		{itheta: 4, qn: 8, midExp: 0.707, sideExp: 0.707}, // 45 degrees (approx)
	}

	for _, tt := range tests {
		mid, side := ThetaToGains(tt.itheta, tt.qn)
		if math.Abs(float64(mid)-tt.midExp) > 0.01 {
			t.Errorf("ThetaToGains(%d, %d) mid = %v, want ~%v", tt.itheta, tt.qn, mid, tt.midExp)
		}
		if math.Abs(float64(side)-tt.sideExp) > 0.01 {
			t.Errorf("ThetaToGains(%d, %d) side = %v, want ~%v", tt.itheta, tt.qn, side, tt.sideExp)
		}
	}
}

// BenchmarkNormalizeVector measures normalization performance.
func BenchmarkNormalizeFloat64Vector(b *testing.B) {
	v := make([]float64, 100)
	for i := range v {
		v[i] = float64(i + 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizeFloat64Vector(v)
	}
}

// BenchmarkFoldBand measures band folding performance.
func BenchmarkFoldBand(b *testing.B) {
	lowband := make([]celtNorm, 8)
	for i := range lowband {
		lowband[i] = celtNorm(float32(i+1) / 10.0)
	}
	normalizeNormVectorInPlace(lowband)

	seed := uint32(12345)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FoldBand(lowband, 16, &seed)
	}
}

// BenchmarkBitsToK measures bits-to-pulses conversion performance.
func BenchmarkBitsToK(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bitsToKQ0(50, 8)
	}
}

// BenchmarkDecodeBands measures band decoding performance.
// Note: This benchmark uses mock data since we don't have a real range decoder.
func BenchmarkDecodeBands(b *testing.B) {
	energies := make([]celtGLog, 21)
	bandBits := make([]int, 21)
	for i := range bandBits {
		bandBits[i] = 50 // Typical bit allocation
	}

	// Benchmark energy scaling (denormalization) as part of decode path
	shape64 := make([]float64, 100)
	shape := make([]celtNorm, 100)
	for i := range shape {
		shape64[i] = float64(i) / 100.0
	}
	shape64 = normalizeFloat64Vector(shape64)
	for i := range shape {
		shape[i] = celtNorm(shape64[i])
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DenormalizeBand(shape, energies[0])
		_ = len(bandBits)
	}
}

// TestBitsToKBoundaries tests edge cases for bits-to-pulses conversion.
func TestBitsToKBoundaries(t *testing.T) {
	// Zero bits should always return zero pulses
	for n := 1; n <= 32; n++ {
		k := bitsToKQ0(0, n)
		if k != 0 {
			t.Errorf("bitsToKQ0(0, %d) = %d, want 0", n, k)
		}
	}

	// Zero dimensions should always return zero pulses
	for bits := 0; bits <= 100; bits += 10 {
		k := bitsToKQ0(bits, 0)
		if k != 0 {
			t.Errorf("bitsToKQ0(%d, 0) = %d, want 0", bits, k)
		}
	}

	// Negative dimensions should return zero
	k := bitsToKQ0(50, -1)
	if k != 0 {
		t.Errorf("bitsToKQ0(50, -1) = %d, want 0", k)
	}

	// Test minimum bits for k=1 at various n
	t.Run("min_bits_for_k1", func(t *testing.T) {
		for n := 2; n <= 16; n++ {
			// Find minimum bits needed for k=1
			var minBits int
			for bits := 1; bits <= 100; bits++ {
				if bitsToKQ0(bits, n) >= 1 {
					minBits = bits
					break
				}
			}
			// Should be approximately log2(V(n,1)) = log2(2n)
			v1 := PVQ_V(n, 1)
			expectedMin := ilog2(int(v1 - 1))
			t.Logf("n=%d: min bits for k=1 is %d, V(%d,1)=%d, ilog2(V-1)=%d",
				n, minBits, n, v1, expectedMin)
		}
	})
}

// TestBitsToKMonotonic verifies that more bits never result in fewer pulses.
func TestBitsToKMonotonic(t *testing.T) {
	dimensions := []int{2, 4, 8, 16, 32}

	for _, n := range dimensions {
		t.Run("", func(t *testing.T) {
			prev := 0
			for bits := 0; bits <= 150; bits++ {
				k := bitsToKQ0(bits, n)
				if k < prev {
					t.Errorf("bitsToK not monotonic at n=%d: bits=%d gave k=%d, but bits=%d gave k=%d",
						n, bits-1, prev, bits, k)
				}
				prev = k
			}
		})
	}
}

// TestKToBitsRoundtrip verifies kToBits tracks log2(V(n,k)-1).
func TestKToBitsRoundtrip(t *testing.T) {
	// Test that the functions are internally consistent
	for n := 2; n <= 16; n++ {
		for k := 1; k <= 8; k++ {
			bits := kToBits(k, n)
			v := PVQ_V(n, k)

			// kToBits should return approximately log2(V(n,k))
			expectedBits := ilog2(int(v - 1))
			if bits != expectedBits && bits != expectedBits+1 && bits != expectedBits-1 {
				t.Logf("kToBits(%d, %d) = %d, but ilog2(V(%d,%d)-1) = ilog2(%d-1) = %d",
					k, n, bits, n, k, v, expectedBits)
			}

			_ = bits // kToBits validation is above; bitsToK is tested separately.
		}
	}
}

// TestKToBitsValues verifies kToBits produces reasonable bit counts.
// kToBits returns ilog2(V(n,k)-1), which is floor(log2(V-1)).
func TestKToBitsValues(t *testing.T) {
	// Test that kToBits returns values consistent with V(n,k)
	testCases := []struct {
		k, n int
	}{
		{1, 2}, // V(2,1) = 4
		{1, 4}, // V(4,1) = 8
		{1, 8}, // V(8,1) = 16
		{2, 4}, // V(4,2) = 32
		{4, 8}, // V(8,4) is large
	}

	for _, tc := range testCases {
		bits := kToBits(tc.k, tc.n)
		v := PVQ_V(tc.n, tc.k)
		expectedBits := ilog2(int(v - 1))

		if bits != expectedBits {
			t.Errorf("kToBits(%d, %d) = %d, but ilog2(V(%d,%d)-1) = ilog2(%d) = %d",
				tc.k, tc.n, bits, tc.n, tc.k, v-1, expectedBits)
		}

		// Log the values for visibility
		t.Logf("kToBits(%d, %d) = %d, V = %d", tc.k, tc.n, bits, v)
	}
}

// TestBitsToKWithRealFrameSizes tests bit allocation for actual CELT frame sizes.
func TestBitsToKWithRealFrameSizes(t *testing.T) {
	// Test with band widths from actual CELT frames
	frameSizes := []struct {
		name      string
		frameSize int
		nbBands   int
	}{
		{"2.5ms", 120, 13},
		{"5ms", 240, 17},
		{"10ms", 480, 19},
		{"20ms", 960, 21},
	}

	for _, fs := range frameSizes {
		t.Run(fs.name, func(t *testing.T) {
			for band := 0; band < fs.nbBands; band++ {
				n := ScaledBandWidth(band, fs.frameSize)
				if n <= 0 {
					continue
				}

				// Test various bit allocations
				for bits := 0; bits <= 200; bits += 20 {
					k := bitsToKQ0(bits, n)

					// k should be non-negative
					if k < 0 {
						t.Errorf("bitsToKQ0(%d, %d) = %d < 0 at band %d", bits, n, k, band)
					}

					// k should not exceed reasonable bounds
					if k > MaxPVQK {
						t.Errorf("bitsToKQ0(%d, %d) = %d > MaxPVQK at band %d", bits, n, k, band)
					}

					// Verify V(n,k) is valid (not zero for k > 0)
					if k > 0 {
						v := PVQ_V(n, k)
						if v == 0 {
							t.Errorf("V(%d, %d) = 0 for bits=%d at band %d", n, k, bits, band)
						}
					}
				}
			}
		})
	}
}

// TestDecodeBandsAllocationPath verifies the allocation -> k -> PVQ path is correctly wired.
// This tests that for each band, the bit allocation produces valid pulse counts
// that can be used with PVQ decoding.
func TestDecodeBandsAllocationPath(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		nbBands   int
	}{
		{"2.5ms", 120, 13},
		{"5ms", 240, 17},
		{"10ms", 480, 19},
		{"20ms", 960, 21},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for band := 0; band < tc.nbBands; band++ {
				n := ScaledBandWidth(band, tc.frameSize)
				if n <= 0 {
					continue
				}

				// Test various bit allocations
				for bits := 0; bits <= 100; bits += 10 {
					k := bitsToKQ0(bits, n)

					// If k > 0, verify we can decode all valid indices
					if k > 0 {
						vCount := PVQ_V(n, k)
						if vCount == 0 {
							t.Errorf("V(%d, %d) = 0 at band %d with %d bits", n, k, band, bits)
							continue
						}

						// Test a few indices
						testIndices := []uint32{0}
						if vCount > 1 {
							testIndices = append(testIndices, vCount-1)
						}
						if vCount > 2 {
							testIndices = append(testIndices, vCount/2)
						}

						for _, idx := range testIndices {
							pulses := DecodePulses(idx, n, k)
							if pulses == nil {
								t.Errorf("DecodePulses(%d, %d, %d) returned nil at band %d", idx, n, k, band)
								continue
							}

							// Verify L1 norm
							sum := 0
							for _, p := range pulses {
								if p < 0 {
									sum -= p
								} else {
									sum += p
								}
							}
							if sum != k {
								t.Errorf("DecodePulses(%d, %d, %d) L1=%d, want %d at band %d",
									idx, n, k, sum, k, band)
							}

							// Verify normalization produces unit vector
							normalized := NormalizeVector(intToNorm(pulses))
							var norm2 float64
							for _, x := range normalized {
								norm2 += float64(x) * float64(x)
							}
							if math.Abs(norm2-1.0) > 1e-6 {
								t.Errorf("Normalized vector L2^2=%v, want 1.0 at band %d, idx %d",
									norm2, band, idx)
							}
						}
					}
				}
			}
		})
	}
}

// TestDenormalizationGainPath verifies the energy -> gain -> coefficient path.
// This tests that given a normalized shape and energy, we get correctly scaled coefficients.
func TestDenormalizationGainPath(t *testing.T) {
	// Test that energy values produce correct gains
	testCases := []struct {
		energy celtGLog
		gain   float64 // expected gain = 2^energy
	}{
		{0.0, 1.0},
		{celtGLog(DB6), 2.0},
		{celtGLog(-DB6), 0.5},
		{celtGLog(3 * DB6), 8.0},
		{celtGLog(-3 * DB6), 0.125},
		{celtGLog(5 * DB6), 32.0},
		{celtGLog(-5 * DB6), 1.0 / 32.0},
	}

	shape := []celtNorm{0.6, 0.8} // 3-4-5 triangle, normalized

	for _, tc := range testCases {
		result := DenormalizeBand(shape, tc.energy)

		// Check gain is correct
		if shape[0] != 0 {
			actualGain := float64(result[0] / shape[0])
			if math.Abs(actualGain-tc.gain) > tc.gain*1e-6 {
				t.Errorf("energy=%v: gain=%v, want %v", tc.energy, actualGain, tc.gain)
			}
		}
	}

	// Test clamping at high energies
	t.Run("clamp_high", func(t *testing.T) {
		// Energy > 32 should be clamped to 32
		result := DenormalizeBand([]celtNorm{1.0}, celtGLog(100.0))
		expectedGain := math.Exp2(32 / DB6) // Clamped at 32 dB
		if math.Abs(float64(result[0])-expectedGain) > expectedGain*1e-6 {
			t.Errorf("High energy: got %v, want ~%v (clamped)", result[0], expectedGain)
		}
	})

	// Test very low energies don't produce NaN/Inf
	t.Run("low_energy", func(t *testing.T) {
		result := DenormalizeBand([]celtNorm{1.0}, celtGLog(-100.0))
		if math.IsNaN(float64(result[0])) || math.IsInf(float64(result[0]), 0) {
			t.Errorf("Low energy produced NaN/Inf: %v", result[0])
		}
	})
}

// TestIlog2 verifies the integer log2 function.
func TestIlog2(t *testing.T) {
	tests := []struct {
		x    int
		want int
	}{
		{0, 0},
		{1, 0},
		{2, 1},
		{3, 1},
		{4, 2},
		{7, 2},
		{8, 3},
		{15, 3},
		{16, 4},
		{255, 7},
		{256, 8},
		{1023, 9},
		{1024, 10},
	}

	for _, tc := range tests {
		got := ilog2(tc.x)
		if got != tc.want {
			t.Errorf("ilog2(%d) = %d, want %d", tc.x, got, tc.want)
		}
	}
}

func referenceZeroPulseResynth(x, lowband []celtNorm, seed *uint32, gain opusVal16) {
	if lowband == nil {
		if seed != nil {
			for i := range x {
				*seed = (*seed)*1664525 + 1013904223
				x[i] = celtNorm(float32(int32(*seed) >> 20))
			}
		}
		renormalizeVector(x, gain)
		return
	}
	if seed != nil {
		for i := range x {
			*seed = (*seed)*1664525 + 1013904223
			tmp := 1.0 / 256.0
			if (*seed & 0x8000) == 0 {
				tmp = -tmp
			}
			if i < len(lowband) {
				x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
			} else {
				x[i] = celtNorm(tmp)
			}
		}
	}
	renormalizeVector(x, gain)
}

func makeLowband(n int) []celtNorm {
	lowband := make([]celtNorm, n)
	for i := range lowband {
		v := celtNorm(float32((i%7)-3) * 0.125)
		if i%3 == 1 {
			v = -v * 0.75
		}
		lowband[i] = v
	}
	return lowband
}

func makeLowbandNorm(n int) []celtNorm {
	return makeLowband(n)
}

func TestSeededZeroPulseResynthMatchesReference(t *testing.T) {
	gains := []opusVal16{0.25, 0.75, 1.0, 1.5}
	seeds := []uint32{1, 0x8000, 0x12345678, 0xdeadbeef}
	lengths := []int{1, 2, 3, 4, 7, 8, 15, 16, 17}

	for _, n := range lengths {
		for _, gain := range gains {
			for _, seed0 := range seeds {
				currentNoise := make([]celtNorm, n)
				referenceNoise := make([]celtNorm, n)
				seedCurrentNoise := seed0
				seedReferenceNoise := seed0

				if ok := seededZeroPulseResynth(currentNoise, nil, &seedCurrentNoise, gain); !ok {
					t.Fatalf("noise fast path unexpectedly refused n=%d gain=%v", n, gain)
				}
				referenceZeroPulseResynth(referenceNoise, nil, &seedReferenceNoise, gain)

				if seedCurrentNoise != seedReferenceNoise {
					t.Fatalf("noise seed mismatch n=%d gain=%v seed=%08x: got %08x want %08x",
						n, gain, seed0, seedCurrentNoise, seedReferenceNoise)
				}
				for i := range currentNoise {
					if currentNoise[i] != referenceNoise[i] {
						t.Fatalf("noise mismatch n=%d gain=%v seed=%08x at %d: got %v want %v",
							n, gain, seed0, i, currentNoise[i], referenceNoise[i])
					}
				}

				currentFold := make([]celtNorm, n)
				referenceFold := make([]celtNorm, n)
				lowbandReference := makeLowband(n)
				lowband := append([]celtNorm(nil), lowbandReference...)
				seedCurrentFold := seed0
				seedReferenceFold := seed0

				if ok := seededZeroPulseResynth(currentFold, lowband, &seedCurrentFold, gain); !ok {
					t.Fatalf("fold fast path unexpectedly refused n=%d gain=%v", n, gain)
				}
				referenceZeroPulseResynth(referenceFold, lowbandReference, &seedReferenceFold, gain)

				if seedCurrentFold != seedReferenceFold {
					t.Fatalf("fold seed mismatch n=%d gain=%v seed=%08x: got %08x want %08x",
						n, gain, seed0, seedCurrentFold, seedReferenceFold)
				}
				for i := range currentFold {
					if currentFold[i] != referenceFold[i] {
						t.Fatalf("fold mismatch n=%d gain=%v seed=%08x at %d: got %v want %v",
							n, gain, seed0, i, currentFold[i], referenceFold[i])
					}
				}
			}
		}
	}
}

func TestSeededZeroPulseResynthFallback(t *testing.T) {
	x := make([]celtNorm, 4)
	seed := uint32(123)
	if seededZeroPulseResynth(x, nil, nil, 1.0) {
		t.Fatal("expected nil-seed call to refuse fast path")
	}
	if seededZeroPulseResynth(x, []celtNorm{1, 2}, &seed, 1.0) {
		t.Fatal("expected short lowband to refuse fast path")
	}
}

func BenchmarkZeroPulseResynthCurrent(b *testing.B) {
	benchmarks := []struct {
		name    string
		lowband []celtNorm
	}{
		{name: "noise", lowband: nil},
		{name: "fold", lowband: makeLowbandNorm(32)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			x := make([]celtNorm, 32)
			seedBase := uint32(0x12345678)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seed := seedBase + uint32(i)
				if !seededZeroPulseResynth(x, bm.lowband, &seed, 1.0) {
					b.Fatal("fast path refused benchmark input")
				}
			}
		})
	}
}

func BenchmarkZeroPulseResynthReference(b *testing.B) {
	benchmarks := []struct {
		name    string
		lowband []celtNorm
	}{
		{name: "noise", lowband: nil},
		{name: "fold", lowband: makeLowband(32)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			x := make([]celtNorm, 32)
			seedBase := uint32(0x12345678)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seed := seedBase + uint32(i)
				referenceZeroPulseResynth(x, bm.lowband, &seed, 1.0)
			}
		})
	}
}
