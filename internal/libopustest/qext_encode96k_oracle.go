package libopustest

import "fmt"

const (
	qextEncode96kInputMagic  = "GQEI"
	qextEncode96kOutputMagic = "GQEO"
)

var qextEncode96kHelper HelperCache

func buildQEXTEncode96kHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "qext encode96k",
		OutputBase:  "gopus_libopus_qext_encode96k",
		SourceFile:  "libopus_qext_encode96k_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getQEXTEncode96kHelperPath() (string, error) {
	return qextEncode96kHelper.Path(buildQEXTEncode96kHelper)
}

// QEXTEncode96kParams configures a native 96 kHz QEXT full-packet encode probe.
// The reference encoder is created with opus_encoder_create(96000, channels,
// OPUS_APPLICATION_RESTRICTED_LOWDELAY) plus OPUS_SET_QEXT(1) and CELT-only /
// fullband forcing, so it runs the native 96 kHz CELT mode and the >20 kHz
// extension-band encode chain.
type QEXTEncode96kParams struct {
	Channels      int
	FrameSize     int // per-channel samples at 96 kHz (1920 for 20 ms)
	Bitrate       int
	Complexity    int
	VBR           bool
	MaxPacketSize int
	// PCM is the interleaved native 96 kHz float input for all frames,
	// length FrameSize*Channels*FrameCount.
	PCM        []float32
	FrameCount int
}

// QEXTEncode96kResult holds the produced native 96 kHz QEXT Opus packets and
// the per-frame final range from the reference encoder.
type QEXTEncode96kResult struct {
	Packets     [][]byte
	FinalRanges []uint32
}

// ProbeQEXTEncode96k encodes the supplied native 96 kHz float PCM frames through
// the QEXT-enabled libopus reference at Fs=96000 and returns the produced Opus
// packets plus the per-frame OPUS_GET_FINAL_RANGE values.
func ProbeQEXTEncode96k(p QEXTEncode96kParams) (QEXTEncode96kResult, error) {
	binPath, err := getQEXTEncode96kHelperPath()
	if err != nil {
		return QEXTEncode96kResult{}, err
	}
	if p.Channels < 1 || p.Channels > 2 {
		return QEXTEncode96kResult{}, fmt.Errorf("qext encode96k: invalid channels %d", p.Channels)
	}
	if p.FrameSize <= 0 || p.FrameCount <= 0 || p.MaxPacketSize <= 0 {
		return QEXTEncode96kResult{}, fmt.Errorf("qext encode96k: invalid dimensions")
	}
	want := p.FrameSize * p.Channels * p.FrameCount
	if len(p.PCM) != want {
		return QEXTEncode96kResult{}, fmt.Errorf("qext encode96k: PCM len %d want %d", len(p.PCM), want)
	}

	vbr := uint32(0)
	if p.VBR {
		vbr = 1
	}

	payload := NewOraclePayloadVersion(qextEncode96kInputMagic, 1)
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.FrameSize))
	payload.U32(uint32(p.Bitrate))
	payload.U32(uint32(p.Complexity))
	payload.U32(vbr)
	payload.U32(uint32(p.MaxPacketSize))
	payload.U32(uint32(p.FrameCount))
	for _, s := range p.PCM {
		payload.Float32(s)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "qext encode96k", qextEncode96kOutputMagic)
	if err != nil {
		return QEXTEncode96kResult{}, err
	}

	nFrames := int(reader.U32())
	res := QEXTEncode96kResult{Packets: make([][]byte, nFrames)}
	for i := range nFrames {
		count := int(reader.U32())
		res.Packets[i] = append([]byte(nil), reader.Bytes(count)...)
		if pad := (4 - count%4) % 4; pad > 0 {
			reader.Bytes(pad)
		}
	}
	nRanges := int(reader.U32())
	res.FinalRanges = make([]uint32, nRanges)
	for i := range res.FinalRanges {
		res.FinalRanges[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return QEXTEncode96kResult{}, fmt.Errorf("qext encode96k oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return QEXTEncode96kResult{}, err
	}
	return res, nil
}
