package libopustest

const (
	celtEncodeInputMagic  = "GCEI"
	celtEncodeOutputMagic = "GCEO"

	// CELTEncodeModeEncode selects the full FIXED_POINT celt_encode_with_ec,
	// dumping the produced packet bytes.
	CELTEncodeModeEncode = uint32(0)
	// CELTEncodeModeFrontend selects the inline FIXED_POINT encode front-end
	// (celt_preemphasis + compute_mdcts + compute_band_energies +
	// normalise_bands), dumping freq, bandE and the normalised X.
	CELTEncodeModeFrontend = uint32(1)
	// CELTEncodeModeEncodeSeq encodes N consecutive frames on a single encoder
	// (VBR/CVBR/CBR), dumping every produced packet so cross-frame state can be
	// validated.
	CELTEncodeModeEncodeSeq = uint32(2)
	// CELTEncodeModeEncodeExt runs one celt_encode_with_ec frame with the LFE
	// and/or energy_mask controls applied, dumping the produced packet bytes.
	CELTEncodeModeEncodeExt = uint32(3)

	// celtEncodeNbEBands mirrors the static 48000/960 mode nbEBands.
	celtEncodeNbEBands = 21
)

var celtEncodeHelper HelperCache

func buildCELTEncodeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt encode fixed",
		OutputBase:  "gopus_libopus_celt_encode_fixed",
		SourceFile:  "libopus_celt_encode_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTEncodeHelperPath() (string, error) {
	return celtEncodeHelper.Path(buildCELTEncodeHelper)
}

// CELTFrontendResult holds the intermediate front-end signals produced by the
// FIXED_POINT encode front-end just before quant_all_bands.
type CELTFrontendResult struct {
	// Freq is the interleaved post-MDCT signal (celt_sig), length C*N.
	Freq []int32
	// BandE is the per-band energy (celt_ener), channel-major, length C*nbEBands.
	BandE []int32
	// X is the normalised bands (celt_norm), interleaved, length C*N.
	X []int32
}

