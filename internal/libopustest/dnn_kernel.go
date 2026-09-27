package libopustest

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	DNNKernelSGEMV          = uint32(0)
	DNNKernelCGEMV8x4       = uint32(1)
	DNNKernelLinearCGEMV8x4 = uint32(2)

	dnnKernelInputMagic  = "GDKI"
	dnnKernelOutputMagic = "GDKO"
)

var dnnKernelHelper HelperCache
var dnnKernelScalarHelper HelperCache

func dnnKernelHelperPath() (string, error) {
	_, source, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	return dnnKernelHelper.Path(func() (string, error) {
		return BuildDREDHelper(repoRoot, "libopus_dnn_kernel_info.c", "gopus_libopus_dnn_kernel", true)
	})
}

func dnnKernelScalarHelperPath() (string, error) {
	return dnnKernelScalarHelper.CHelperPath(CHelperConfig{
		Label:       "scalar dnn kernel",
		OutputBase:  "gopus_libopus_dnn_kernel_scalar",
		SourceFile:  "libopus_dnn_kernel_info.c",
		RefIncludes: []string{"celt", "celt/x86", "dnn"},
		CFlags:      append(strings.Fields(libopustooling.OSCEScalarDNNBuildCFLAGS), "-DGOPUS_DIRECT_SCALAR_DNN"),
		Libs:        []string{"-lm"},
	})
}

func ProbeDNNKernelSGEMV(rows, cols, colStride int, weights, x []float32) ([]float32, error) {
	binPath, err := dnnKernelHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(dnnKernelInputMagic, DNNKernelSGEMV, uint32(rows), uint32(cols), uint32(colStride))
	payload.Float32s(weights...)
	payload.Float32s(x...)
	return readDNNKernelOracle(binPath, payload.Bytes(), rows, true)
}

func ProbeDNNKernelCGEMV8x4(rows, cols int, weights []byte, scale, x []float32) ([]float32, error) {
	binPath, err := dnnKernelHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(dnnKernelInputMagic, DNNKernelCGEMV8x4, uint32(rows), uint32(cols), 0)
	payload.Raw(weights)
	payload.Float32s(scale...)
	payload.Float32s(x...)
	return readDNNKernelOracle(binPath, payload.Bytes(), rows, true)
}

// ProbeDNNLinearCGEMV8x4 calls the selected DRED archive's compute_linear,
// including its integer-matrix bias selection and optional sparse weights.
func ProbeDNNLinearCGEMV8x4(rows, cols int, idx []int32, weights []byte, scale, x, bias, subias []float32) ([]float32, error) {
	binPath, err := dnnKernelHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(dnnKernelInputMagic, DNNKernelLinearCGEMV8x4, uint32(rows), uint32(cols), uint32(len(idx)))
	payload.I32s(idx...)
	payload.Raw(weights)
	payload.Float32s(scale...)
	payload.Float32s(x...)
	payload.Float32s(bias...)
	payload.Float32s(subias...)
	return readDNNKernelOracle(binPath, payload.Bytes(), rows, true)
}

func ProbeDNNKernelScalarCGEMV8x4(rows, cols int, weights []byte, scale, x []float32) ([]float32, error) {
	binPath, err := dnnKernelScalarHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(dnnKernelInputMagic, DNNKernelCGEMV8x4, uint32(rows), uint32(cols), 0)
	payload.Raw(weights)
	payload.Float32s(scale...)
	payload.Float32s(x...)
	return readDNNKernelOracle(binPath, payload.Bytes(), rows, false)
}

// ValidateDNNDispatchArch verifies that the selected x86 SIMD helper executes
// libopus's AVX2 DNN table slot. Other targets use their selected config and
// may map the architecture number differently.
func ValidateDNNDispatchArch(arch uint32) error {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return err
	}
	if variant == libopustooling.LibopusReferenceScalar && arch != 0 {
		return fmt.Errorf("scalar DNN oracle selected arch=%d, want 0", arch)
	}
	if runtime.GOARCH == "amd64" && variant == libopustooling.LibopusReferenceSIMD && arch != 4 {
		return fmt.Errorf("amd64 SIMD DNN oracle selected arch=%d, want AVX2 arch 4", arch)
	}
	return nil
}

func readDNNKernelOracle(binPath string, input []byte, rows int, selected bool) ([]float32, error) {
	reader, err := RunOracle(binPath, input, "dnn kernel", dnnKernelOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(rows)
	arch := reader.U32()
	if selected {
		if err := ValidateDNNDispatchArch(arch); err != nil {
			return nil, err
		}
	} else if arch != 0 {
		return nil, fmt.Errorf("direct scalar DNN oracle selected arch=%d, want 0", arch)
	}
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
