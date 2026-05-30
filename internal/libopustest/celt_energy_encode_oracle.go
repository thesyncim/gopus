package libopustest

const (
	celtEnergyEncodeInputMagic  = "GEEI"
	celtEnergyEncodeOutputMagic = "GEEO"

	// CELTEnergyEncodeModeQuant drives quant_coarse_energy + quant_fine_energy +
	// quant_energy_finalise.
	CELTEnergyEncodeModeQuant = uint32(0)
)

var celtEnergyEncodeHelper HelperCache

func buildCELTEnergyEncodeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt energy encode",
		OutputBase:  "gopus_libopus_celt_energy_encode",
		SourceFile:  "libopus_celt_energy_encode_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTEnergyEncodeHelperPath() (string, error) {
	return celtEnergyEncodeHelper.Path(buildCELTEnergyEncodeHelper)
}

// CELTEnergyEncodeParams configures a reference run of the FIXED_POINT CELT
// energy encoders.
type CELTEnergyEncodeParams struct {
	NbEBands         int
	Start            int
	End              int
	EffEnd           int
	C                int
	LM               int
	Budget           int
	NbAvailableBytes int
	ForceIntra       bool
	TwoPass          bool
	LossRate         int
	Lfe              bool
	DelayedIntra     int32
	BufSize          int
	FinaliseBits     int

	// EBands is the channel-major Q24 target log energy (length C*NbEBands).
	EBands []int32
	// OldEBands is the channel-major Q24 predictor (length C*NbEBands).
	OldEBands []int32
	// FineQuant, ExtraQuant and FinePriority are per-band (length NbEBands).
	FineQuant    []int32
	ExtraQuant   []int32
	FinePriority []int32
}

// CELTEnergyEncodeResult holds the reference output.
type CELTEnergyEncodeResult struct {
	// Bytes is the coded range-coder output.
	Bytes []byte
	// OldEBands is the reconstructed channel-major Q24 energy (length C*NbEBands).
	OldEBands []int32
	// Error is the residual channel-major Q24 error (length C*NbEBands).
	Error []int32
	// DelayedIntra is the updated distortion accumulator.
	DelayedIntra int32
}

func boolU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// ProbeCELTQuantEnergyEncode drives the real libopus FIXED_POINT
// quant_coarse_energy, quant_fine_energy and quant_energy_finalise through a
// genuine ec_enc and returns the coded bytes plus the resulting state.
func ProbeCELTQuantEnergyEncode(p CELTEnergyEncodeParams) (*CELTEnergyEncodeResult, error) {
	binPath, err := getCELTEnergyEncodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtEnergyEncodeInputMagic, CELTEnergyEncodeModeQuant, 0)
	payload.U32(uint32(p.NbEBands))
	payload.U32(uint32(p.Start))
	payload.U32(uint32(p.End))
	payload.U32(uint32(p.EffEnd))
	payload.U32(uint32(p.C))
	payload.U32(uint32(p.LM))
	payload.U32(uint32(p.Budget))
	payload.U32(uint32(p.NbAvailableBytes))
	payload.U32(boolU32(p.ForceIntra))
	payload.U32(boolU32(p.TwoPass))
	payload.U32(uint32(p.LossRate))
	payload.U32(boolU32(p.Lfe))
	payload.I32(p.DelayedIntra)
	payload.U32(uint32(p.BufSize))
	payload.U32(uint32(p.FinaliseBits))
	payload.I32s(p.EBands...)
	payload.I32s(p.OldEBands...)
	payload.I32s(p.FineQuant...)
	payload.I32s(p.ExtraQuant...)
	payload.I32s(p.FinePriority...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt quant energy encode", celtEnergyEncodeOutputMagic)
	if err != nil {
		return nil, err
	}

	res := &CELTEnergyEncodeResult{}
	// Consume the unused count header word written after the version.
	reader.U32()
	nbytes := int(reader.U32())
	res.Bytes = reader.Bytes(nbytes)
	total := int(reader.U32())
	res.OldEBands = make([]int32, total)
	for i := range res.OldEBands {
		res.OldEBands[i] = reader.I32()
	}
	res.Error = make([]int32, total)
	for i := range res.Error {
		res.Error[i] = reader.I32()
	}
	res.DelayedIntra = reader.I32()
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return res, nil
}
