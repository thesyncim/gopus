package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTMathInputMagic  = "GCMI"
	libopusCELTMathOutputMagic = "GCMO"

	libopusCELTMathModeLog2 = uint32(0)
	libopusCELTMathModeExp2 = uint32(1)

	libopusCELTMathModeFracMul16       = uint32(2)
	libopusCELTMathModeBitexactCos     = uint32(3)
	libopusCELTMathModeBitexactLog2Tan = uint32(4)
	libopusCELTMathModeISqrt32         = uint32(5)
	libopusCELTMathModeUdiv            = uint32(6)
	libopusCELTMathModeSudiv           = uint32(7)
	libopusCELTMathModeLog2TanTheta    = uint32(8)
	libopusCELTMathModeAtanNorm        = uint32(9)
	libopusCELTMathModeAtan2pNorm      = uint32(10)
	libopusCELTMathModeCosNorm2        = uint32(11)
	libopusCELTMathModeStereoIthetaQ30 = uint32(12)
)

var libopusCELTMathHelper libopustest.HelperCache

type libopusCELTStereoIthetaCase struct {
	stereo bool
	x      []float32
	y      []float32
}

func buildLibopusCELTMathHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt math",
		OutputBase:  "gopus_libopus_celt_math",
		SourceFile:  "libopus_celt_math_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getLibopusCELTMathHelperPath() (string, error) {
	return libopusCELTMathHelper.Path(buildLibopusCELTMathHelper)
}

func probeLibopusCELTMath(mode uint32, samples []float32) ([]float32, error) {
	binPath, err := getLibopusCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusCELTMathInputMagic, mode, uint32(len(samples)))
	for _, sample := range samples {
		payload.Float32(sample)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt math", libopusCELTMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
	reader.ExpectRemaining(4 * count)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusCELTMathWords(mode uint32, count int, words []uint32) ([]uint32, error) {
	binPath, err := getLibopusCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusCELTMathInputMagic, mode, uint32(count))
	for _, word := range words {
		payload.U32(word)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt math", libopusCELTMathOutputMagic)
	if err != nil {
		return nil, err
	}
	gotCount := reader.Count(count)
	reader.ExpectRemaining(4 * gotCount)
	out := make([]uint32, gotCount)
	for i := range out {
		out[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusCELTStereoIthetaQ30(cases []libopusCELTStereoIthetaCase) ([]uint32, error) {
	binPath, err := getLibopusCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusCELTMathInputMagic, libopusCELTMathModeStereoIthetaQ30, uint32(len(cases)))
	for _, tc := range cases {
		if tc.stereo {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(tc.x)))
		for _, v := range tc.x {
			payload.Float32(v)
		}
		for _, v := range tc.y {
			payload.Float32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt math", libopusCELTMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(4 * count)
	out := make([]uint32, count)
	for i := range out {
		out[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

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
	want, err := probeLibopusCELTMath(libopusCELTMathModeLog2, samples)
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
	want, err := probeLibopusCELTMath(libopusCELTMathModeExp2, samples)
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
		want, err := probeLibopusCELTMath(libopusCELTMathModeAtanNorm, inputs)
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
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeAtan2pNorm, len(pairs), words)
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
		want, err := probeLibopusCELTMath(libopusCELTMathModeCosNorm2, angleSamples)
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
	cases := []libopusCELTStereoIthetaCase{
		{stereo: false, x: []float32{1}, y: []float32{0}},
		{stereo: false, x: []float32{0}, y: []float32{1}},
		{stereo: false, x: []float32{0.70710677}, y: []float32{0.70710677}},
		{stereo: true, x: []float32{1, 0.5, -0.25, 0.125}, y: []float32{1, -0.5, 0.25, -0.125}},
		{stereo: true, x: []float32{0.25, -0.5, 0.75, -1}, y: []float32{-0.25, 0.5, -0.75, 1}},
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
			cases = append(cases, libopusCELTStereoIthetaCase{stereo: stereo, x: x, y: y})
		}
	}

	want, err := probeLibopusCELTStereoIthetaQ30(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, tc := range cases {
		got := stereoIthetaQ30(float32SliceToFloat64(tc.x), float32SliceToFloat64(tc.y), tc.stereo)
		if int32(got) != int32(want[i]) {
			t.Fatalf("case %d stereo=%v n=%d stereoIthetaQ30=%d want %d",
				i, tc.stereo, len(tc.x), got, int32(want[i]))
		}
	}
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
	want, err := probeLibopusCELTMathWords(libopusCELTMathModeBitexactCos, len(inputs), inputs)
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
	for theta := 64; theta <= 8192; theta++ {
		pairs = append(pairs, pair{
			isin: bitexactCos(theta),
			icos: bitexactCos(16384 - theta),
		})
	}
	for theta := 8193; theta <= 16320; theta += 17 {
		pairs = append(pairs, pair{
			isin: bitexactCos(theta),
			icos: bitexactCos(16384 - theta),
		})
	}
	words := make([]uint32, 0, 2*len(pairs))
	for _, p := range pairs {
		words = append(words, uint32(int32(p.isin)), uint32(int32(p.icos)))
	}
	want, err := probeLibopusCELTMathWords(libopusCELTMathModeBitexactLog2Tan, len(pairs), words)
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
	want, err := probeLibopusCELTMathWords(libopusCELTMathModeLog2TanTheta, len(inputs), inputs)
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
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeFracMul16, len(pairs), words)
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
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeISqrt32, len(inputs), inputs)
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
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeUdiv, len(pairs), words)
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
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeSudiv, len(pairs), words)
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
