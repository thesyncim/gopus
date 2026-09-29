//go:build amd64.v3 && !gopus_fixed_point

package silk

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKWarpedGainInputMagic  = "GSWI"
	libopusSILKWarpedGainOutputMagic = "GSWO"
)

var libopusSILKWarpedGainHelper libopustest.HelperCache

type libopusSILKWarpedGainCase struct {
	order  int
	lambda float32
	coefs  []float32
}

func getLibopusSILKWarpedGainHelperPath(t testing.TB) (string, error) {
	pinnedSource := libopustest.ReadPinnedSourceFileOrSkip(t, "SILK warped gain source",
		"silk", "float", "noise_shape_analysis_FLP.c")
	sourceHash := sha256.Sum256(pinnedSource)
	return libopusSILKWarpedGainHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "SILK warped gain",
		OutputBase:   "gopus_libopus_silk_warped_gain",
		SourceFile:   "libopus_silk_warped_gain_info.c",
		ProbeRelPath: "silk/float/noise_shape_analysis_FLP.c",
		// BuildCHelper includes CFlags in its cache digest, so the included pinned
		// translation unit's source hash invalidates a stale helper binary.
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG", fmt.Sprintf("-DGOPUS_SILK_WARPED_GAIN_SOURCE_SHA256=0x%x", sourceHash)},
		RefIncludes: []string{"celt", "silk", "silk/float"},
		SIMDRef:     silkLPCOracleUsesAVX2(),
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

func probeLibopusSILKWarpedGain(t testing.TB, cases []libopusSILKWarpedGainCase) ([]float32, error) {
	path, err := getLibopusSILKWarpedGainHelperPath(t)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKWarpedGainInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.order))
		payload.Float32(tc.lambda)
		payload.Float32s(tc.coefs...)
	}
	reader, err := libopustest.RunOracle(path, payload.Bytes(), "SILK warped gain", libopusSILKWarpedGainOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]float32, count)
	for i := range out {
		order := int(reader.U32())
		if order != cases[i].order {
			return nil, fmt.Errorf("record %d helper order=%d want %d", i, order, cases[i].order)
		}
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func nativeSILKWarpedGainCases() []libopusSILKWarpedGainCase {
	orders := [...]int{12, 14, 16, 20, 24}
	warps := [...]float32{0, 0.01, 0.125, 0.3, 0.5, 0.51}
	cases := make([]libopusSILKWarpedGainCase, 0, len(orders)*len(warps)*3+128)
	for _, order := range orders {
		for pattern := range 3 {
			rc := make([]float32, order)
			for i := range rc {
				switch pattern {
				case 0:
					rc[i] = 0
				case 1:
					rc[i] = [...]float32{-0.75, 0.75}[i&1]
				case 2:
					rc[i] = [...]float32{-0.9, 0.8, -0.65, 0.5}[i&3]
				}
			}
			coefs := make([]float32, order)
			k2aF32(coefs, rc, order)
			for _, lambda := range warps {
				cases = append(cases, libopusSILKWarpedGainCase{order: order, lambda: lambda, coefs: append([]float32(nil), coefs...)})
			}
		}
	}

	rng := rand.New(rand.NewSource(0x5741525045444741))
	for i := 0; i < 128; i++ {
		order := orders[i%len(orders)]
		rc := make([]float32, order)
		for j := range rc {
			rc[j] = float32(rng.Intn(1901)-950) / 1000
		}
		coefs := make([]float32, order)
		k2aF32(coefs, rc, order)
		lambda := float32(rng.Intn(5101)) / 10000
		cases = append(cases, libopusSILKWarpedGainCase{order: order, lambda: lambda, coefs: coefs})
	}
	return cases
}

//go:noinline
func warpedGainCandidateForOracle(coefs []float32, lambda float32, order int) float32 {
	return warpedGainF32(coefs, lambda, order)
}

func separateWarpedGainWitness(coefs []float32, lambda float32, order int) float32 {
	lambda = -lambda
	gain := coefs[order-1]
	for i := order - 2; i >= 0; i-- {
		gain = noFMA32(lambda, gain) + coefs[i]
	}
	return 1.0 / (1.0 - noFMA32(lambda, gain))
}

func TestSILKWarpedGainNativeV3MatchesLibopusSource(t *testing.T) {
	requireLibopusAMD64V3Target(t)
	libopustest.RequireOracle(t)
	cases := nativeSILKWarpedGainCases()
	want, err := probeLibopusSILKWarpedGain(t, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "native v3 SILK warped gain", err)
	}
	separateWitness := false
	for i, tc := range cases {
		got := warpedGainCandidateForOracle(tc.coefs, tc.lambda, tc.order)
		if gotBits, wantBits := math.Float32bits(got), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("case=%d order=%d lambda=%08x warped gain=%08x source=%08x", i, tc.order,
				math.Float32bits(tc.lambda), gotBits, wantBits)
		}
		if math.Float32bits(separateWarpedGainWitness(tc.coefs, tc.lambda, tc.order)) != math.Float32bits(want[i]) {
			separateWitness = true
		}
	}
	if !separateWitness {
		t.Fatal("oracle corpus did not distinguish the separate multiply/add model from libopus source")
	}
}

func TestSILKWarpedGainF32ZeroAlloc(t *testing.T) {
	var coefs [maxShapeLpcOrder]float32
	for i := range coefs {
		coefs[i] = float32(i+1) * 0.0007
	}
	var sink float32
	for range 4 {
		sink = warpedGainF32(coefs[:24], 0.37, 24)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		sink = warpedGainF32(coefs[:24], 0.37, 24)
	}); allocs != 0 {
		t.Fatalf("warped gain allocated %v times", allocs)
	}
	if math.Float32bits(sink) == 0 {
		t.Fatal("warped gain result is zero")
	}
}
