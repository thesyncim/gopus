package libopustest

const (
	celtQuantAllBandsInputMagic  = "GQBI"
	celtQuantAllBandsOutputMagic = "GQBO"
)

var celtQuantAllBandsHelper HelperCache

func buildCELTQuantAllBandsHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt quant_all_bands fixed",
		OutputBase:  "gopus_libopus_celt_quant_all_bands_fixed",
		SourceFile:  "libopus_celt_quant_all_bands_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTQuantAllBandsHelperPath() (string, error) {
	return celtQuantAllBandsHelper.Path(buildCELTQuantAllBandsHelper)
}

// CELTQuantAllBandsParams describes a quant_all_bands decode pass against the
// FIXED_POINT libopus bands.c reference. The range decoder is initialized fresh
// over Coded; the energy/allocation/tf inputs are supplied directly so the
// comparison is decoupled from surrounding decoder state.
type CELTQuantAllBandsParams struct {
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
	DisableInv  bool
	Seed        uint32
	NbEBands    int
	Pulses      []int32
	TfRes       []int32
	Coded       []byte
}

// CELTQuantAllBandsResult holds the reference quant_all_bands decode output:
// the normalized celt_norm X buffer (channel-major, channel stride N), the
// per-band collapse masks (channels*NbEBands), the channel sample count N and
// the threaded LCG seed.
type CELTQuantAllBandsResult struct {
	N        int
	Channels int
	Seed     uint32
	X        []int32
	Collapse []byte
}

// ProbeCELTFixedQuantAllBands runs the real FIXED_POINT libopus quant_all_bands
// (decode side, QEXT off) over the supplied inputs and returns the resulting
// normalized X[] (channels*N) plus collapse masks.
func ProbeCELTFixedQuantAllBands(p CELTQuantAllBandsParams) (*CELTQuantAllBandsResult, error) {
	binPath, err := getCELTQuantAllBandsHelperPath()
	if err != nil {
		return nil, err
	}

	disableInv := uint32(0)
	if p.DisableInv {
		disableInv = 1
	}

	payload := NewOraclePayload(celtQuantAllBandsInputMagic)
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
	payload.U32(disableInv)
	payload.U32(p.Seed)
	payload.U32(uint32(p.NbEBands))
	payload.I32s(padInt32(p.Pulses, p.NbEBands)...)
	payload.I32s(padInt32(p.TfRes, p.NbEBands)...)
	payload.U32(uint32(len(p.Coded)))
	payload.Raw(p.Coded)
	if pad := (4 - len(p.Coded)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed quant_all_bands", celtQuantAllBandsOutputMagic)
	if err != nil {
		return nil, err
	}
	n := int(reader.U32())
	channels := int(reader.U32())
	seed := reader.U32()
	if err := reader.Err(); err != nil {
		return nil, err
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
	return &CELTQuantAllBandsResult{
		N:        n,
		Channels: channels,
		Seed:     seed,
		X:        x,
		Collapse: masks,
	}, nil
}