// ProbeCELTFixedEncodeFrontend runs the inline FIXED_POINT encode front-end on
// the static 48000/960 mode for the supplied interleaved int16 PCM
// (channels*frameSize samples). isTransient selects the transient MDCT striping
// (shortBlocks==M) vs the normal long-block path. It returns the resulting
// freq, bandE and normalised X exactly as celt_encode_with_ec would compute
// them on a fresh encoder's first frame.
func ProbeCELTFixedEncodeFrontend(pcm []int16, channels, frameSize, start, end int, isTransient bool) (*CELTFrontendResult, error) {
	binPath, err := getCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}

	tr := uint32(0)
	if isTransient {
		tr = 1
	}
	nsamples := channels * frameSize
	payload := NewOraclePayload(celtEncodeInputMagic, CELTEncodeModeFrontend, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(tr)
	payload.U32(uint32(nsamples))
	for _, s := range pcm {
		payload.I16(s)
	}
	if pad := (4 - (nsamples*2)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed encode frontend", celtEncodeOutputMagic)
	if err != nil {
		return nil, err
	}

	n := frameSize // N == frameSize for the 48k core
	cn := channels * n
	reader.Count(cn)
	reader.ExpectRemaining(4 * (2*cn + channels*celtEncodeNbEBands))
	res := &CELTFrontendResult{
		Freq:  make([]int32, cn),
		BandE: make([]int32, channels*celtEncodeNbEBands),
		X:     make([]int32, cn),
	}
	for i := range res.Freq {
		res.Freq[i] = reader.I32()
	}
	for i := range res.BandE {
		res.BandE[i] = reader.I32()
	}
	for i := range res.X {
		res.X[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return res, nil
}

// ProbeCELTFixedEncode runs the full FIXED_POINT celt_encode_with_ec on the
// static 48000/960 mode for the supplied interleaved int16 PCM
// (channels*frameSize samples) at the given CBR target, returning the produced
// CELT packet bytes. This is the end-goal reference for the integer encoder.
func ProbeCELTFixedEncode(pcm []int16, channels, frameSize, start, end, bitrate, complexity, nbCompressedBytes int) ([]byte, error) {
	binPath, err := getCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}

	nsamples := channels * frameSize
	payload := NewOraclePayload(celtEncodeInputMagic, CELTEncodeModeEncode, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(bitrate))
	payload.U32(uint32(complexity))
	payload.U32(uint32(nbCompressedBytes))
	payload.U32(uint32(nsamples))
	for _, s := range pcm {
		payload.I16(s)
	}
	if pad := (4 - (nsamples*2)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed encode", celtEncodeOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(-1)
	pad := (4 - count%4) % 4
	reader.ExpectRemaining(count + pad)
	out := append([]byte(nil), reader.Bytes(count)...)
	if pad > 0 {
		reader.Bytes(pad)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// ProbeCELTFixedEncodeExt runs the full FIXED_POINT celt_encode_with_ec on the
// static 48000/960 mode with the LFE and/or energy_mask controls applied,
// returning the produced CELT packet bytes. mask, when non-nil, holds
// channels*nbEBands celt_glog (Q24, channel-major) values passed via
// OPUS_SET_ENERGY_MASK.
func ProbeCELTFixedEncodeExt(pcm []int16, channels, frameSize, start, end, bitrate, complexity, nbCompressedBytes int,
	vbr, constrainedVBR, lfe bool, mask []int32) ([]byte, error) {
	binPath, err := getCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}

	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}

	hasMask := mask != nil
	nsamples := channels * frameSize
	payload := NewOraclePayload(celtEncodeInputMagic, CELTEncodeModeEncodeExt, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(bitrate))
	payload.U32(uint32(complexity))
	payload.U32(uint32(nbCompressedBytes))
	payload.U32(b2u(vbr))
	payload.U32(b2u(constrainedVBR))
	payload.U32(b2u(lfe))
	payload.U32(b2u(hasMask))
	payload.U32(uint32(nsamples))
	for _, s := range pcm {
		payload.I16(s)
	}
	if pad := (4 - (nsamples*2)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}
	if hasMask {
		for _, v := range mask {
			payload.I32(v)
		}
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed encode ext", celtEncodeOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(-1)
	pad := (4 - count%4) % 4
	reader.ExpectRemaining(count + pad)
	out := append([]byte(nil), reader.Bytes(count)...)
	if pad > 0 {
		reader.Bytes(pad)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// ProbeCELTFixedEncodeSeq creates one FIXED_POINT CELT encoder, configures it
// for the given VBR/constrained-VBR/CBR mode and bitrate, and encodes nframes
// consecutive frames of interleaved int16 PCM (channels*frameSize*nframes
// samples) in sequence. It returns the produced packet bytes for each frame so
// cross-frame state (VBR reservoir/drift, energy histories, spec_avg,
// consec_transient, prefilter_mem) can be validated. maxBytes is the per-frame
// output buffer size (the VBR/CBR cap).
func ProbeCELTFixedEncodeSeq(pcm []int16, channels, frameSize, start, end, bitrate, complexity int,
	vbr, constrainedVBR bool, maxBytes, nframes int) ([][]byte, error) {
	binPath, err := getCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}

	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}

	nsamples := channels * frameSize * nframes
	payload := NewOraclePayload(celtEncodeInputMagic, CELTEncodeModeEncodeSeq, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(bitrate))
	payload.U32(uint32(complexity))
	payload.U32(b2u(vbr))
	payload.U32(b2u(constrainedVBR))
	payload.U32(uint32(maxBytes))
	payload.U32(uint32(nframes))
	payload.U32(uint32(nsamples))
	for _, s := range pcm {
		payload.I16(s)
	}
	if pad := (4 - (nsamples*2)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed encode seq", celtEncodeOutputMagic)
	if err != nil {
		return nil, err
	}
	nf := reader.Count(nframes)
	packets := make([][]byte, nf)
	for f := 0; f < nf; f++ {
		count := int(reader.U32())
		pad := (4 - count%4) % 4
		packets[f] = append([]byte(nil), reader.Bytes(count)...)
		if pad > 0 {
			reader.Bytes(pad)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return packets, nil
}
