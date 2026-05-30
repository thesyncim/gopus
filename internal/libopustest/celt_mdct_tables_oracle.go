package libopustest

const (
	celtMDCTTablesInputMagic  = "GTMI"
	celtMDCTTablesOutputMagic = "GTMO"
)

var celtMDCTTablesHelper HelperCache

func buildCELTMDCTTablesHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt mdct tables fixed",
		OutputBase:  "gopus_libopus_celt_mdct_tables_fixed",
		SourceFile:  "libopus_celt_mdct_tables_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// CELTMDCTKFFT mirrors one sub-FFT state of the static 48000/960 mode->mdct.
type CELTMDCTKFFT struct {
	Nfft       int
	Scale      int32
	ScaleShift int32
	Shift      int32
	Factors    [16]int16
	Bitrev     []int16
	Twiddles   [][2]int16
}

// CELTMDCTTables holds the dumped static 48000/960 mode->mdct tables plus the
// mode window, band layout and preemphasis coefficient.
type CELTMDCTTables struct {
	N        int
	MaxShift int
	Trig     []int16
	Window   []int16
	EBands   []int16
	Preemph0 int32
	KFFT     []CELTMDCTKFFT
}

// ProbeCELTFixedMDCTTables dumps the libopus FIXED_POINT static 48000/960 custom
// mode's mode->mdct lookup (the exact tables celt_decode_with_ec uses).
func ProbeCELTFixedMDCTTables() (*CELTMDCTTables, error) {
	binPath, err := celtMDCTTablesHelper.Path(buildCELTMDCTTablesHelper)
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtMDCTTablesInputMagic, 0, 0)
	reader, err := RunOracle(binPath, payload.Bytes(), "celt mdct tables", celtMDCTTablesOutputMagic)
	if err != nil {
		return nil, err
	}
	reader.U32() // count word (unused)
	out := &CELTMDCTTables{}
	out.N = int(reader.U32())
	out.MaxShift = int(reader.U32())
	trigLen := int(reader.U32())
	out.Trig = make([]int16, trigLen)
	for i := range out.Trig {
		out.Trig[i] = reader.I16()
	}
	winLen := int(reader.U32())
	out.Window = make([]int16, winLen)
	for i := range out.Window {
		out.Window[i] = reader.I16()
	}
	ebLen := int(reader.U32())
	out.EBands = make([]int16, ebLen)
	for i := range out.EBands {
		out.EBands[i] = reader.I16()
	}
	out.Preemph0 = reader.I32()
	for s := 0; s <= out.MaxShift; s++ {
		var k CELTMDCTKFFT
		k.Nfft = int(reader.U32())
		k.Scale = reader.I32()
		k.ScaleShift = reader.I32()
		k.Shift = reader.I32()
		for i := 0; i < 16; i++ {
			k.Factors[i] = reader.I16()
		}
		bl := int(reader.U32())
		k.Bitrev = make([]int16, bl)
		for i := range k.Bitrev {
			k.Bitrev[i] = reader.I16()
		}
		tl := int(reader.U32())
		k.Twiddles = make([][2]int16, tl)
		for i := range k.Twiddles {
			k.Twiddles[i][0] = reader.I16()
			k.Twiddles[i][1] = reader.I16()
		}
		out.KFFT = append(out.KFFT, k)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
