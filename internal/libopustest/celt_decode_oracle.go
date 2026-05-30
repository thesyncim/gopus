package libopustest

const (
	celtDecodeInputMagic  = "GCDI"
	celtDecodeOutputMagic = "GCDO"

	// CELTDecodeModeEnergy selects the FIXED_POINT energy unquantizers
	// (unquant_coarse_energy + unquant_fine_energy + unquant_energy_finalise).
	CELTDecodeModeEnergy = uint32(0)
	// CELTDecodeModeDecode selects the full FIXED_POINT celt_decode_with_ec.
	CELTDecodeModeDecode = uint32(1)
	// CELTDecodeModeDecodeSeq decodes a sequence of packets through one
	// FIXED_POINT decoder (cross-frame state + downsample output path).
	CELTDecodeModeDecodeSeq = uint32(2)
)

var celtDecodeHelper HelperCache

func buildCELTDecodeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt decode fixed",
		OutputBase:  "gopus_libopus_celt_decode_fixed",
		SourceFile:  "libopus_celt_decode_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTDecodeHelperPath() (string, error) {
	return celtDecodeHelper.Path(buildCELTDecodeHelper)
}

// CELTEnergyUnquantParams describes a coarse+fine+finalise energy decode pass
// against the FIXED_POINT libopus quant_bands.c unquantizers.
type CELTEnergyUnquantParams struct {
	NbEBands     int
	Start        int
	End          int
	EffEnd       int
	Channels     int
	LM           int
	Intra        bool
	Coded        []byte
	FineQuant    []int32
	FinePriority []int32
	BitsLeft     int32
}

// ProbeCELTFixedEnergyUnquant runs unquant_coarse_energy, unquant_fine_energy and
// unquant_energy_finalise on the supplied coded bytes against the real libopus
// FIXED_POINT reference, returning the resulting oldEBands (channel-major, Q24,
// length Channels*NbEBands).
func ProbeCELTFixedEnergyUnquant(p CELTEnergyUnquantParams) ([]int32, error) {
	binPath, err := getCELTDecodeHelperPath()
	if err != nil {
		return nil, err
	}

	intra := uint32(0)
	if p.Intra {
		intra = 1
	}
	payload := NewOraclePayload(celtDecodeInputMagic, CELTDecodeModeEnergy, 0)
	payload.U32(uint32(p.NbEBands))
	payload.U32(uint32(p.Start))
	payload.U32(uint32(p.End))
	payload.U32(uint32(p.EffEnd))
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.LM))
	payload.U32(intra)
	payload.U32(uint32(len(p.Coded)))
	payload.Raw(p.Coded)
	if pad := (4 - len(p.Coded)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}
	fineQuant := padInt32(p.FineQuant, p.NbEBands)
	finePriority := padInt32(p.FinePriority, p.NbEBands)
	payload.I32s(fineQuant...)
	payload.I32s(finePriority...)
	payload.I32(p.BitsLeft)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed energy unquant", celtDecodeOutputMagic)
	if err != nil {
		return nil, err
	}
	want := p.Channels * p.NbEBands
	count := reader.Count(want)
	reader.ExpectRemaining(4 * count)
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// ProbeCELTFixedDecode runs the full FIXED_POINT celt_decode_with_ec on a CELT
// packet using the static 48000/960 mode, returning interleaved int16 PCM of
// length channels*frameSize.
func ProbeCELTFixedDecode(packet []byte, channels, frameSize, start, end int) ([]int16, error) {
	binPath, err := getCELTDecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtDecodeInputMagic, CELTDecodeModeDecode, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(len(packet)))
	payload.Raw(packet)
	if pad := (4 - len(packet)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed decode", celtDecodeOutputMagic)
	if err != nil {
		return nil, err
	}
	want := channels * frameSize
	count := reader.Count(want)
	reader.ExpectRemaining(2 * count)
	out := make([]int16, count)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// ProbeCELTFixedDecodeSeq creates ONE FIXED_POINT libopus CELT decoder at the
// given output samplingRate (48000/24000/16000/12000/8000) and decodes the
// supplied sequence of CELT packets in order through it, returning one int16 PCM
// slice per packet. frameSize is the 48k-core per-channel sample count; each
// returned frame has channels*(frameSize/downsample) interleaved samples, where
// downsample = resampling_factor(samplingRate). This exercises the cross-frame
// decode_mem/post-filter/energy-prediction carry and the downsample output path.
func ProbeCELTFixedDecodeSeq(packets [][]byte, channels, frameSize, start, end, samplingRate int) ([][]int16, error) {
	binPath, err := getCELTDecodeHelperPath()
	if err != nil {
		return nil, err
	}
	downsample := resamplingFactorRef(samplingRate)
	outPerFrame := channels * (frameSize / downsample)

	payload := NewOraclePayload(celtDecodeInputMagic, CELTDecodeModeDecodeSeq, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(samplingRate))
	payload.U32(uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
		if pad := (4 - len(pkt)%4) % 4; pad > 0 {
			payload.Raw(make([]byte, pad))
		}
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed decode seq", celtDecodeOutputMagic)
	if err != nil {
		return nil, err
	}
	want := len(packets) * outPerFrame
	count := reader.Count(want)
	reader.ExpectRemaining(2 * count)
	flat := make([]int16, count)
	for i := range flat {
		flat[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	out := make([][]int16, len(packets))
	for p := range out {
		out[p] = flat[p*outPerFrame : (p+1)*outPerFrame]
	}
	return out, nil
}

// resamplingFactorRef mirrors celt/celt.c resampling_factor for the rates the
// oracle accepts.
func resamplingFactorRef(rate int) int {
	switch rate {
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	default:
		return 1
	}
}

func padInt32(src []int32, n int) []int32 {
	if len(src) >= n {
		return src[:n]
	}
	out := make([]int32, n)
	copy(out, src)
	return out
}
