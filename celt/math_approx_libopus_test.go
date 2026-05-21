package celt

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCELTLog2MatchesLibopusFloatApprox(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		math.SmallestNonzeroFloat32,
		1e-30, 1e-20, 1e-10, 1e-5, 0.03125,
		0.5, 0.75, 0.99999994, 1, 1.0000001,
		1.125, 1.25, 1.5, 1.875, 2, 3, 8, 1024,
	}
	for exp := int32(-12); exp <= 12; exp++ {
		for mant := uint32(0); mant < 8; mant++ {
			bits := uint32(exp+127)<<23 | mant<<20 | 0x12345
			samples = append(samples, math.Float32frombits(bits))
		}
	}
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeLog2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, sample := range samples {
		got := celtLog2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("celtLog2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}

func TestCELTExp2MatchesLibopusFloatApprox(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		-60, -51, -50.5, -50, -24, -10,
		-1.75, -1.5, -1.25, -1, -0.75, -0.5, -0.25,
		0, 0.25, 0.5, 0.75, 1, 1.25, 2, 5, 10, 24,
	}
	for integer := int32(-12); integer <= 12; integer++ {
		for _, frac := range []float32{0, 0.0625, 0.125, 0.33325195, 0.5, 0.875, 0.99902344} {
			samples = append(samples, float32(integer)+frac)
		}
	}
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeExp2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, sample := range samples {
		got := celtExp2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("celtExp2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}

func TestCELTAngleMathMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	angleSamples := []float32{
		-1.25, -1, -0.5, -0.001, 0, 1e-20, 1e-10, 1e-5,
		0.001, 0.03125, 0.125, 0.25, 0.5, 0.75, 0.875,
		0.99999994, 1, 1.0000001, 1.25, 2, 3.75,
	}
	seed := uint32(0x6d2b79f5)
	for i := 0; i < 128; i++ {
		word := nextCELTMathWord(&seed)
		sample := float32(int32(word%20001)-10000) / 8192
		angleSamples = append(angleSamples, sample)
	}

	t.Run("atanNorm", func(t *testing.T) {
		inputs := make([]float32, 0, len(angleSamples))
		for _, sample := range angleSamples {
			if sample >= 0 && sample <= 1 {
				inputs = append(inputs, sample)
			}
		}
		want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeAtanNorm, inputs)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, sample := range inputs {
			got := celtAtanNormF32(sample)
			if math.Float32bits(got) != math.Float32bits(want[i]) {
				t.Fatalf("celtAtanNormF32(%g)=%08x(%g) want %08x(%g)",
					sample,
					math.Float32bits(got), got,
					math.Float32bits(want[i]), want[i],
				)
			}
			got64 := float32(celtAtanNorm(float64(sample)))
			if math.Float32bits(got64) != math.Float32bits(want[i]) {
				t.Fatalf("celtAtanNorm(%g)=%08x(%g) want %08x(%g)",
					sample,
					math.Float32bits(got64), got64,
					math.Float32bits(want[i]), want[i],
				)
			}
		}
	})

	t.Run("atan2pNorm", func(t *testing.T) {
		type pair struct {
			y float32
			x float32
		}
		pairs := []pair{
			{0, 0}, {0, 1}, {1, 0}, {1, 1},
			{1e-10, 1e-10}, {1e-5, 0.25}, {0.25, 1e-5},
			{0.125, 0.875}, {0.875, 0.125}, {0.99999994, 1},
		}
		seed := uint32(0x1234abcd)
		for i := 0; i < 128; i++ {
			y := float32(nextCELTMathWord(&seed)%20001) / 8192
			x := float32(nextCELTMathWord(&seed)%20001) / 8192
			pairs = append(pairs, pair{y: y, x: x})
		}
		words := make([]uint32, 0, 2*len(pairs))
		for _, p := range pairs {
			words = append(words, math.Float32bits(p.x), math.Float32bits(p.y))
		}
		want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeAtan2pNorm, len(pairs), words)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, p := range pairs {
			got := celtAtan2pNormF32(p.y, p.x)
			if math.Float32bits(got) != want[i] {
				t.Fatalf("celtAtan2pNormF32(%g,%g)=%08x(%g) want %08x(%g)",
					p.y, p.x,
					math.Float32bits(got), got,
					want[i], math.Float32frombits(want[i]),
				)
			}
			got64 := float32(celtAtan2pNorm(float64(p.y), float64(p.x)))
			if math.Float32bits(got64) != want[i] {
				t.Fatalf("celtAtan2pNorm(%g,%g)=%08x(%g) want %08x(%g)",
					p.y, p.x,
					math.Float32bits(got64), got64,
					want[i], math.Float32frombits(want[i]),
				)
			}
		}
	})

	t.Run("cosNorm2", func(t *testing.T) {
		want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeCosNorm2, angleSamples)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, sample := range angleSamples {
			got := float32(celtCosNorm2(float64(sample)))
			if math.Float32bits(got) != math.Float32bits(want[i]) {
				t.Fatalf("celtCosNorm2(%g)=%08x(%g) want %08x(%g)",
					sample,
					math.Float32bits(got), got,
					math.Float32bits(want[i]), want[i],
				)
			}
		}
	})
}

