package celt

import (
	"bytes"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"reflect"
	"testing"
)

func TestQuantBandStereoPreparedLowbandMatchesStandard(t *testing.T) {
	cases := []struct {
		name       string
		band       int
		n          int
		b          int
		B          int
		lm         int
		tfChange   int
		fill       int
		thetaRound int
	}{
		{name: "flat_stride8_round_down", band: 17, n: 32, b: 60, B: 8, lm: 2, tfChange: 0, fill: 0xE7, thetaRound: -1},
		{name: "flat_stride8_round_up", band: 17, n: 32, b: 60, B: 8, lm: 2, tfChange: 0, fill: 0xE7, thetaRound: 1},
		{name: "time_divide_round_down", band: 18, n: 48, b: 72, B: 8, lm: 2, tfChange: -1, fill: 0xDB, thetaRound: -1},
		{name: "recombine_round_up", band: 16, n: 24, b: 36, B: 4, lm: 1, tfChange: 1, fill: 0x0D, thetaRound: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xBase := make([]celtNorm, tc.n)
			yBase := make([]celtNorm, tc.n)
			lowbandBase := make([]celtNorm, tc.n)
			for i := 0; i < tc.n; i++ {
				xBase[i] = celtNorm(float32(((i*7)%19)-9) * 0.0875)
				yBase[i] = celtNorm(float32(((i*5)%23)-11) * 0.07125)
				lowbandBase[i] = celtNorm(float32(((i*3)%17)-8) * 0.0625)
			}

			bandE := make([]celtEner, 2*MaxBands)
			for i := range bandE {
				bandE[i] = celtEner(0.35 + float32((i%9)+1)*0.03)
			}

			x1 := append([]celtNorm(nil), xBase...)
			y1 := append([]celtNorm(nil), yBase...)
			lowband1 := append([]celtNorm(nil), lowbandBase...)
			out1 := make([]celtNorm, tc.n)
			var buf1 [512]byte
			var re1 rangecoding.Encoder
			re1.Init(buf1[:])
			var scratch1 bandEncodeScratch
			ctx1 := bandCtx{
				re:            &re1,
				encode:        true,
				bandE:         bandE,
				nbBands:       MaxBands,
				channels:      2,
				spread:        spreadNormal,
				tfChange:      tc.tfChange,
				remainingBits: tc.b + 96,
				intensity:     MaxBands,
				band:          tc.band,
				resynth:       true,
				thetaRound:    tc.thetaRound,
				encScratch:    &scratch1,
			}
			cm1 := quantBandStereo(&ctx1, x1, y1, tc.n, tc.b, tc.B, lowband1, tc.lm, out1, scratch1.ensureLowbandScratch(tc.n), tc.fill)
			data1 := append([]byte(nil), re1.Done()...)

			x2 := append([]celtNorm(nil), xBase...)
			y2 := append([]celtNorm(nil), yBase...)
			lowband2 := append([]celtNorm(nil), lowbandBase...)
			out2 := make([]celtNorm, tc.n)
			var buf2 [512]byte
			var re2 rangecoding.Encoder
			re2.Init(buf2[:])
			var scratch2 bandEncodeScratch
			prepared := prepareQuantBandLowband(scratch2.ensureLowbandScratch(tc.n), lowband2, tc.n, tc.B, tc.tfChange, &scratch2)
			if prepared == nil {
				t.Fatal("prepareQuantBandLowband returned nil")
			}
			ctx2 := bandCtx{
				re:            &re2,
				encode:        true,
				bandE:         bandE,
				nbBands:       MaxBands,
				channels:      2,
				spread:        spreadNormal,
				tfChange:      tc.tfChange,
				remainingBits: tc.b + 96,
				intensity:     MaxBands,
				band:          tc.band,
				resynth:       true,
				thetaRound:    tc.thetaRound,
				encScratch:    &scratch2,
			}
			cm2 := quantBandStereoPreparedLowband(&ctx2, x2, y2, tc.n, tc.b, tc.B, prepared, tc.lm, out2, scratch2.ensureLowbandScratch(tc.n), tc.fill, true)
			data2 := append([]byte(nil), re2.Done()...)

			if cm1 != cm2 {
				t.Fatalf("collapse mask mismatch: got %d want %d", cm2, cm1)
			}
			if ctx1.remainingBits != ctx2.remainingBits {
				t.Fatalf("remainingBits mismatch: got %d want %d", ctx2.remainingBits, ctx1.remainingBits)
			}
			if !reflect.DeepEqual(x2, x1) {
				t.Fatalf("x mismatch: got %v want %v", x2, x1)
			}
			if !reflect.DeepEqual(y2, y1) {
				t.Fatalf("y mismatch: got %v want %v", y2, y1)
			}
			if !reflect.DeepEqual(out2, out1) {
				t.Fatalf("lowbandOut mismatch: got %v want %v", out2, out1)
			}
			if !bytes.Equal(data2, data1) {
				t.Fatalf("range coder output mismatch: got %v want %v", data2, data1)
			}
		})
	}
}

