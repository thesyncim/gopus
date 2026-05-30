package libopustest

import "fmt"

const (
	qextDecode96kInputMagic  = "GQDI"
	qextDecode96kOutputMagic = "GQDO"

	// QEXTDecode96kFormatFloat32 selects opus_decode_float output.
	QEXTDecode96kFormatFloat32 = uint32(0)
	// QEXTDecode96kFormatInt16 selects opus_decode output.
	QEXTDecode96kFormatInt16 = uint32(1)
	// QEXTDecode96kFormatInt24 selects opus_decode24 output.
	QEXTDecode96kFormatInt24 = uint32(2)
)

var qextDecode96kHelper HelperCache

func buildQEXTDecode96kHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "qext decode96k",
		OutputBase:  "gopus_libopus_qext_decode96k",
		SourceFile:  "libopus_qext_decode96k_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getQEXTDecode96kHelperPath() (string, error) {
	return qextDecode96kHelper.Path(buildQEXTDecode96kHelper)
}

// QEXTDecode96kParams configures a native 96 kHz QEXT full-packet decode probe.
// The reference decoder is created with opus_decoder_create(96000, channels),
// which under ENABLE_QEXT runs the native 96 kHz CELT mode plus the >20 kHz
// extension-band decode chain.
type QEXTDecode96kParams struct {
	SampleFormat uint32 // QEXTDecode96kFormat* (float32/int16/int24)
	Channels     int
	MaxFrameSize int      // per-channel sample capacity passed to opus_decode (96 kHz)
	Packets      [][]byte // Opus packets to decode in sequence through one decoder
}

// QEXTDecode96kResult holds the decoded native 96 kHz PCM and per-packet final
// range. For float32 output PCM is populated; Int16/Int24 carry the integer
// formats. Exactly one of the three is non-nil depending on SampleFormat.
type QEXTDecode96kResult struct {
	PCM         []float32
	Int16       []int16
	Int24       []int32
	FinalRanges []uint32
}

// ProbeQEXTDecode96k decodes the supplied Opus packets through the QEXT-enabled
// libopus reference at Fs=96000 and returns the native 96 kHz PCM (interleaved)
// plus the per-packet OPUS_GET_FINAL_RANGE values.
func ProbeQEXTDecode96k(p QEXTDecode96kParams) (QEXTDecode96kResult, error) {
	binPath, err := getQEXTDecode96kHelperPath()
	if err != nil {
		return QEXTDecode96kResult{}, err
	}
	if p.Channels < 1 || p.Channels > 2 {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k: invalid channels %d", p.Channels)
	}
	if p.MaxFrameSize <= 0 {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k: invalid maxFrameSize %d", p.MaxFrameSize)
	}

	payload := NewOraclePayloadVersion(qextDecode96kInputMagic, 1)
	payload.U32(p.SampleFormat)
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.MaxFrameSize))
	payload.U32(uint32(len(p.Packets)))
	for _, pkt := range p.Packets {
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "qext decode96k", qextDecode96kOutputMagic)
	if err != nil {
		return QEXTDecode96kResult{}, err
	}

	total := int(reader.U32())
	var res QEXTDecode96kResult
	switch p.SampleFormat {
	case QEXTDecode96kFormatInt16:
		res.Int16 = make([]int16, total)
		for i := range res.Int16 {
			res.Int16[i] = reader.I16()
		}
	case QEXTDecode96kFormatInt24:
		res.Int24 = make([]int32, total)
		for i := range res.Int24 {
			res.Int24[i] = reader.I32()
		}
	default:
		res.PCM = make([]float32, total)
		for i := range res.PCM {
			res.PCM[i] = reader.Float32()
		}
	}

	nRanges := int(reader.U32())
	res.FinalRanges = make([]uint32, nRanges)
	for i := range res.FinalRanges {
		res.FinalRanges[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return QEXTDecode96kResult{}, err
	}
	return res, nil
}
