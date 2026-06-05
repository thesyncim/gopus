//go:build gopus_fixed_point

package silk

import (
	"math/rand"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusSILKFixedLPCInputMagic  = "GSFI"
	libopusSILKFixedLPCOutputMagic = "GSFO"
)

// fixedRefPath returns a path under the pinned --enable-fixed-point libopus
// reference tree (tmp_check/opus-<version>-fixed). The SILK fixed-point oracle
// is built and linked against this FIXED_POINT libopus, independent of the
// shared float/qext reference machinery.
func fixedRefPath(elem ...string) string {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	base := []string{repoRoot, "tmp_check", "opus-" + libopustooling.DefaultVersion + "-fixed"}
	return filepath.Join(append(base, elem...)...)
}

type silkFixedLPCAnalysisCase struct {
	name string
	d    int
	b    []int16
	in   []int16
}

func probeLibopusSILKFixedLPCAnalysis(cases []silkFixedLPCAnalysisCase) ([][]int16, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_lpc_analysis_info.c", "lpc")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedLPCInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.d))
		payload.U32(uint32(len(tc.in)))
		for _, v := range tc.b {
			payload.I16(v)
		}
		for _, v := range tc.in {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed lpc analysis", libopusSILKFixedLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]int16, count)
	for i := range out {
		n := len(cases[i].in)
		out[i] = make([]int16, n)
		for j := 0; j < n; j++ {
			out[i][j] = reader.I16()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKLPCAnalysisFilterFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5117))

	// Realistic monic whitening filter coefficients (Q12) drift around small
	// values; libopus expects |B| well below 4096. Generate orders 6..16 even.
	randCoefs := func(d int, scale int32) []int16 {
		b := make([]int16, d)
		for i := range b {
			b[i] = int16(rng.Int31n(2*scale+1) - scale)
		}
		return b
	}
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	var cases []silkFixedLPCAnalysisCase

	for _, d := range []int{6, 8, 10, 12, 16} {
		for _, length := range []int{d, d + 1, 40, 240, 1024} {
			cases = append(cases, silkFixedLPCAnalysisCase{
				name: "rand",
				d:    d,
				b:    randCoefs(d, 600),
				in:   randSignal(length, 12000),
			})
		}
	}

	// Edge cases: zero coefficients, full-scale signal (saturation stress),
	// and coefficients near the stability limit to exercise wrap-around.
	zeros := make([]int16, 10)
	cases = append(cases, silkFixedLPCAnalysisCase{
		name: "zero-coefs",
		d:    10,
		b:    zeros,
		in:   randSignal(240, 32767),
	})

	fullScale := make([]int16, 240)
	for i := range fullScale {
		if i%2 == 0 {
			fullScale[i] = 32767
		} else {
			fullScale[i] = -32768
		}
	}
	bigB := make([]int16, 16)
	for i := range bigB {
		bigB[i] = 32767
	}
	cases = append(cases, silkFixedLPCAnalysisCase{
		name: "saturation-wrap",
		d:    16,
		b:    bigB,
		in:   fullScale,
	})

	// Many random orders to broaden coverage.
	for i := 0; i < 64; i++ {
		d := 6 + 2*rng.Intn(6) // 6,8,...,16
		length := d + rng.Intn(300)
		cases = append(cases, silkFixedLPCAnalysisCase{
			name: "rand-bulk",
			d:    d,
			b:    randCoefs(d, int32(1+rng.Intn(2000))),
			in:   randSignal(length, int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedLPCAnalysis(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed lpc analysis", err)
		return
	}

	for i, tc := range cases {
		got := make([]int16, len(tc.in))
		silkLPCAnalysisFilterFixed(got, tc.in, tc.b, len(tc.in), tc.d)
		for j := range got {
			if got[j] != want[i][j] {
				t.Fatalf("case %d (%s d=%d len=%d): out[%d]=%d want %d",
					i, tc.name, tc.d, len(tc.in), j, got[j], want[i][j])
			}
		}
	}
}
