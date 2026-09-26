package libopustest

import (
	"fmt"
	"math"
	"runtime"
)

const (
	celtPVQFloatSSE2InputMagic  = "GPS2"
	celtPVQFloatSSE2OutputMagic = "GPSO"
)

var celtPVQFloatSSE2Helper HelperCache

func buildCELTPVQFloatSSE2Helper() (string, error) {
	cflags := []string{"-DVAR_ARRAYS", "-DOPUS_X86_MAY_HAVE_SSE2=", "-msse2", "-O3", "-DNDEBUG"}
	if runtime.GOOS == "darwin" {
		cflags = append(cflags, "-arch", "x86_64")
	}
	return BuildCHelper(CHelperConfig{
		Label:       "CELT float PVQ SSE2",
		OutputBase:  "gopus_libopus_celt_pvq_float_sse2",
		SourceFile:  "libopus_celt_pvq_float_sse2_info.c",
		CFlags:      cflags,
		RefIncludes: []string{"celt", "silk"},
		RefSources:  []string{"celt/x86/vq_sse2.c"},
		Libs:        []string{"-lm"},
		DeadStrip:   true,
	})
}

// ProbeCELTPVQSearchFloatSSE2 runs the pinned libopus x86 SSE2 PVQ kernel.
func ProbeCELTPVQSearchFloatSSE2(x []float32, k int) (yy float32, iy []int32, err error) {
	binPath, buildErr := celtPVQFloatSSE2Helper.Path(buildCELTPVQFloatSSE2Helper)
	if buildErr != nil {
		return 0, nil, buildErr
	}
	payload := NewOraclePayload(celtPVQFloatSSE2InputMagic, uint32(len(x)), uint32(k))
	for _, v := range x {
		payload.U32(math.Float32bits(v))
	}

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT float PVQ SSE2", celtPVQFloatSSE2OutputMagic)
	if runErr != nil {
		return 0, nil, runErr
	}
	yy = math.Float32frombits(reader.U32())
	n := int(reader.U32())
	if n != len(x) {
		return 0, nil, fmt.Errorf("celt float PVQ SSE2 oracle returned n=%d want %d", n, len(x))
	}
	iy = make([]int32, n)
	for i := range iy {
		iy[i] = int32(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return 0, nil, err
	}
	return yy, iy, nil
}
