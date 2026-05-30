package libopustest

const (
	celtSynthesisInputMagic  = "GCYI"
	celtSynthesisOutputMagic = "GCYO"

	// CELTSynthesisDecodeBufferSize mirrors DECODE_BUFFER_SIZE
	// (DEC_PITCH_BUF_SIZE) in celt/celt_decoder.c. The synthesis writes into a
	// per-channel decode_mem buffer of size CELTSynthesisDecodeBufferSize+overlap.
	CELTSynthesisDecodeBufferSize = 2048
)

var celtSynthesisHelper HelperCache

func buildCELTSynthesisHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt synthesis fixed",
		OutputBase:  "gopus_libopus_celt_synthesis_fixed",
		SourceFile:  "libopus_celt_synthesis_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTSynthesisHelperPath() (string, error) {
	return celtSynthesisHelper.Path(buildCELTSynthesisHelper)
}

// CELTSynthesisParams describes a single celt_synthesis call against the
// FIXED_POINT reference using the static 48000 mode.
type CELTSynthesisParams struct {
	FrameSize   int // shortMdctSize<<LM (120/240/480/960 for LM 0..3)
	C           int // coded channels (1 or 2)
	CC          int // output channels (1 or 2)
	LM          int
	Downsample  int
	Start       int
	EffEnd      int
	IsTransient bool
	Silence     bool
}

// CELTSynthesisResult holds the libopus celt_synthesis output: the mode bands
// and window used (so the Go side can reuse the exact reference values) and the
// resulting per-channel decode_mem buffers (each ChanLen samples).
type CELTSynthesisResult struct {
	NbEBands int
	Overlap  int
	EBands   []int16
	Window   []int16
	ChanLen  int
	// DecodeMem is the per-channel decode_mem after synthesis, channel-major:
	// DecodeMem[c*ChanLen : (c+1)*ChanLen].
	DecodeMem []int32
}

// ProbeCELTFixedSynthesis runs the FIXED_POINT celt_synthesis on the supplied
// coefficients, band energies and per-channel decode_mem state, returning the
// reference mode bands/window and the resulting decode_mem buffers.
//
//	x         de-normalised input coefficients (celt_norm), length C*N
//	oldBandE  per-band quantized log energy (celt_glog), length C*nbEBands
//	decodeMem per-channel input decode_mem (celt_sig), channel-major, length
//	          CC*(CELTSynthesisDecodeBufferSize+overlap)
func ProbeCELTFixedSynthesis(p CELTSynthesisParams, x, oldBandE, decodeMem []int32) (*CELTSynthesisResult, error) {
	binPath, err := getCELTSynthesisHelperPath()
	if err != nil {
		return nil, err
	}
	boolWord := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}
	payload := NewOraclePayload(celtSynthesisInputMagic,
		uint32(p.FrameSize), uint32(p.C), uint32(p.CC), boolWord(p.IsTransient),
		uint32(p.LM), uint32(p.Downsample), boolWord(p.Silence),
		uint32(p.Start), uint32(p.EffEnd))
	payload.I32s(x...)
	payload.I32s(oldBandE...)
	payload.I32s(decodeMem...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed synthesis", celtSynthesisOutputMagic)
	if err != nil {
		return nil, err
	}
	res := &CELTSynthesisResult{}
	res.NbEBands = int(reader.U32())
	res.Overlap = int(reader.U32())
	if res.NbEBands < 0 || res.Overlap < 0 {
		return nil, reader.Err()
	}
	res.EBands = make([]int16, res.NbEBands+1)
	for i := range res.EBands {
		res.EBands[i] = int16(reader.I32())
	}
	res.Window = make([]int16, res.Overlap)
	for i := range res.Window {
		res.Window[i] = int16(reader.I32())
	}
	res.ChanLen = CELTSynthesisDecodeBufferSize + res.Overlap
	want := p.CC * res.ChanLen
	count := reader.Count(want)
	reader.ExpectRemaining(4 * count)
	res.DecodeMem = make([]int32, count)
	for i := range res.DecodeMem {
		res.DecodeMem[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return res, nil
}
