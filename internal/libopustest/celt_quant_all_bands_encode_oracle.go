package libopustest

const (
	celtQuantAllBandsEncodeInputMagic  = "GQEI"
	celtQuantAllBandsEncodeOutputMagic = "GQEO"
)

var celtQuantAllBandsEncodeHelper HelperCache

func buildCELTQuantAllBandsEncodeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt quant_all_bands encode fixed",
		OutputBase:  "gopus_libopus_celt_quant_all_bands_encode_fixed",
		SourceFile:  "libopus_celt_quant_all_bands_encode_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTQuantAllBandsEncodeHelperPath() (string, error) {
	return celtQuantAllBandsEncodeHelper.Path(buildCELTQuantAllBandsEncodeHelper)
}

// CELTQuantAllBandsEncodeParams describes a quant_all_bands encode pass against
// the FIXED_POINT libopus bands.c reference. A fresh range encoder is created
// over an Nbytes-sized buffer; the normalized X (and stereo Y), band energies,
// allocation and tf inputs are supplied directly so the comparison is decoupled
// from the surrounding encoder state. X is channel-major with stride N.
type CELTQuantAllBandsEncodeParams struct {
	Channels    int
	LM          int
	Start       int
	End         int
	ShortBlocks int
	Spread      int
	DualStereo  int
	Intensity   int
	TotalBits   int32
	Balance     int32
	CodedBands  int
	Complexity  int
	DisableInv  bool
	Seed        uint32
	NbEBands    int
	Nbytes      int
	Pulses      []int32
	TfRes       []int32
	BandE       []int32 // channels*NbEBands, channel-major (per-band, then +nbEBands)
	X           []int32 // channels*N, channel-major
}

// CELTQuantAllBandsEncodeResult holds the reference quant_all_bands encode
// output: the coded byte buffer (full storage, raw bits included), the number
// of forward range-coder bytes used, the post-encode normalized X buffer
// (channel-major, stride N), the per-band collapse masks, the channel sample
// count N and the threaded LCG seed.
type CELTQuantAllBandsEncodeResult struct {
	N         int
	Channels  int
	Seed      uint32
	BytesUsed int
	Coded     []byte
	X         []int32
	Collapse  []byte
}

// ProbeCELTFixedQuantAllBandsEncode runs the real FIXED_POINT libopus
// quant_all_bands (encode side, QEXT off) over the supplied inputs and returns
// the coded bytes, post-encode X[] and collapse masks.
func ProbeCELTFixedQuantAllBandsEncode(p CELTQuantAllBandsEncodeParams) (*CELTQuantAllBandsEncodeResult, error) {
	binPath, err := getCELTQuantAllBandsEncodeHelperPath()
	if err != nil {
		return nil, err
	}

	disableInv := uint32(0)
	if p.DisableInv {
		disableInv = 1
	}

	payload := NewOraclePayload(celtQuantAllBandsEncodeInputMagic)
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.LM))
	payload.U32(uint32(p.Start))
	payload.U32(uint32(p.End))
	payload.U32(uint32(p.ShortBlocks))
	payload.U32(uint32(p.Spread))
	payload.U32(uint32(p.DualStereo))
	payload.U32(uint32(p.Intensity))
	payload.I32(p.TotalBits)
	payload.I32(p.Balance)
	payload.U32(uint32(p.CodedBands))
	payload.U32(uint32(p.Complexity))
	payload.U32(disableInv)
	payload.U32(p.Seed)
	payload.U32(uint32(p.NbEBands))
	payload.U32(uint32(p.Nbytes))
	payload.I32s(padInt32(p.Pulses, p.NbEBands)...)
	payload.I32s(padInt32(p.TfRes, p.NbEBands)...)
	payload.I32s(p.BandE...)
	payload.I32s(p.X...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed quant_all_bands encode", celtQuantAllBandsEncodeOutputMagic)
	if err != nil {
		return nil, err
	}
	n := int(reader.U32())
	channels := int(reader.U32())
	seed := reader.U32()
	bytesUsed := int(reader.U32())
	if err := reader.Err(); err != nil {
		return nil, err
	}
	coded := append([]byte(nil), reader.Bytes(p.Nbytes)...)
	if pad := (4 - p.Nbytes%4) % 4; pad > 0 {
		reader.Bytes(pad)
	}
	totalX := channels * n
	x := make([]int32, totalX)
	for i := range x {
		x[i] = reader.I32()
	}
	mcount := channels * p.NbEBands
	masks := append([]byte(nil), reader.Bytes(mcount)...)
	if pad := (4 - mcount%4) % 4; pad > 0 {
		reader.Bytes(pad)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return &CELTQuantAllBandsEncodeResult{
		N:         n,
		Channels:  channels,
		Seed:      seed,
		BytesUsed: bytesUsed,
		Coded:     coded,
		X:         x,
		Collapse:  masks,
	}, nil
}
