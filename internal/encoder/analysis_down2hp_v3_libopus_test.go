//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	analysisDown2HPInputMagic  = "GSDI"
	analysisDown2HPOutputMagic = "GSDO"
	analysisDown2HPInputLimit  = 960
)

var analysisDown2HPKernelHelper libopustest.HelperCache
var analysisDown2HPAllocSink float32

type analysisDown2HPCase struct {
	name  string
	state [3]float32
	in    []float32
}

type analysisDown2HPResult struct {
	state    [3]float32
	hpEnergy float32
	out      []float32
}

func requireAnalysisDown2HPV3Target(t *testing.T) {
	t.Helper()
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err == nil && target == "v3" {
		return
	}
	message := "analysis down2hp source oracle requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += fmt.Sprintf(", got %q", target)
	}
	if libopustest.StrictRefRequired() {
		t.Fatal(message)
	}
	t.Skip(message)
}

func buildAnalysisDown2HPKernelHelper() (string, string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	analysisPath := libopustest.RefPath("src", "analysis.c")
	root := filepath.Clean(filepath.Join(filepath.Dir(analysisPath), "..", "..", ".."))
	switch variant {
	case libopustooling.LibopusReferenceScalar:
		if !libopustooling.EnsureLibopusScalar(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected scalar libopus reference at %s", root)
		}
	case libopustooling.LibopusReferenceSIMD:
		if !libopustooling.EnsureLibopusSIMD(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected SIMD libopus reference at %s", root)
		}
	default:
		return "", "", fmt.Errorf("analysis down2hp source oracle selected unsupported reference variant %q", variant)
	}

	analysisSource, err := os.ReadFile(analysisPath)
	if err != nil {
		return "", "", fmt.Errorf("read selected libopus src/analysis.c: %w", err)
	}
	sourceHash := sha256.Sum256(analysisSource)
	sourceHashHex := hex.EncodeToString(sourceHash[:])
	config := libopustest.CHelperConfig{
		Label:      "libopus analysis down2hp source kernel",
		OutputBase: "gopus_libopus_analysis_down2hp_kernel",
		SourceFile: "libopus_analysis_down2hp_kernel.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
			fmt.Sprintf("-DGOPUS_ANALYSIS_SOURCE_SHA256=%q", sourceHashHex),
		},
		// analysis.c is included with angle brackets by the helper. BuildCHelper
		// places these directories under the already selected reference tree.
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	path, err := analysisDown2HPKernelHelper.CHelperPath(config)
	if err != nil {
		return "", "", err
	}
	return path, sourceHashHex, nil
}

func analysisDown2HPCases(t *testing.T) []analysisDown2HPCase {
	t.Helper()
	const (
		frameSamples = analysisDown2HPInputLimit
		channels     = 2
	)
	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1, 48000, frameSamples*channels, channels,
	)
	if err != nil {
		t.Fatalf("generate quantized analysis input: %v", err)
	}
	pcm = quantizeCELTTracePCM(pcm)
	actual := make([]float32, frameSamples)
	for i := range actual {
		// Match opus_encoder.c:downmix_float for c2==-2 with two channels,
		// then analysis.c:downmix_and_resample's HALF32 step.
		value := pcm[2*i]*celtSigScale + pcm[2*i+1]*celtSigScale
		if value < -65536 {
			value = -65536
		}
		if value > 65536 {
			value = 65536
		}
		actual[i] = 0.5 * value
	}

	var synthetic [analysisDown2HPInputLimit]float32
	pattern := [...]float32{-65536, 65536, -0.25, 0.125, 8192, -16384, 1.0 / 32768, -1.0 / 32768}
	for i := range synthetic {
		synthetic[i] = pattern[i%len(pattern)]
	}

	lengths := [...]int{0, 1, 2, 3, 16, 480, 960}
	cases := make([]analysisDown2HPCase, 0, len(lengths)*2)
	for _, n := range lengths {
		cases = append(cases,
			analysisDown2HPCase{name: fmt.Sprintf("quantized-stereo-downmix-%d", n), in: append([]float32(nil), actual[:n]...)},
			analysisDown2HPCase{
				name:  fmt.Sprintf("bounded-synthetic-%d", n),
				state: [3]float32{0.125, -0.25, 0.0625},
				in:    append([]float32(nil), synthetic[:n]...),
			},
		)
	}
	return cases
}