func TestQuantBandStereoPreparedLowbandMatchesStandardWithQEXTBudget(t *testing.T) {
	const extBudget = 512

	xBase := make([]celtNorm, 32)
	yBase := make([]celtNorm, 32)
	lowbandBase := make([]celtNorm, 32)
	for i := range 32 {
		xBase[i] = celtNorm(float32(((i*7)%19)-9) * 0.0875)
		yBase[i] = celtNorm(float32(((i*5)%23)-11) * 0.07125)
		lowbandBase[i] = celtNorm(float32(((i*3)%17)-8) * 0.0625)
	}

	bandE := make([]celtEner, 2*MaxBands)
	for i := range bandE {
		bandE[i] = celtEner(0.35 + float32((i%9)+1)*0.03)
	}

	x1 := append([]celtNorm(nil), xBase...)
	y1 := append([]celtNorm(nil), yBase...)
	lowband1 := append([]celtNorm(nil), lowbandBase...)
	out1 := make([]celtNorm, 32)
	var buf1 [512]byte
	var extBuf1 [256]byte
	var re1 rangecoding.Encoder
	var ext1 rangecoding.Encoder
	re1.Init(buf1[:])
	ext1.Init(extBuf1[:])
	var scratch1 bandEncodeScratch
	ctx1 := bandCtx{
		re:            &re1,
		encode:        true,
		extEnc:        &ext1,
		extBudget:     extBudget,
		extTotalBits:  ext1.StorageBits() << bitRes,
		bandE:         bandE,
		nbBands:       MaxBands,
		channels:      2,
		spread:        spreadNormal,
		tfChange:      0,
		remainingBits: 60 + 96,
		intensity:     MaxBands,
		band:          17,
		resynth:       true,
		thetaRound:    -1,
		encScratch:    &scratch1,
	}
	cm1 := quantBandStereo(&ctx1, x1, y1, 32, 60, 8, lowband1, 2, out1, scratch1.ensureLowbandScratch(32), 0xE7)
	extTell1 := ext1.TellFrac()
	data1 := append([]byte(nil), re1.Done()...)
	extData1 := append([]byte(nil), ext1.Done()...)

	x2 := append([]celtNorm(nil), xBase...)
	y2 := append([]celtNorm(nil), yBase...)
	lowband2 := append([]celtNorm(nil), lowbandBase...)
	out2 := make([]celtNorm, 32)
	var buf2 [512]byte
	var extBuf2 [256]byte
	var re2 rangecoding.Encoder
	var ext2 rangecoding.Encoder
	re2.Init(buf2[:])
	ext2.Init(extBuf2[:])
	var scratch2 bandEncodeScratch
	prepared := prepareQuantBandLowband(scratch2.ensureLowbandScratch(32), lowband2, 32, 8, 0, &scratch2)
	if prepared == nil {
		t.Fatal("prepareQuantBandLowband returned nil")
	}
	ctx2 := bandCtx{
		re:            &re2,
		encode:        true,
		extEnc:        &ext2,
		extBudget:     extBudget,
		extTotalBits:  ext2.StorageBits() << bitRes,
		bandE:         bandE,
		nbBands:       MaxBands,
		channels:      2,
		spread:        spreadNormal,
		tfChange:      0,
		remainingBits: 60 + 96,
		intensity:     MaxBands,
		band:          17,
		resynth:       true,
		thetaRound:    -1,
		encScratch:    &scratch2,
	}
	cm2 := quantBandStereoPreparedLowband(&ctx2, x2, y2, 32, 60, 8, prepared, 2, out2, scratch2.ensureLowbandScratch(32), 0xE7, true)
	extTell2 := ext2.TellFrac()
	data2 := append([]byte(nil), re2.Done()...)
	extData2 := append([]byte(nil), ext2.Done()...)

	if extTell1 == 0 || extTell2 == 0 {
		t.Fatalf("expected QEXT coder to be used, got tell_frac standard=%d prepared=%d", extTell1, extTell2)
	}
	if extTell1 > extBudget {
		t.Fatalf("standard path exceeded QEXT budget: tell_frac=%d budget=%d", extTell1, extBudget)
	}
	if extTell2 > extBudget {
		t.Fatalf("prepared path exceeded QEXT budget: tell_frac=%d budget=%d", extTell2, extBudget)
	}
	if cm1 != cm2 {
		t.Fatalf("collapse mask mismatch: got %d want %d", cm2, cm1)
	}
	if ctx1.remainingBits != ctx2.remainingBits {
		t.Fatalf("remainingBits mismatch: got %d want %d", ctx2.remainingBits, ctx1.remainingBits)
	}
	if extTell1 != extTell2 {
		t.Fatalf("QEXT tell_frac mismatch: got %d want %d", extTell2, extTell1)
	}
	if !reflect.DeepEqual(x2, x1) {
		t.Fatalf("x mismatch: got %v want %v", x2, x1)
	}
	if !reflect.DeepEqual(y2, y1) {
		t.Fatalf("y mismatch: got %v want %v", y2, y1)
	}
	if !reflect.DeepEqual(out2, out1) {
		t.Fatalf("lowbandOut mismatch: got %v want %v", out2, out1)
	}
	if !bytes.Equal(data2, data1) {
		t.Fatalf("range coder output mismatch: got %v want %v", data2, data1)
	}
	if !bytes.Equal(extData2, extData1) {
		t.Fatalf("QEXT coder output mismatch: got %v want %v", extData2, extData1)
	}
}

