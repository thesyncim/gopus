package libopustest

import (
	"strings"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	DNNKernelSGEMV    = uint32(0)
	DNNKernelCGEMV8x4 = uint32(1)

	dnnKernelInputMagic  = "GDKI"
	dnnKernelOutputMagic = "GDKO"
)

var dnnKernelHelper HelperCache
var dnnKernelScalarHelper HelperCache

func dnnKernelHelperPath() (string, error) {
	return dnnKernelHelper.CHelperPath(CHelperConfig{
		Label:       "dnn kernel",
		OutputBase:  "gopus_libopus_dnn_kernel",
		SourceFile:  "libopus_dnn_kernel_info.c",
		RefIncludes: []string{"celt", "celt/x86", "dnn"},
		Libs:        []string{"-lm"},
	})
}

func dnnKernelScalarHelperPath() (string, error) {
	return dnnKernelScalarHelper.CHelperPath(CHelperConfig{
		Label:       "scalar dnn kernel",
		OutputBase:  "gopus_libopus_dnn_kernel_scalar",
		SourceFile:  "libopus_dnn_kernel_info.c",
		RefIncludes: []string{"celt", "celt/x86", "dnn"},
		CFlags:      strings.Fields(libopustooling.OSCEScalarDNNBuildCFLAGS),
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
	return readDNNKernelOracle(binPath, payload.Bytes(), rows)
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
	return readDNNKernelOracle(binPath, payload.Bytes(), rows)
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
	return readDNNKernelOracle(binPath, payload.Bytes(), rows)
}

func readDNNKernelOracle(binPath string, input []byte, rows int) ([]float32, error) {
	reader, err := RunOracle(binPath, input, "dnn kernel", dnnKernelOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(rows)
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
