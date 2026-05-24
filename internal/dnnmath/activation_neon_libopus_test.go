package dnnmath

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDNNActivationInputMagic  = "GDAI"
	libopusDNNActivationOutputMagic = "GDAO"

	libopusDNNActivationSigmoid = uint32(0)
	libopusDNNActivationTanh    = uint32(1)
	libopusDNNActivationExp     = uint32(2)
)

var libopusDNNActivationHelper libopustest.HelperCache

func getLibopusDNNActivationHelperPath() (string, error) {
	return libopusDNNActivationHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "dnn activation",
		OutputBase:  "gopus_libopus_dnn_activation",
		SourceFile:  "libopus_dnn_activation_info.c",
		RefIncludes: []string{"celt", "celt/x86", "dnn"},
		Libs:        []string{"-lm"},
	})
}

func probeLibopusDNNActivation(mode uint32, input []float32) ([]float32, error) {
	binPath, err := getLibopusDNNActivationHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusDNNActivationInputMagic, mode, uint32(len(input)))
	payload.Float32s(input...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "dnn activation", libopusDNNActivationOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(input))
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

func TestNEONVectorActivationsMatchLibopusOracle(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("NEON activation path is arm64-only")
	}
	libopustest.RequireOracle(t)

	input := []float32{-12, -8, -2, -0.75, -0.125, 0, 0.125, 0.75, 2, 8, 12}
	tests := []struct {
		name string
		mode uint32
		run  func([]float32, []float32, int)
	}{
		{name: "sigmoid", mode: libopusDNNActivationSigmoid, run: SigmoidVectorApprox},
		{name: "tanh", mode: libopusDNNActivationTanh, run: TanhVectorApprox},
		{name: "exp", mode: libopusDNNActivationExp, run: ExpVectorApprox},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDNNActivation(tc.mode, input)
			if err != nil {
				libopustest.HelperUnavailable(t, "dnn activation", err)
			}
			got := make([]float32, len(input))
			tc.run(got, input, len(input))
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("%s[%d]=%s want %s", tc.name, i, formatDNNFloat(got[i]), formatDNNFloat(want[i]))
				}
			}
		})
	}
}

func TestCgemv8x4ScalarQuantizeInputMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	weights := make([]byte, 8*8)
	weights[0] = byte(int8(1))
	scale := make([]float32, 8)
	scale[0] = 1
	cases := []float32{
		-1,
		float32(-2.5 / 127),
		0,
		float32(2.5 / 127),
		math.Float32frombits(0x3e83060c),
		1,
	}
	for _, x := range cases {
		input := make([]float32, 8)
		input[0] = x
		want, err := libopustest.ProbeDNNKernelScalarCGEMV8x4(8, 8, weights, scale, input)
		if err != nil {
			libopustest.HelperUnavailable(t, "scalar dnn cgemv8x4", err)
		}
		got := float32(Cgemv8x4QuantizeInputScalar(x))
		if math.Float32bits(got) != math.Float32bits(want[0]) {
			t.Fatalf("scalar quantize(%g)=%s want %s", x, formatDNNFloat(got), formatDNNFloat(want[0]))
		}
	}
}

func formatDNNFloat(v float32) string {
	return fmt.Sprintf("0x%08x(%0.10g)", math.Float32bits(v), v)
}
