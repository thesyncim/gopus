//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"math"
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
	libopusSILKFixedVADInputMagic  = "GVDI"
	libopusSILKFixedVADOutputMagic = "GVDO"
)

var (
	libopusSILKFixedVADOnce sync.Once
	libopusSILKFixedVADBin  string
	libopusSILKFixedVADErr  error
)

// buildLibopusSILKFixedVADHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_vad_info.c against it.
func buildLibopusSILKFixedVADHelper() (string, error) {
	libopusSILKFixedVADOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedVADErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedVADErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedVADErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_vad_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedVADErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_vad_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedVADErr = fmt.Errorf("build silk fixed vad helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedVADBin = out
	})
	return libopusSILKFixedVADBin, libopusSILKFixedVADErr
}

type silkFixedVADCase struct {
	name        string
	fsKHz       int
	frameLength int
	frames      [][]int16
}

type silkFixedVADFrameResult struct {
	speechActivityQ8     int32
	inputTiltQ15         int32
	inputQualityBandsQ15 [vadNBands]int32
}

type silkFixedVADResult struct {
	frames         []silkFixedVADFrameResult
	hpState        int32
	counter        int32
	nl             [vadNBands]int32
	invNL          [vadNBands]int32
	nrgRatioSmthQ8 [vadNBands]int32
	xnrgSubfr      [vadNBands]int32
	anaState       [2]int32
	anaState1      [2]int32
	anaState2      [2]int32
}