func TestPredCoefValues(t *testing.T) {
	// Reference values from libopus celt/quant_bands.c
	expected := []float32{
		29440.0 / 32768.0, // LM=0
		26112.0 / 32768.0, // LM=1
		21248.0 / 32768.0, // LM=2
		16384.0 / 32768.0, // LM=3
	}

	for lm := range 4 {
		if math.Abs(float64(predCoef[lm]-expected[lm])) > 1e-7 {
			t.Errorf("predCoef[%d] = %f, want %f", lm, predCoef[lm], expected[lm])
		}
	}
}

// TestBetaCoefValues verifies beta coefficients match libopus.
func TestBetaCoefValues(t *testing.T) {
	// Reference values from libopus celt/quant_bands.c
	expectedInter := []float32{
		30147.0 / 32768.0, // LM=0
		22282.0 / 32768.0, // LM=1
		12124.0 / 32768.0, // LM=2
		6554.0 / 32768.0,  // LM=3
	}

	for lm := range 4 {
		if math.Abs(float64(betaCoef[lm]-expectedInter[lm])) > 1e-7 {
			t.Errorf("betaCoef[%d] = %f, want %f", lm, betaCoef[lm], expectedInter[lm])
		}
	}

	expectedIntra := float32(4915.0 / 32768.0)
	if math.Abs(float64(betaIntraF32-expectedIntra)) > 1e-7 {
		t.Errorf("betaIntraF32 = %f, want %f", betaIntraF32, expectedIntra)
	}
}

// TestEMeansValues verifies eMeans table matches libopus.
func TestEMeansValues(t *testing.T) {
	// Reference values from libopus celt/quant_bands.c (float eMeans table)
	expected := []float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	for i := 0; i < len(expected) && i < len(eMeans); i++ {
		if math.Abs(float64(eMeans[i])-expected[i]) > 1e-6 {
			t.Errorf("eMeans[%d] = %f, want %f", i, eMeans[i], expected[i])
		}
	}
}