func probeAnalysisDown2HP(cases []analysisDown2HPCase) ([]analysisDown2HPResult, string, error) {
	binPath, sourceHash, err := buildAnalysisDown2HPKernelHelper()
	if err != nil {
		return nil, "", err
	}
	payload := libopustest.NewOraclePayload(analysisDown2HPInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.in)))
		payload.Float32s(tc.state[:]...)
		payload.Float32s(tc.in...)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "analysis down2hp source kernel", analysisDown2HPOutputMagic)
	if err != nil {
		return nil, "", err
	}
	count := reader.Count(len(cases))
	if got := string(reader.Bytes(64)); got != sourceHash {
		return nil, "", fmt.Errorf("included libopus src/analysis.c hash=%s want selected source %s", got, sourceHash)
	}
	results := make([]analysisDown2HPResult, count)
	for i := range results {
		inputLen := int(reader.U32())
		outputLen := int(reader.U32())
		if inputLen != len(cases[i].in) || outputLen != len(cases[i].in)/2 {
			return nil, "", fmt.Errorf("record %d geometry input=%d output=%d want %d/%d", i, inputLen, outputLen, len(cases[i].in), len(cases[i].in)/2)
		}
		for j := range results[i].state {
			results[i].state[j] = reader.Float32()
		}
		results[i].hpEnergy = reader.Float32()
		results[i].out = make([]float32, outputLen)
		for j := range results[i].out {
			results[i].out[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, "", err
	}
	return results, sourceHash, nil
}

func TestAnalysisDown2HPV3MatchesLibopusSourceKernel(t *testing.T) {
	requireAnalysisDown2HPV3Target(t)
	libopustest.RequireOracle(t)
	cases := analysisDown2HPCases(t)
	want, sourceHash, err := probeAnalysisDown2HP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis down2hp source kernel", err)
	}
	for i, tc := range cases {
		state := tc.state
		out := make([]float32, len(tc.in)/2)
		hpEnergy := silkResamplerDown2HP(state[:], out, tc.in)
		for j := range state {
			if gotBits, wantBits := math.Float32bits(state[j]), math.Float32bits(want[i].state[j]); gotBits != wantBits {
				t.Fatalf("%s state[%d]=%08x want=%08x source=%s", tc.name, j, gotBits, wantBits, sourceHash)
			}
		}
		if gotBits, wantBits := math.Float32bits(hpEnergy), math.Float32bits(want[i].hpEnergy); gotBits != wantBits {
			t.Fatalf("%s high-pass energy=%08x want=%08x source=%s", tc.name, gotBits, wantBits, sourceHash)
		}
		for j := range out {
			if gotBits, wantBits := math.Float32bits(out[j]), math.Float32bits(want[i].out[j]); gotBits != wantBits {
				t.Fatalf("%s output[%d]=%08x want=%08x source=%s", tc.name, j, gotBits, wantBits, sourceHash)
			}
		}
	}

	input := cases[len(cases)-2].in
	var state [3]float32
	var out [analysisDown2HPInputLimit / 2]float32
	for range 3 {
		state = [3]float32{}
		analysisDown2HPAllocSink = silkResamplerDown2HP(state[:], out[:], input)
	}
	allocs := testing.AllocsPerRun(100, func() {
		state = [3]float32{}
		analysisDown2HPAllocSink = silkResamplerDown2HP(state[:], out[:], input)
	})
	if allocs != 0 {
		t.Fatalf("silkResamplerDown2HP allocations=%g want 0", allocs)
	}
}
