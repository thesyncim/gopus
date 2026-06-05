package libopustest

import "fmt"

const (
	encodeDiffInputMagic  = "GEDI"
	encodeDiffOutputMagic = "GEDO"
)

// EncodeDiff force-mode codes (opus_private.h MODE_* consumed by
// OPUS_SET_FORCE_MODE). Zero leaves the mode auto.
const (
	EncodeDiffForceModeAuto     = 0
	EncodeDiffForceModeSILKOnly = 1000
	EncodeDiffForceModeHybrid   = 1001
	EncodeDiffForceModeCELTOnly = 1002
)

// EncodeDiff bandwidth codes (OPUS_BANDWIDTH_*). Zero leaves the bandwidth
// unset (encoder auto-selects).
const (
	EncodeDiffBandwidthAuto          = 0
	EncodeDiffBandwidthNarrowband    = 1101
	EncodeDiffBandwidthMediumband    = 1102
	EncodeDiffBandwidthWideband      = 1103
	EncodeDiffBandwidthSuperwideband = 1104
	EncodeDiffBandwidthFullband      = 1105
)

// EncodeDiff signal codes (OPUS_SIGNAL_*). OPUS_AUTO (-1000) is the two's
// complement uint32.
const (
	EncodeDiffSignalAuto  = uint32(0xFFFFFC18) // OPUS_AUTO = -1000
	EncodeDiffSignalVoice = uint32(3001)
	EncodeDiffSignalMusic = uint32(3002)
)

// EncodeDiff application codes (OPUS_APPLICATION_*).
const (
	EncodeDiffApplicationVoIP         = 2048
	EncodeDiffApplicationAudio        = 2049
	EncodeDiffApplicationRestrictedLD = 2051
)

var encodeDiffHelper HelperCache

func buildEncodeDiffHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "opus encode diff float",
		OutputBase:  "gopus_libopus_encode_diff",
		SourceFile:  "libopus_encode_diff_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getEncodeDiffHelperPath() (string, error) {
	return encodeDiffHelper.Path(buildEncodeDiffHelper)
}

// EncodeDiffHelperPath returns the built float encode-diff oracle path (building
// it on first use) so tests can detect availability before running the sweep.
func EncodeDiffHelperPath() (string, error) {
	return getEncodeDiffHelperPath()
}

// EncodeDiffParams configures the comprehensive FLOAT opus_encode_float oracle.
// One encoder is created and driven statefully across all frames (no reset).
type EncodeDiffParams struct {
	SampleRate         int
	Channels           int
	Application        int
	ForceMode          int
	Bandwidth          int
	MaxBandwidth       int
	Bitrate            int
	Complexity         int
	Signal             uint32
	VBR                bool
	VBRConstraint      bool
	ForceChannels      int
	InbandFEC          int
	PacketLoss         int
	DTX                bool
	LSBDepth           int
	PredictionDisabled bool
	PhaseInvDisabled   bool
	FrameSize          int // per-channel samples at SampleRate
	FrameCount         int
	// PCM is interleaved float32, length FrameSize*Channels*FrameCount.
	PCM []float32
}

// EncodeDiffRecord is one frame's oracle result: opus_encode_float's return code
// Ret (>0 packet len, 1 = DTX/CELT-silence TOC-only, 0 = DTX no-output, <0 error),
// the post-encode final range, and the produced packet bytes (nil when Ret<=0).
type EncodeDiffRecord struct {
	Ret        int
	FinalRange uint32
	Packet     []byte
}

// ProbeEncodeDiff encodes the supplied float PCM frames through the default
// (float) libopus opus_encode_float() and returns per-frame records. This is the
// byte-exact oracle for the public float Encoder on the same architecture.
func ProbeEncodeDiff(p EncodeDiffParams) ([]EncodeDiffRecord, error) {
	binPath, err := getEncodeDiffHelperPath()
	if err != nil {
		return nil, err
	}
	if p.Channels < 1 || p.Channels > 2 {
		return nil, fmt.Errorf("encode diff: invalid channels %d", p.Channels)
	}
	if p.FrameSize <= 0 || p.FrameCount <= 0 {
		return nil, fmt.Errorf("encode diff: invalid dimensions")
	}
	nsamples := p.FrameSize * p.Channels * p.FrameCount
	if len(p.PCM) != nsamples {
		return nil, fmt.Errorf("encode diff: PCM len %d want %d", len(p.PCM), nsamples)
	}

	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}

	payload := NewOraclePayloadVersion(encodeDiffInputMagic, 1)
	payload.U32(uint32(p.SampleRate))
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.Application))
	payload.U32(uint32(p.ForceMode))
	payload.U32(uint32(p.Bandwidth))
	payload.U32(uint32(p.MaxBandwidth))
	payload.U32(uint32(p.Bitrate))
	payload.U32(uint32(p.Complexity))
	payload.U32(p.Signal)
	payload.U32(b2u(p.VBR))
	payload.U32(b2u(p.VBRConstraint))
	payload.U32(uint32(p.ForceChannels))
	payload.U32(uint32(p.InbandFEC))
	payload.U32(uint32(p.PacketLoss))
	payload.U32(b2u(p.DTX))
	payload.U32(uint32(p.LSBDepth))
	payload.U32(b2u(p.PredictionDisabled))
	payload.U32(b2u(p.PhaseInvDisabled))
	payload.U32(uint32(p.FrameSize))
	payload.U32(uint32(p.FrameCount))
	payload.U32(uint32(nsamples))
	for _, s := range p.PCM {
		payload.Float32(s)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "opus encode diff", encodeDiffOutputMagic)
	if err != nil {
		return nil, err
	}
	n := reader.Count(p.FrameCount)
	recs := make([]EncodeDiffRecord, n)
	for i := range n {
		ret := int(int32(reader.U32()))
		fr := reader.U32()
		plen := int(reader.U32())
		var pkt []byte
		if plen > 0 {
			pkt = append([]byte(nil), reader.Bytes(plen)...)
			if pad := (4 - plen%4) % 4; pad > 0 {
				reader.Bytes(pad)
			}
		}
		recs[i] = EncodeDiffRecord{Ret: ret, FinalRange: fr, Packet: pkt}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("encode diff oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}