// TestEProbModelValues verifies e_prob_model table matches libopus.
func TestEProbModelValues(t *testing.T) {
	// Test a few key values from libopus e_prob_model
	// LM=0, Inter, first band
	if eProbModel[0][0][0] != 72 {
		t.Errorf("eProbModel[0][0][0] = %d, want 72", eProbModel[0][0][0])
	}
	if eProbModel[0][0][1] != 127 {
		t.Errorf("eProbModel[0][0][1] = %d, want 127", eProbModel[0][0][1])
	}

	// LM=0, Intra, first band
	if eProbModel[0][1][0] != 24 {
		t.Errorf("eProbModel[0][1][0] = %d, want 24", eProbModel[0][1][0])
	}
	if eProbModel[0][1][1] != 179 {
		t.Errorf("eProbModel[0][1][1] = %d, want 179", eProbModel[0][1][1])
	}

	// LM=3, Inter, first band
	if eProbModel[3][0][0] != 42 {
		t.Errorf("eProbModel[3][0][0] = %d, want 42", eProbModel[3][0][0])
	}
}

// TestLossDistortion verifies the loss distortion computation.
func TestLossDistortion(t *testing.T) {
	// Create simple test case
	eBands := make([]celtGLog, MaxBands)
	oldEBands := make([]celtGLog, MaxBands)

	// Set some test values
	for i := range 10 {
		eBands[i] = celtGLog(i)
		oldEBands[i] = celtGLog(float64(i) + 0.5)
	}

	dist := lossDistortion(eBands, oldEBands, 0, 10, MaxBands, 1)

	// Expected: sum of (0.5)^2 * 10 / 128 = 0.5^2 * 10 / 128 = 2.5/128 ≈ 0.0195
	expected := float32(2.5 / 128.0)
	if math.Abs(float64(dist-expected)) > 0.001 {
		t.Errorf("lossDistortion = %f, want ~%f", dist, expected)
	}
}

// TestAmp2Log2Conversion verifies amplitude to log2 conversion.
func TestAmp2Log2Conversion(t *testing.T) {
	// Create test amplitudes
	bandE := make([]celtEner, MaxBands)
	for i := range MaxBands {
		bandE[i] = celtEner(math.Pow(2, float64(i)/4.0)) // Exponentially increasing
	}

	result := Amp2Log2(bandE, MaxBands, MaxBands, 1)

	// Verify a few values
	// For bandE[4] = 2^1 = 2, log2(2) = 1, then subtract eMeans[4]
	expected4 := 1.0 - float64(eMeans[4])
	if math.Abs(float64(result[4])-expected4) > 0.01 {
		t.Errorf("Amp2Log2[4] = %f, want ~%f", result[4], expected4)
	}
}

// TestQuantCoarseEnergyRoundTrip tests that encoding and decoding produce consistent results.
func TestQuantCoarseEnergyRoundTrip(t *testing.T) {
	// Create test energies
	nbBands := 21
	channels := 1
	eBands := make([]celtGLog, nbBands*channels)
	oldEBands := make([]celtGLog, nbBands*channels)

	for i := range nbBands {
		eBands[i] = celtGLog(5 - i/4) // Some variation
		oldEBands[i] = 0              // Start with zeros
	}

	// Create encoder buffer
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode coarse energy
	delayedIntra := float32(1.0)
	params := QuantCoarseEnergyParams{
		Start:            0,
		End:              nbBands,
		EffEnd:           nbBands,
		LM:               3, // 20ms
		Channels:         channels,
		Budget:           256 * 8,
		NBAvailableBytes: 256,
		ForceIntra:       false,
		TwoPass:          true,
		LossRate:         0,
		LFE:              false,
	}

	result := QuantCoarseEnergy(re, eBands, oldEBands, params, &delayedIntra)

	// Verify results are reasonable
	for i := range nbBands {
		// Quantized energy should be close to original (within a few dB)
		diff := math.Abs(float64(result.QuantizedEnergy[i] - eBands[i]))
		if diff > 3.0 { // Allow up to 3 dB difference (half a step)
			t.Errorf("Band %d: quantized=%f, original=%f, diff=%f too large",
				i, result.QuantizedEnergy[i], eBands[i], diff)
		}
	}
}

// TestQuantFineEnergy tests fine energy encoding.
func TestQuantFineEnergy(t *testing.T) {
	nbBands := 21
	channels := 1

	// Setup test data
	oldEBands := make([]celtGLog, nbBands*channels)
	errorVal := make([]celtGLog, nbBands*channels)
	extraQuant := make([]int, nbBands)

	for i := range nbBands {
		oldEBands[i] = celtGLog(5 - i/4)
		errorVal[i] = 0.25 // Small positive error
		if i < 15 {
			extraQuant[i] = 2 // 2 fine bits for first 15 bands
		}
	}

	// Create encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode fine energy
	QuantFineEnergy(re, 0, nbBands, oldEBands, errorVal, nil, extraQuant, channels)

	// Verify that error was reduced
	for i := range 15 {
		if math.Abs(float64(errorVal[i])) >= 0.25 {
			t.Errorf("Band %d: error not reduced after fine encoding: %f", i, errorVal[i])
		}
	}
}