func probeLibopusSILKFixedVAD(cases []silkFixedVADCase) ([]silkFixedVADResult, error) {
	binPath, err := buildLibopusSILKFixedVADHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedVADInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.fsKHz))
		payload.U32(uint32(tc.frameLength))
		payload.U32(uint32(len(tc.frames)))
		for _, frame := range tc.frames {
			for _, s := range frame {
				payload.I16(s)
			}
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed vad", libopusSILKFixedVADOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([]silkFixedVADResult, cnt)
	for i := range out {
		out[i].frames = make([]silkFixedVADFrameResult, len(cases[i].frames))
		for f := range out[i].frames {
			out[i].frames[f].speechActivityQ8 = reader.I32()
			out[i].frames[f].inputTiltQ15 = reader.I32()
			for b := 0; b < vadNBands; b++ {
				out[i].frames[f].inputQualityBandsQ15[b] = reader.I32()
			}
		}
		out[i].hpState = reader.I32()
		out[i].counter = reader.I32()
		for b := 0; b < vadNBands; b++ {
			out[i].nl[b] = reader.I32()
		}
		for b := 0; b < vadNBands; b++ {
			out[i].invNL[b] = reader.I32()
		}
		for b := 0; b < vadNBands; b++ {
			out[i].nrgRatioSmthQ8[b] = reader.I32()
		}
		for b := 0; b < vadNBands; b++ {
			out[i].xnrgSubfr[b] = reader.I32()
		}
		out[i].anaState[0] = reader.I32()
		out[i].anaState[1] = reader.I32()
		out[i].anaState1[0] = reader.I32()
		out[i].anaState1[1] = reader.I32()
		out[i].anaState2[0] = reader.I32()
		out[i].anaState2[1] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// genFrames builds numFrames frames of frameLength samples each from a
// generator function indexed by absolute sample position.
func genFrames(numFrames, frameLength int, gen func(pos int) int16) [][]int16 {
	frames := make([][]int16, numFrames)
	pos := 0
	for f := range frames {
		frame := make([]int16, frameLength)
		for i := range frame {
			frame[i] = gen(pos)
			pos++
		}
		frames[f] = frame
	}
	return frames
}

func clampI16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func TestSILKVADGetSAQ8FixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5ad8))

	var cases []silkFixedVADCase

	// frame_length is 10ms or 20ms at the internal fs. The encoder uses
	// 8/12/16 kHz internal sampling.
	for _, fsKHz := range []int{8, 12, 16} {
		for _, ms := range []int{10, 20} {
			frameLength := ms * fsKHz
			nf := 6

			// Silence.
			cases = append(cases, silkFixedVADCase{
				name:        fmt.Sprintf("silence_fs%d_%dms", fsKHz, ms),
				fsKHz:       fsKHz,
				frameLength: frameLength,
				frames: genFrames(nf, frameLength, func(pos int) int16 {
					return 0
				}),
			})

			// White noise (active, noise-like).
			noiseRng := rand.New(rand.NewSource(int64(0xa11ce + fsKHz*100 + ms)))
			cases = append(cases, silkFixedVADCase{
				name:        fmt.Sprintf("noise_fs%d_%dms", fsKHz, ms),
				fsKHz:       fsKHz,
				frameLength: frameLength,
				frames: genFrames(nf, frameLength, func(pos int) int16 {
					return int16(noiseRng.Intn(8001) - 4000)
				}),
			})

			// Voiced-like: low-frequency sinusoid (~200 Hz fundamental) with a
			// formant overtone, ramping amplitude across frames.
			fs := float64(fsKHz) * 1000.0
			cases = append(cases, silkFixedVADCase{
				name:        fmt.Sprintf("speech_fs%d_%dms", fsKHz, ms),
				fsKHz:       fsKHz,
				frameLength: frameLength,
				frames: genFrames(nf, frameLength, func(pos int) int16 {
					t := float64(pos) / fs
					amp := 6000.0 * (0.4 + 0.6*float64(pos)/float64(frameLength*nf))
					v := amp*math.Sin(2*math.Pi*200*t) + 0.4*amp*math.Sin(2*math.Pi*900*t)
					return clampI16(v)
				}),
			})

			// High-frequency tone near band edge (tilt stress).
			cases = append(cases, silkFixedVADCase{
				name:        fmt.Sprintf("hf_fs%d_%dms", fsKHz, ms),
				fsKHz:       fsKHz,
				frameLength: frameLength,
				frames: genFrames(nf, frameLength, func(pos int) int16 {
					t := float64(pos) / fs
					return clampI16(8000 * math.Sin(2*math.Pi*3200*t))
				}),
			})

			// Silence then a sudden loud burst, to exercise the noise-estimate
			// adaptation and saturation paths.
			cases = append(cases, silkFixedVADCase{
				name:        fmt.Sprintf("burst_fs%d_%dms", fsKHz, ms),
				fsKHz:       fsKHz,
				frameLength: frameLength,
				frames: genFrames(8, frameLength, func(pos int) int16 {
					frame := pos / frameLength
					if frame < 3 {
						return int16(rng.Intn(41) - 20)
					}
					t := float64(pos) / fs
					return clampI16(30000 * math.Sin(2*math.Pi*150*t))
				}),
			})

			// Full-scale random (stress saturation in energy accumulation).
			fsRng := rand.New(rand.NewSource(int64(0xf5e + fsKHz*7 + ms)))
			cases = append(cases, silkFixedVADCase{
				name:        fmt.Sprintf("fullscale_fs%d_%dms", fsKHz, ms),
				fsKHz:       fsKHz,
				frameLength: frameLength,
				frames: genFrames(nf, frameLength, func(pos int) int16 {
					return int16(fsRng.Intn(65536) - 32768)
				}),
			})
		}
	}

	want, err := probeLibopusSILKFixedVAD(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed vad", err)
		return
	}

	for i, tc := range cases {
		var state silkVADState
		silkVADInit(&state)

		fail := func(field string, got, exp interface{}) {
			t.Fatalf("case %d (%s fs=%d len=%d): %s=%v want %v",
				i, tc.name, tc.fsKHz, tc.frameLength, field, got, exp)
		}

		sc := &silkFixedEncodeScratch{}
		for f, frame := range tc.frames {
			res := silkVADGetSAQ8(sc, &state, frame, tc.frameLength, tc.fsKHz)
			w := want[i].frames[f]
			if res.speechActivityQ8 != w.speechActivityQ8 {
				fail(fmt.Sprintf("frame %d speech_activity_Q8", f), res.speechActivityQ8, w.speechActivityQ8)
			}
			if res.inputTiltQ15 != w.inputTiltQ15 {
				fail(fmt.Sprintf("frame %d input_tilt_Q15", f), res.inputTiltQ15, w.inputTiltQ15)
			}
			for b := 0; b < vadNBands; b++ {
				if res.inputQualityBandsQ15[b] != w.inputQualityBandsQ15[b] {
					fail(fmt.Sprintf("frame %d input_quality_bands_Q15[%d]", f, b),
						res.inputQualityBandsQ15[b], w.inputQualityBandsQ15[b])
				}
			}
		}

		// Verify the cross-frame VAD state matches at the end of the sequence.
		if int32(state.HPstate) != want[i].hpState {
			fail("HPstate", state.HPstate, want[i].hpState)
		}
		if state.counter != want[i].counter {
			fail("counter", state.counter, want[i].counter)
		}
		for b := 0; b < vadNBands; b++ {
			if state.NL[b] != want[i].nl[b] {
				fail(fmt.Sprintf("NL[%d]", b), state.NL[b], want[i].nl[b])
			}
			if state.invNL[b] != want[i].invNL[b] {
				fail(fmt.Sprintf("inv_NL[%d]", b), state.invNL[b], want[i].invNL[b])
			}
			if state.NrgRatioSmthQ8[b] != want[i].nrgRatioSmthQ8[b] {
				fail(fmt.Sprintf("NrgRatioSmth_Q8[%d]", b), state.NrgRatioSmthQ8[b], want[i].nrgRatioSmthQ8[b])
			}
			if state.XnrgSubfr[b] != want[i].xnrgSubfr[b] {
				fail(fmt.Sprintf("XnrgSubfr[%d]", b), state.XnrgSubfr[b], want[i].xnrgSubfr[b])
			}
		}
		if state.AnaState[0] != want[i].anaState[0] || state.AnaState[1] != want[i].anaState[1] {
			fail("AnaState", state.AnaState, want[i].anaState)
		}
		if state.AnaState1[0] != want[i].anaState1[0] || state.AnaState1[1] != want[i].anaState1[1] {
			fail("AnaState1", state.AnaState1, want[i].anaState1)
		}
		if state.AnaState2[0] != want[i].anaState2[0] || state.AnaState2[1] != want[i].anaState2[1] {
			fail("AnaState2", state.AnaState2, want[i].anaState2)
		}
	}
}