func TestCELTStereoIthetaQ30MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopustest.CELTStereoIthetaCase{
		{Stereo: false, X: []float32{1}, Y: []float32{0}},
		{Stereo: false, X: []float32{0}, Y: []float32{1}},
		{Stereo: false, X: []float32{0.70710677}, Y: []float32{0.70710677}},
		{Stereo: true, X: []float32{1, 0.5, -0.25, 0.125}, Y: []float32{1, -0.5, 0.25, -0.125}},
		{Stereo: true, X: []float32{0.25, -0.5, 0.75, -1}, Y: []float32{-0.25, 0.5, -0.75, 1}},
	}
	seed := uint32(0x89abcdef)
	for _, n := range []int{1, 2, 3, 5, 16, 31, 64} {
		for _, stereo := range []bool{false, true} {
			x := make([]float32, n)
			y := make([]float32, n)
			for i := 0; i < n; i++ {
				x[i] = float32(int32(nextCELTMathWord(&seed)%20001)-10000) / 10000
				y[i] = float32(int32(nextCELTMathWord(&seed)%20001)-10000) / 10000
			}
			cases = append(cases, libopustest.CELTStereoIthetaCase{Stereo: stereo, X: x, Y: y})
		}
	}

	want, err := libopustest.ProbeCELTStereoIthetaQ30(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, tc := range cases {
		got := stereoIthetaQ30(float32SliceToFloat64(tc.X), float32SliceToFloat64(tc.Y), tc.Stereo)
		if !stereoIthetaQ30MatchesLibopusBuild(got, int(want[i])) {
			t.Fatalf("case %d stereo=%v n=%d stereoIthetaQ30=%d want %d",
				i, tc.Stereo, len(tc.X), got, int32(want[i]))
		}
	}
}

func stereoIthetaQ30MatchesLibopusBuild(got, want int) bool {
	if int32(got) == int32(want) {
		return true
	}
	// Apple clang may contract the arm64 float accumulation inside libopus.
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		diff := got - want
		if diff < 0 {
			diff = -diff
		}
		return diff <= 64
	}
	return false
}

func float32SliceToFloat64(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func TestCELTBitexactCosMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	inputs := make([]uint32, 0, bitexactThetaMax+1)
	for x := uint32(0); x <= bitexactThetaMax; x++ {
		inputs = append(inputs, x)
	}
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeBitexactCos, len(inputs), inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, x := range inputs {
		got := bitexactCos(int(x))
		if got != int(int32(want[i])) {
			t.Fatalf("bitexactCos(%d)=%d want %d", x, got, int32(want[i]))
		}
	}
}

func TestCELTBitexactLog2TanMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	type pair struct {
		isin int
		icos int
	}
	pairs := []pair{
		{isin: 32767, icos: 200},
		{isin: 30274, icos: 12540},
		{isin: 23171, icos: 23171},
		{isin: 200, icos: 32767},
		{isin: 12540, icos: 30274},
	}
	edgeValues := []int{
		1, 2, 3, 15, 16, 17, 31, 32, 33, 63, 64, 65,
		127, 128, 129, 199, 200, 201, 255, 256, 257,
		511, 512, 513, 1023, 1024, 1025, 2047, 2048, 2049,
		4095, 4096, 4097, 8191, 8192, 8193, 12539, 12540, 12541,
		16383, 16384, 16385, 23170, 23171, 23172, 30273, 30274, 30275,
		32765, 32766, 32767,
	}
	for _, isin := range edgeValues {
		for _, icos := range edgeValues {
			pairs = append(pairs, pair{isin: isin, icos: icos})
		}
	}
	seed := uint32(0x5eed1234)
	for i := 0; i < 2048; i++ {
		isin := int(nextCELTMathWord(&seed)%32767) + 1
		icos := int(nextCELTMathWord(&seed)%32767) + 1
		pairs = append(pairs, pair{isin: isin, icos: icos})
	}
	words := make([]uint32, 0, 2*len(pairs))
	for _, p := range pairs {
		words = append(words, uint32(int32(p.isin)), uint32(int32(p.icos)))
	}
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeBitexactLog2Tan, len(pairs), words)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, p := range pairs {
		got := bitexactLog2tan(p.isin, p.icos)
		if got != int(int32(want[i])) {
			t.Fatalf("bitexactLog2tan(%d,%d)=%d want %d", p.isin, p.icos, got, int32(want[i]))
		}
	}
}

func TestCELTBitexactLog2TanThetaTableMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	inputs := make([]uint32, 0, 16320-64+1)
	for theta := 64; theta <= 16320; theta++ {
		inputs = append(inputs, uint32(theta))
	}
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeLog2TanTheta, len(inputs), inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, theta := range inputs {
		got := bitexactLog2tanTheta(int(theta))
		if got != int(int32(want[i])) {
			t.Fatalf("bitexactLog2tanTheta(%d)=%d want %d", theta, got, int32(want[i]))
		}
	}
}

