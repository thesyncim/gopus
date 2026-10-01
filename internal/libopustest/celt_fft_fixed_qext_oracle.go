//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type CELTFixedQEXTFFTParams struct {
	Mode  int
	Input []int32 // interleaved real and imaginary values
}

var celtFixedQEXTFFTHelper HelperCache

func buildCELTFixedQEXTFFTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT FFT",
		OutputBase:   "gopus_libopus_celt_fft_fixed_qext",
		SourceFile:   "libopus_celt_fft_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func ProbeCELTFixedQEXTFFT(p CELTFixedQEXTFFTParams) ([]int32, error) {
	n := 0
	switch p.Mode {
	case 0:
		n = 480
	case 1:
		n = 960
	default:
		return nil, fmt.Errorf("invalid fixed-QEXT FFT mode %d", p.Mode)
	}
	if len(p.Input) != 2*n {
		return nil, fmt.Errorf("invalid fixed-QEXT FFT input length %d, want %d", len(p.Input), 2*n)
	}
	payload := NewOraclePayloadVersion("GQFI", 1, uint32(p.Mode))
	payload.I32s(p.Input...)
	bin, err := celtFixedQEXTFFTHelper.Path(buildCELTFixedQEXTFFTHelper)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT FFT", "GQFO")
	if err != nil {
		return nil, err
	}
	count := int(reader.U32())
	if count != n {
		return nil, fmt.Errorf("fixed-QEXT FFT output count %d, want %d", count, n)
	}
	out := make([]int32, 2*n)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
