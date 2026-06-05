package libopustest

const (
	celtDeemphasisInputMagic  = "GDPI"
	celtDeemphasisOutputMagic = "GDPO"
)

var celtDeemphasisHelper HelperCache

func buildCELTDeemphasisHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt deemphasis",
		OutputBase:  "gopus_libopus_celt_deemphasis",
		SourceFile:  "libopus_celt_deemphasis_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTDeemphasisHelperPath() (string, error) {
	return celtDeemphasisHelper.Path(buildCELTDeemphasisHelper)
}

// CELTDeemphasisResult holds the FIXED_POINT deemphasis oracle output: the
// interleaved opus_res pcm buffer (length Nd*C, stride C) and the updated
// per-channel filter state mem.
type CELTDeemphasisResult struct {
	PCM []int32
	Mem []int32
}

// ProbeCELTFixedDeemphasis runs the FIXED_POINT celt/celt_decoder.c deemphasis
// kernel against the real libopus reference.
//
//	in          per-channel celt_sig synthesis input, in[c] has length N
//	pcm         pre-existing interleaved opus_res buffer (length Nd*C); only
//	            read when accum is set, but always provided to seed the buffer
//	coef        mode->preemph coefficients (length 4); only coef[0] is used in
//	            this build config
//	mem         per-channel filter state (length C)
//	N           per-channel sample count
//	downsample  decimation factor (1, 2, ...)
//	accum       whether to accumulate into pcm (ADD_RES) instead of overwriting
func ProbeCELTFixedDeemphasis(in [][]int32, pcm []int32, coef []int16, mem []int32, N, downsample int, accum bool) (*CELTDeemphasisResult, error) {
	binPath, err := getCELTDeemphasisHelperPath()
	if err != nil {
		return nil, err
	}
	C := len(in)
	Nd := N / downsample
	pcmLen := Nd * C
	accumWord := uint32(0)
	if accum {
		accumWord = 1
	}
	payload := NewOraclePayload(celtDeemphasisInputMagic,
		uint32(N), uint32(C), uint32(downsample), accumWord)
	for i := range 4 {
		var v int16
		if i < len(coef) {
			v = coef[i]
		}
		payload.I32(int32(v))
	}
	for c := range C {
		payload.I32s(in[c][:N]...)
	}
	for c := range C {
		payload.I32(mem[c])
	}
	for i := range pcmLen {
		var v int32
		if i < len(pcm) {
			v = pcm[i]
		}
		payload.I32(v)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed deemphasis", celtDeemphasisOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(pcmLen)
	outPCM := make([]int32, count)
	for i := range outPCM {
		outPCM[i] = reader.I32()
	}
	memCount := reader.Count(C)
	outMem := make([]int32, memCount)
	for i := range outMem {
		outMem[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return &CELTDeemphasisResult{PCM: outPCM, Mem: outMem}, nil
}