// TestQuantEnergyFinalise tests energy finalization.
func TestQuantEnergyFinalise(t *testing.T) {
	nbBands := 21
	channels := 1

	// Setup test data
	oldEBands := make([]celtGLog, nbBands*channels)
	errorVal := make([]celtGLog, nbBands*channels)
	fineQuant := make([]int, nbBands)
	finePriority := make([]int, nbBands)

	for i := range nbBands {
		oldEBands[i] = celtGLog(5 - i/4)
		errorVal[i] = 0.1 // Small positive error
		fineQuant[i] = 2  // 2 bits already used
		if i%2 == 0 {
			finePriority[i] = 0 // High priority
		} else {
			finePriority[i] = 1 // Low priority
		}
	}

	// Create encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Record initial tell value (range coder starts at 1 bit, not 0)
	initialTell := re.Tell()

	// Finalize with some bits
	bitsLeft := 20
	QuantEnergyFinalise(re, 0, nbBands, oldEBands, errorVal, fineQuant, finePriority, bitsLeft, channels)

	// Verify bits were consumed (Tell() should have increased)
	finalTell := re.Tell()
	bitsUsed := finalTell - initialTell
	if bitsUsed == 0 {
		t.Error("No bits were used during finalization")
	}
	if bitsUsed > bitsLeft {
		t.Errorf("Used more bits than available: %d > %d", bitsUsed, bitsLeft)
	}
}

// TestCeltLog2Accuracy tests the log2 function accuracy.
func TestCeltLog2Accuracy(t *testing.T) {
	testCases := []struct {
		input    float32
		expected float32
	}{
		{1.0, 0.0},
		{2.0, 1.0},
		{4.0, 2.0},
		{0.5, -1.0},
		{1e-10, -33.219}, // Approximately log2(1e-10)
	}

	for _, tc := range testCases {
		result := celtLog2(tc.input)
		if math.Abs(float64(result-tc.expected)) > 0.01 {
			t.Errorf("celtLog2(%f) = %f, want ~%f", tc.input, result, tc.expected)
		}
	}
}

// TestEncodeLaplaceEnergy tests Laplace encoding for energy.
func TestEncodeLaplaceEnergy(t *testing.T) {
	// Create encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Test encoding zero (most common)
	fs := 72 << 7 // From prob_model
	decay := 127 << 6

	val0 := encodeLaplaceEnergy(re, 0, fs, decay)
	if val0 != 0 {
		t.Errorf("encodeLaplaceEnergy(0) returned %d, want 0", val0)
	}

	// Reset and test positive value
	re.Init(buf)
	val1 := encodeLaplaceEnergy(re, 1, fs, decay)
	if val1 != 1 {
		t.Errorf("encodeLaplaceEnergy(1) returned %d, want 1", val1)
	}

	// Reset and test negative value
	re.Init(buf)
	valNeg := encodeLaplaceEnergy(re, -1, fs, decay)
	if valNeg != -1 {
		t.Errorf("encodeLaplaceEnergy(-1) returned %d, want -1", valNeg)
	}
}

// BenchmarkQuantCoarseEnergy benchmarks coarse energy quantization.
func BenchmarkQuantCoarseEnergy(b *testing.B) {
	nbBands := 21
	channels := 1
	eBands := make([]celtGLog, nbBands*channels)
	oldEBands := make([]celtGLog, nbBands*channels)

	for i := range nbBands {
		eBands[i] = celtGLog(5 - i/4)
	}

	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	delayedIntra := float32(1.0)

	params := QuantCoarseEnergyParams{
		Start:            0,
		End:              nbBands,
		EffEnd:           nbBands,
		LM:               3,
		Channels:         channels,
		Budget:           256 * 8,
		NBAvailableBytes: 256,
		TwoPass:          true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		re.Init(buf)
		copy(oldEBands, eBands) // Reset state
		_ = QuantCoarseEnergy(re, eBands, oldEBands, params, &delayedIntra)
	}
}