func nextCELTMathWord(seed *uint32) uint32 {
	*seed = 1664525*(*seed) + 1013904223
	return *seed
}

func TestCELTIntegerMathMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	t.Run("fracMul16", func(t *testing.T) {
		values := []int{
			-1 << 31, (-1 << 31) + 1, -40000, -32769, -32768, -32767, -12345,
			-1, 0, 1, 12345, 32766, 32767, 32768, 32769, 40000,
			(1 << 31) - 2, (1 << 31) - 1,
		}
		pairs := make([][2]int, 0, len(values)*len(values)+512)
		for _, a := range values {
			for _, b := range values {
				pairs = append(pairs, [2]int{a, b})
			}
		}
		seed := uint32(0x9e3779b9)
		for i := 0; i < 512; i++ {
			a := int(int32(nextCELTMathWord(&seed)))
			b := int(int32(nextCELTMathWord(&seed)))
			pairs = append(pairs, [2]int{a, b})
		}
		words := make([]uint32, 0, 2*len(pairs))
		for _, p := range pairs {
			words = append(words, uint32(int32(p[0])), uint32(int32(p[1])))
		}
		want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeFracMul16, len(pairs), words)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, p := range pairs {
			got := fracMul16(p[0], p[1])
			if got != int(int32(want[i])) {
				t.Fatalf("fracMul16(%d,%d)=%d want %d", p[0], p[1], got, int32(want[i]))
			}
		}
	})

	t.Run("isqrt32", func(t *testing.T) {
		inputs := []uint32{
			1, 2, 3, 4, 15, 16, 17,
			(1 << 16) - 1, 1 << 16, (1 << 16) + 1,
			(1 << 24) - 1, 1 << 24, (1 << 24) + 1,
			^uint32(0) - 2, ^uint32(0) - 1, ^uint32(0),
		}
		for i := uint32(1); i < 65536; i += 257 {
			inputs = append(inputs, i*i, i*i+1)
			if i > 0 {
				inputs = append(inputs, i*i-1)
			}
		}
		want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeISqrt32, len(inputs), inputs)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, x := range inputs {
			got := isqrt32(x)
			if got != want[i] {
				t.Fatalf("isqrt32(%d)=%d want %d", x, got, want[i])
			}
		}
	})

	t.Run("udiv", func(t *testing.T) {
		numerators := []uint32{
			0, 1, 2, 3, 7, 15, 16, 17, 127, 128, 129,
			255, 256, 257, 65535, 65536, 65537, 1 << 31,
			(1 << 31) + 1, ^uint32(0) - 1, ^uint32(0),
		}
		divisors := []uint32{1, 2, 3, 5, 7, 31, 127, 128, 129, 255, 256, 257, 65535, 65536, 65537, 1 << 31, ^uint32(0)}
		pairs := make([][2]uint32, 0, len(numerators)*len(divisors)+512)
		for _, n := range numerators {
			for _, d := range divisors {
				pairs = append(pairs, [2]uint32{n, d})
			}
		}
		seed := uint32(0x31415926)
		for i := 0; i < 512; i++ {
			n := nextCELTMathWord(&seed)
			d := nextCELTMathWord(&seed)
			if d == 0 {
				d = 1
			}
			pairs = append(pairs, [2]uint32{n, d})
		}
		words := make([]uint32, 0, 2*len(pairs))
		for _, p := range pairs {
			words = append(words, p[0], p[1])
		}
		want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeUdiv, len(pairs), words)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, p := range pairs {
			got := celtUdiv(int(p[0]), int(p[1]))
			if uint32(got) != want[i] {
				t.Fatalf("celtUdiv(%d,%d)=%d want %d", p[0], p[1], got, want[i])
			}
		}
	})

	t.Run("sudiv", func(t *testing.T) {
		pairs := [][2]int32{
			{-2147483647, 3},
			{-2147483647, 1},
			{-65536, 257},
			{-32769, 128},
			{-32768, 127},
			{-32767, 129},
			{-3, 2},
			{-1, 2},
			{0, 1},
			{1, 2},
			{3, 2},
			{32767, 129},
			{32768, 127},
			{32769, 128},
			{2147483647, 65535},
			{2147483647, 1},
		}
		seed := uint32(0x27182818)
		for i := 0; i < 512; i++ {
			n := int32(nextCELTMathWord(&seed))
			if n == int32(-1<<31) {
				n++
			}
			d := int32(nextCELTMathWord(&seed) & 0x7fffffff)
			if d == 0 {
				d = 1
			}
			pairs = append(pairs, [2]int32{n, d})
		}
		words := make([]uint32, 0, 2*len(pairs))
		for _, p := range pairs {
			words = append(words, uint32(p[0]), uint32(p[1]))
		}
		want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeSudiv, len(pairs), words)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt math", err)
		}
		for i, p := range pairs {
			got := celtSudiv(int(p[0]), int(p[1]))
			if int32(got) != int32(want[i]) {
				t.Fatalf("celtSudiv(%d,%d)=%d want %d", p[0], p[1], got, int32(want[i]))
			}
		}
	})
}
