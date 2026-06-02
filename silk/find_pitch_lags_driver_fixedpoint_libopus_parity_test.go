//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusSILKFixedFindPitchLagsInputMagic  = "GFPI"
	libopusSILKFixedFindPitchLagsOutputMagic = "GFPO"
)

var (
	libopusSILKFixedFindPitchLagsOnce sync.Once
	libopusSILKFixedFindPitchLagsBin  string
	libopusSILKFixedFindPitchLagsErr  error
)

// buildLibopusSILKFixedFindPitchLagsHelper ensures the FIXED_POINT libopus
// reference exists, then compiles
// tools/csrc/libopus_silk_fixed_find_pitch_lags_info.c against it.
func buildLibopusSILKFixedFindPitchLagsHelper() (string, error) {
	libopusSILKFixedFindPitchLagsOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedFindPitchLagsErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedFindPitchLagsErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedFindPitchLagsErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_find_pitch_lags_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedFindPitchLagsErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_find_pitch_lags_%s_%s", runtime.GOOS, runtime.GOARCH))

		args := []string{
			"-std=c99", "-O2", "-DHAVE_CONFIG_H",
			"-I", refDir,
			"-I", filepath.Join(refDir, "include"),
			"-I", filepath.Join(refDir, "celt"),
			"-I", filepath.Join(refDir, "silk"),
			"-I", filepath.Join(refDir, "silk", "fixed"),
			src, staticLib, "-lm",
			"-o", out,
		}
		cmd := exec.Command(cc, args...)
		if combined, cerr := cmd.CombinedOutput(); cerr != nil {
			libopusSILKFixedFindPitchLagsErr = fmt.Errorf("build silk fixed find_pitch_lags helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedFindPitchLagsBin = out
	})
	return libopusSILKFixedFindPitchLagsBin, libopusSILKFixedFindPitchLagsErr
}

func probeLibopusSILKFixedFindPitchLags(cases []silkFindPitchLagsInput) ([]silkFindPitchLagsResult, error) {
	binPath, err := buildLibopusSILKFixedFindPitchLagsHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindPitchLagsInputMagic, uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		payload.I32(int32(tc.laPitch))
		payload.I32(int32(tc.frameLength))
		payload.I32(int32(tc.ltpMemLength))
		payload.I32(int32(tc.pitchLPCWinLength))
		payload.I32(int32(tc.pitchEstimationLPCOrder))
		payload.U32(uint32(len(tc.x)))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find_pitch_lags", libopusSILKFixedFindPitchLagsOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFindPitchLagsResult, count)
	for i := range out {
		n := int(reader.U32())
		res := make([]int16, n)
		for j := 0; j < n; j++ {
			res[j] = int16(reader.I32())
		}
		out[i].res = res
		out[i].predGainQ16 = reader.I32()
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindPitchLagsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xF17))

	// randSignal generates a low-frequency-correlated int16 signal so the
	// whitening analysis produces meaningful LPC structure.
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		var acc int32
		for i := range x {
			acc += rng.Int31n(2*amp+1) - amp
			if acc > 32767 {
				acc = 32767
			} else if acc < -32768 {
				acc = -32768
			}
			x[i] = int16(acc >> 4)
		}
		return x
	}

	// makeCase mirrors the SILK encoder buffer-length setup for a given Fs and
	// subframe count. Values follow silk_init_encoder / silk_control_encoder:
	//   la_pitch          = LA_PITCH_MS * fs_kHz (LA_PITCH_MS = 2 -> 2*fs_kHz)
	//   frame_length      = nb_subfr * 5 * fs_kHz
	//   ltp_mem_length    = LTP_MEM_LENGTH_MS * fs_kHz (20 ms -> 20*fs_kHz)
	//   pitch_LPC_win_length = (FIND_PITCH_LPC_WIN_MS) * fs_kHz, clamped so
	//   buf_len >= pitch_LPC_win_length (we use a value that satisfies the
	//   safety celt_assert).
	makeCase := func(fsKHz, nbSubfr, order int, winMs int, amp int32) silkFindPitchLagsInput {
		laPitch := 2 * fsKHz
		frameLength := nbSubfr * 5 * fsKHz
		ltpMemLength := 20 * fsKHz
		bufLen := laPitch + frameLength + ltpMemLength
		pitchLPCWin := winMs * fsKHz
		if pitchLPCWin > bufLen {
			pitchLPCWin = bufLen
		}
		// pitch_LPC_win_length must be a multiple of 4 and leave room for the
		// sine window (>= 2*la_pitch); round down to a multiple of 4.
		pitchLPCWin -= pitchLPCWin % 4
		return silkFindPitchLagsInput{
			laPitch:                 laPitch,
			frameLength:             frameLength,
			ltpMemLength:            ltpMemLength,
			pitchLPCWinLength:       pitchLPCWin,
			pitchEstimationLPCOrder: order,
			x:                       randSignal(bufLen, amp),
		}
	}

	var cases []silkFindPitchLagsInput
	for _, fs := range []int{8, 12, 16} {
		for _, nb := range []int{2, 4} {
			for _, order := range []int{8, 12, 16} {
				// FIND_PITCH_LPC_WIN_MS variants: 20 ms and 24 ms style windows.
				for _, winMs := range []int{20, 24} {
					for _, amp := range []int32{200, 4000, 30000} {
						cases = append(cases, makeCase(fs, nb, order, winMs, amp))
					}
				}
			}
		}
	}

	want, err := probeLibopusSILKFixedFindPitchLags(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find_pitch_lags", err)
		return
	}

	sc := &silkFixedEncodeScratch{}
	for i := range cases {
		in := cases[i]
		got := silkFindPitchLagsFIXFrontEnd(sc, &in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (fs unknown, nb len=%d order=%d win=%d): %s got %v want %v",
				i, len(cases[i].x), cases[i].pitchEstimationLPCOrder, cases[i].pitchLPCWinLength, field, g, e)
		}

		if got.predGainQ16 != w.predGainQ16 {
			fail("predGain_Q16", got.predGainQ16, w.predGainQ16)
		}
		if len(got.res) != len(w.res) {
			fail("len(res)", len(got.res), len(w.res))
		}
		for j := range w.res {
			if got.res[j] != w.res[j] {
				fail(fmt.Sprintf("res[%d]", j), got.res[j], w.res[j])
				break
			}
		}
	}
}
