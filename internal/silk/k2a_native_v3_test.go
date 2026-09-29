//go:build amd64.v3 && !gopus_fixed_point

package silk

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusSILKK2AInputMagic  = "GSKI"
	libopusSILKK2AOutputMagic = "GSKO"
)

var libopusSILKK2AHelper libopustest.HelperCache

func requireLibopusAMD64V3Target(t *testing.T) {
	t.Helper()
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err != nil || target != "v3" {
		message := "native v3 SILK oracle requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
		if err != nil {
			message += ": " + err.Error()
		} else {
			message += ", got " + target
		}
		if libopustest.StrictRefRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}
}

func getLibopusSILKK2AHelperPath() (string, error) {
	return libopusSILKK2AHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "silk k2a",
		OutputBase:  "gopus_libopus_silk_k2a",
		SourceFile:  "libopus_silk_k2a_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

type libopusSILKK2ACase struct {
	order int
	rc    []float32
}

func probeLibopusSILKK2A(cases []libopusSILKK2ACase) ([][]float32, error) {
	binPath, err := getLibopusSILKK2AHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKK2AInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.order))
		payload.Float32s(tc.rc...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk k2a", libopusSILKK2AOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		order := int(reader.U32())
		if order != cases[i].order {
			return nil, fmt.Errorf("record %d helper order=%d want %d", i, order, cases[i].order)
		}
		out[i] = make([]float32, order)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func nativeV3K2ACases() []libopusSILKK2ACase {
	orders := [...]int{12, 14, 16, 20, 24}
	cases := make([]libopusSILKK2ACase, 0, len(orders)*132)
	for _, order := range orders {
		for pattern := range 4 {
			rc := make([]float32, order)
			for i := range rc {
				switch pattern {
				case 0:
					rc[i] = 0
				case 1:
					rc[i] = [...]float32{-0.75, 0.75}[i&1]
				case 2:
					rc[i] = [...]float32{-0.9375, 0.8125, -0.625, 0.5}[i&3]
				case 3:
					rc[i] = [...]float32{-0.99, 0.99, -0.875, 0.875}[i&3]
				}
			}
			cases = append(cases, libopusSILKK2ACase{order: order, rc: rc})
		}
	}

	rng := rand.New(rand.NewSource(0x2d25f43a6178c90b))
	for _, order := range orders {
		for range 128 {
			rc := make([]float32, order)
			for i := range rc {
				rc[i] = float32(rng.Intn(1999)-999) / 1000
			}
			cases = append(cases, libopusSILKK2ACase{order: order, rc: rc})
		}
	}
	return cases
}

// TestSILKK2ANativeV3MatchesLinkedLibopus compares the same source-valid
// reflection coefficients against the paired native v3 libopus archive.
func TestSILKK2ANativeV3MatchesLinkedLibopus(t *testing.T) {
	requireLibopusAMD64V3Target(t)
	libopustest.RequireOracle(t)
	cases := nativeV3K2ACases()
	want, err := probeLibopusSILKK2A(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "native v3 silk k2a", err)
	}
	for i, tc := range cases {
		got := make([]float32, tc.order)
		k2aF32(got, tc.rc, tc.order)
		for j := range got {
			if gotBits, wantBits := math.Float32bits(got[j]), math.Float32bits(want[i][j]); gotBits != wantBits {
				t.Fatalf("case=%d order=%d rc[%d]=%08x A[%d]=%08x %.10g want=%08x %.10g",
					i, tc.order, j, math.Float32bits(tc.rc[j]), j,
					gotBits, got[j], wantBits, want[i][j])
			}
		}
	}
}
