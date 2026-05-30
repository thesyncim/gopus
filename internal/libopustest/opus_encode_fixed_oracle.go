package libopustest

import "fmt"

const (
	opusEncodeFixedInputMagic  = "GOEI"
	opusEncodeFixedOutputMagic = "GOEO"
)

// Forced coding mode codes for OpusEncodeFixedParams.ForceMode. They mirror the
// libopus opus_private.h MODE_* constants consumed by OPUS_SET_FORCE_MODE.
const (
	OpusForceModeAuto     = 0
	OpusForceModeSILKOnly = 1000
	OpusForceModeHybrid   = 1001
	OpusForceModeCELTOnly = 1002
)

// Bandwidth codes for OpusEncodeFixedParams.Bandwidth. They mirror the libopus
// OPUS_BANDWIDTH_* constants consumed by OPUS_SET_BANDWIDTH. Zero leaves the
// bandwidth unset (encoder auto-selects).
const (
	OpusBandwidthAuto          = 0
	OpusBandwidthNarrowband    = 1101
	OpusBandwidthMediumband    = 1102
	OpusBandwidthWideband      = 1103
	OpusBandwidthSuperwideband = 1104
	OpusBandwidthFullband      = 1105
)

var opusEncodeFixedHelper HelperCache

func buildOpusEncodeFixedHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "opus encode fixed",
		OutputBase:  "gopus_libopus_opus_encode_fixed",
		SourceFile:  "libopus_opus_encode_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getOpusEncodeFixedHelperPath() (string, error) {
	return opusEncodeFixedHelper.Path(buildOpusEncodeFixedHelper)
}

// OpusEncodeFixedParams configures a top-level FIXED_POINT opus_encode probe.
// The reference encoder is created via opus_encoder_create(SampleRate, Channels,
// OPUS_APPLICATION_AUDIO) and driven through the int16 opus_encode API, so the
// whole resampler/SILK/CELT integer chain runs exactly as the public fixed-point
// encoder does. ForceMode (when non-zero) pins SILK/Hybrid/CELT via
// OPUS_SET_FORCE_MODE; Bandwidth (when non-zero) pins the bandwidth via
// OPUS_SET_BANDWIDTH + OPUS_SET_MAX_BANDWIDTH.
type OpusEncodeFixedParams struct {
	SampleRate    int
	Channels      int
	ForceMode     int
	Bandwidth     int
	Bitrate       int
	Complexity    int
	VBR           bool
	VBRConstraint bool
	// ForceChannels pins the coded channel count via OPUS_SET_FORCE_CHANNELS
	// (1 or 2). Zero leaves it auto, letting opus_encode pick mono/stereo per
	// frame from its own stereo-width analysis.
	ForceChannels int
	FrameSize     int // per-channel samples at SampleRate
	FrameCount    int
	// PCM is the interleaved int16 input for all frames,
	// length FrameSize*Channels*FrameCount.
	PCM []int16
}

// ProbeOpusEncodeFixed encodes the supplied int16 PCM frames through the
// FIXED_POINT libopus reference opus_encode() and returns the produced full Opus
// packets (TOC + payload). This is the top-level reference for the public
// fixed-point Encoder.
func ProbeOpusEncodeFixed(p OpusEncodeFixedParams) ([][]byte, error) {
	binPath, err := getOpusEncodeFixedHelperPath()
	if err != nil {
		return nil, err
	}
	if p.Channels < 1 || p.Channels > 2 {
		return nil, fmt.Errorf("opus encode fixed: invalid channels %d", p.Channels)
	}
	if p.FrameSize <= 0 || p.FrameCount <= 0 {
		return nil, fmt.Errorf("opus encode fixed: invalid dimensions")
	}
	nsamples := p.FrameSize * p.Channels * p.FrameCount
	if len(p.PCM) != nsamples {
		return nil, fmt.Errorf("opus encode fixed: PCM len %d want %d", len(p.PCM), nsamples)
	}

	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}

	payload := NewOraclePayloadVersion(opusEncodeFixedInputMagic, 1)
	payload.U32(uint32(p.SampleRate))
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.ForceMode))
	payload.U32(uint32(p.Bandwidth))
	payload.U32(uint32(p.Bitrate))
	payload.U32(uint32(p.Complexity))
	payload.U32(b2u(p.VBR))
	payload.U32(b2u(p.VBRConstraint))
	payload.U32(uint32(p.ForceChannels))
	payload.U32(uint32(p.FrameSize))
	payload.U32(uint32(p.FrameCount))
	payload.U32(uint32(nsamples))
	for _, s := range p.PCM {
		payload.I16(s)
	}
	if pad := (4 - (nsamples*2)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "opus encode fixed", opusEncodeFixedOutputMagic)
	if err != nil {
		return nil, err
	}
	nFrames := reader.Count(-1)
	packets := make([][]byte, nFrames)
	for i := 0; i < nFrames; i++ {
		count := int(reader.U32())
		pad := (4 - count%4) % 4
		packets[i] = append([]byte(nil), reader.Bytes(count)...)
		if pad > 0 {
			reader.Bytes(pad)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("opus encode fixed oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return packets, nil
}
