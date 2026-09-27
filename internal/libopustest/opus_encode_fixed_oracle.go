package libopustest

import (
	"fmt"
	"math"
)

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

const (
	OpusApplicationVoIP               = 2048
	OpusApplicationAudio              = 2049
	OpusApplicationRestrictedLowDelay = 2051
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

// Per-frame CELT energy-mask actions for OpusEncodeFixedMixedFrame. An
// unchanged action preserves the encoder's current control value; Clear asks
// libopus to apply the control with a nil mask pointer.
const (
	OpusEnergyMaskUnchanged uint32 = iota
	OpusEnergyMaskSet
	OpusEnergyMaskClear
)

// Per-frame Opus VBR control actions for OpusEncodeFixedMixedFrame. An
// unchanged action preserves the current encoder control.
const (
	OpusVBRUnchanged uint32 = iota
	OpusVBREnable
	OpusVBRDisable
)

var opusEncodeFixedHelper HelperCache
var opusEncodeFloatShortHelper HelperCache

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

func buildOpusEncodeFloatShortHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "opus encode float build short API",
		OutputBase:  "gopus_libopus_opus_encode_float_build_short",
		SourceFile:  "libopus_opus_encode_fixed_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeOpusEncodeFloatBuildShortRecords calls opus_encode(int16*) in the
// selected non-fixed C build, preserving the short callback's LSB-depth rule.
func ProbeOpusEncodeFloatBuildShortRecords(p OpusEncodeFixedParams) ([]OpusEncodeFixedRecord, error) {
	binPath, err := opusEncodeFloatShortHelper.Path(buildOpusEncodeFloatShortHelper)
	if err != nil {
		return nil, err
	}
	if p.LSBDepth != 0 {
		if p.FrameSize <= 0 || p.Channels <= 0 || p.FrameCount <= 0 ||
			p.FrameSize > int(^uint(0)>>1)/p.Channels/p.FrameCount ||
			len(p.PCM) != p.FrameSize*p.Channels*p.FrameCount {
			return nil, fmt.Errorf("opus encode float short: invalid dimensions")
		}
		perFrame := p.FrameSize * p.Channels
		frames := make([]OpusEncodeFixedMixedFrame, p.FrameCount)
		for i := range frames {
			frames[i].ShortPCM = p.PCM[i*perFrame : (i+1)*perFrame]
			frames[i].ResetBefore = len(p.ResetBefore) != 0 && p.ResetBefore[i]
		}
		return probeOpusEncodeMixedRecords(binPath, p, frames)
	}
	return probeOpusEncodeShortRecords(binPath, p)
}

// OpusEncodeFixedParams configures a top-level FIXED_POINT opus_encode probe.
// The reference encoder is created via opus_encoder_create(SampleRate, Channels,
// OPUS_APPLICATION_AUDIO) and driven through the int16 opus_encode API, so the
// whole resampler/SILK/CELT integer chain runs exactly as the public fixed-point
// encoder does. ForceMode (when non-zero) pins SILK/Hybrid/CELT via
// OPUS_SET_FORCE_MODE; Bandwidth (when non-zero) pins the bandwidth via
// OPUS_SET_BANDWIDTH + OPUS_SET_MAX_BANDWIDTH.
type OpusEncodeFixedParams struct {
	SampleRate int
	Channels   int
	// Application selects the libopus encoder application. Zero selects Audio.
	Application int
	// MaxPacketBytes is the caller's whole-packet output capacity. Zero uses 4000.
	MaxPacketBytes int
	ForceMode      int
	Bandwidth      int
	Bitrate        int
	Complexity     int
	VBR            bool
	VBRConstraint  bool
	// ForceChannels pins the coded channel count via OPUS_SET_FORCE_CHANNELS
	// (1 or 2). Zero leaves it auto, letting opus_encode pick mono/stereo per
	// frame from its own stereo-width analysis.
	ForceChannels int
	// LSBDepth sets OPUS_SET_LSB_DEPTH before encoding. Zero retains the C default.
	LSBDepth int
	// ExpertFrameDuration sets OPUS_SET_EXPERT_FRAME_DURATION before encoding.
	// Zero keeps OPUS_FRAMESIZE_ARG.
	ExpertFrameDuration int
	// LFE sets OPUS_SET_LFE before any frame is encoded.
	LFE        bool
	FrameSize  int // per-channel samples at SampleRate
	FrameCount int
	// PCM is the interleaved int16 input for all frames,
	// length FrameSize*Channels*FrameCount.
	PCM []int16
	// ResetBefore requests OPUS_RESET_STATE before the corresponding frame.
	// A nil slice means no resets.
	ResetBefore []bool
}

// OpusEncodeFixedRecord is one opus_encode call, including an empty or failed
// call, its complete packet, and OPUS_GET_FINAL_RANGE after successful encode.
type OpusEncodeFixedRecord struct {
	Status     int32
	Packet     []byte
	FinalRange uint32
}

// OpusEncodeFixedMixedFrame selects one public input API on a persistent
// selected fixed-point OpusEncoder. Exactly one PCM field is used per frame.
type OpusEncodeFixedMixedFrame struct {
	Format      uint32 // 0=opus_encode, 1=opus_encode_float, 2=opus_encode24
	ShortPCM    []int16
	FloatPCM    []float32
	PCM24       []int32
	ResetBefore bool
	VBRAction   uint32 // OpusVBRUnchanged, OpusVBREnable, or OpusVBRDisable.
	// Nonzero per-frame values override the global controls using libopus
	// MODE_* and OPUS_BANDWIDTH_* values, respectively.
	ForceMode int
	Bandwidth int
	// EnergyMaskAction is one of OpusEnergyMaskUnchanged, OpusEnergyMaskSet, or
	// OpusEnergyMaskClear. Set supplies channels*21 Q24 celt_glog values.
	EnergyMaskAction uint32
	EnergyMask       []int32
}

// ProbeOpusEncodeFixedMixedRecords alternates the three public input APIs on
// one selected FIXED_POINT+ENABLE_RES24 C encoder and returns every call.
func ProbeOpusEncodeFixedMixedRecords(p OpusEncodeFixedParams, frames []OpusEncodeFixedMixedFrame) ([]OpusEncodeFixedRecord, error) {
	binPath, err := getOpusEncodeFixedHelperPath()
	if err != nil {
		return nil, err
	}
	return probeOpusEncodeMixedRecords(binPath, p, frames)
}

// ProbeOpusEncodeFloatBuildMixedRecords exercises all public input APIs on one
// selected float-build C encoder, including their distinct LSB-depth policy.
func ProbeOpusEncodeFloatBuildMixedRecords(p OpusEncodeFixedParams, frames []OpusEncodeFixedMixedFrame) ([]OpusEncodeFixedRecord, error) {
	binPath, err := opusEncodeFloatShortHelper.Path(buildOpusEncodeFloatShortHelper)
	if err != nil {
		return nil, err
	}
	return probeOpusEncodeMixedRecords(binPath, p, frames)
}

func probeOpusEncodeMixedRecords(binPath string, p OpusEncodeFixedParams, frames []OpusEncodeFixedMixedFrame) ([]OpusEncodeFixedRecord, error) {
	if p.Channels < 1 || p.Channels > 2 || p.FrameSize <= 0 || len(frames) == 0 ||
		p.FrameSize > int(^uint(0)>>1)/p.Channels/len(frames) {
		return nil, fmt.Errorf("opus encode fixed mixed: invalid dimensions")
	}
	perFrame := p.FrameSize * p.Channels
	nsamples := perFrame * len(frames)
	application := p.Application
	if application == 0 {
		application = OpusApplicationAudio
	}
	maxBytes := p.MaxPacketBytes
	if maxBytes == 0 {
		maxBytes = 4000
	}
	if maxBytes < 1 || maxBytes > 4000 {
		return nil, fmt.Errorf("opus encode fixed mixed: invalid output cap %d", maxBytes)
	}
	version := uint32(3)
	if p.LSBDepth != 0 {
		if p.LSBDepth < 8 || p.LSBDepth > 24 {
			return nil, fmt.Errorf("opus encode mixed: invalid LSB depth %d", p.LSBDepth)
		}
		version = 4
	}
	if p.ExpertFrameDuration != 0 {
		version = 5
	}
	for _, frame := range frames {
		if frame.ForceMode != 0 || frame.Bandwidth != 0 {
			if version < 6 {
				version = 6
			}
		}
		if frame.EnergyMaskAction != OpusEnergyMaskUnchanged {
			if version < 7 {
				version = 7
			}
		}
		if frame.VBRAction != OpusVBRUnchanged {
			version = 8
		}
	}
	if p.LFE {
		if version < 7 {
			version = 7
		}
	}
	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}
	payload := NewOraclePayloadVersion(opusEncodeFixedInputMagic, version)
	for _, v := range []uint32{
		uint32(p.SampleRate), uint32(p.Channels), uint32(p.ForceMode), uint32(p.Bandwidth),
		uint32(p.Bitrate), uint32(p.Complexity), b2u(p.VBR), b2u(p.VBRConstraint),
		uint32(p.ForceChannels), uint32(p.FrameSize), uint32(len(frames)), uint32(nsamples),
		uint32(application), uint32(maxBytes), 3, // per-frame public input format.
	} {
		payload.U32(v)
	}
	if version >= 4 {
		payload.U32(uint32(p.LSBDepth))
	}
	if version >= 5 {
		payload.U32(uint32(p.ExpertFrameDuration))
	}
	for i, frame := range frames {
		switch frame.Format {
		case 0:
			if len(frame.ShortPCM) != perFrame {
				return nil, fmt.Errorf("opus encode fixed mixed: frame %d short PCM length", i)
			}
			for _, sample := range frame.ShortPCM {
				payload.U32(uint32(int32(sample)))
			}
		case 1:
			if len(frame.FloatPCM) != perFrame {
				return nil, fmt.Errorf("opus encode fixed mixed: frame %d float PCM length", i)
			}
			for _, sample := range frame.FloatPCM {
				payload.U32(math.Float32bits(sample))
			}
		case 2:
			if len(frame.PCM24) != perFrame {
				return nil, fmt.Errorf("opus encode fixed mixed: frame %d int24 PCM length", i)
			}
			for _, sample := range frame.PCM24 {
				payload.I32(sample)
			}
		default:
			return nil, fmt.Errorf("opus encode fixed mixed: frame %d invalid format %d", i, frame.Format)
		}
		if frame.EnergyMaskAction > OpusEnergyMaskClear {
			return nil, fmt.Errorf("opus encode fixed mixed: frame %d invalid energy-mask action %d", i, frame.EnergyMaskAction)
		}
		if frame.VBRAction > OpusVBRDisable {
			return nil, fmt.Errorf("opus encode fixed mixed: frame %d invalid VBR action %d", i, frame.VBRAction)
		}
		if frame.EnergyMaskAction == OpusEnergyMaskSet {
			if len(frame.EnergyMask) != p.Channels*21 {
				return nil, fmt.Errorf("opus encode fixed mixed: frame %d energy-mask length=%d want=%d", i, len(frame.EnergyMask), p.Channels*21)
			}
		} else if len(frame.EnergyMask) != 0 {
			return nil, fmt.Errorf("opus encode fixed mixed: frame %d energy mask requires set action", i)
		}
	}
	for _, frame := range frames {
		flags := b2u(frame.ResetBefore)
		if version >= 8 {
			flags |= frame.VBRAction << 1
		}
		payload.U32(flags)
	}
	for _, frame := range frames {
		payload.U32(frame.Format)
	}
	if version >= 6 {
		for _, frame := range frames {
			payload.U32(uint32(frame.ForceMode))
			payload.U32(uint32(frame.Bandwidth))
		}
	}
	if version >= 7 {
		payload.U32(b2u(p.LFE))
		for _, frame := range frames {
			payload.U32(frame.EnergyMaskAction)
			if frame.EnergyMaskAction == OpusEnergyMaskSet {
				for _, value := range frame.EnergyMask {
					payload.I32(value)
				}
			}
		}
	}
	reader, err := RunOracleVersion(binPath, payload.Bytes(), "opus encode mixed records", opusEncodeFixedOutputMagic, version)
	if err != nil {
		return nil, err
	}
	return parseOpusEncodeFixedRecords(reader, len(frames), maxBytes)
}

// ProbeOpusEncodeFixedRecords drives the selected FIXED_POINT+ENABLE_RES24
// reference with identical signed-16 input and public output capacity. The
// result preserves one record per call, including DTX or error records.
func ProbeOpusEncodeFixedRecords(p OpusEncodeFixedParams) ([]OpusEncodeFixedRecord, error) {
	binPath, err := getOpusEncodeFixedHelperPath()
	if err != nil {
		return nil, err
	}
	return probeOpusEncodeShortRecords(binPath, p)
}

func probeOpusEncodeShortRecords(binPath string, p OpusEncodeFixedParams) ([]OpusEncodeFixedRecord, error) {
	if p.Channels < 1 || p.Channels > 2 || p.FrameSize <= 0 || p.FrameCount <= 0 ||
		p.FrameSize > int(^uint(0)>>1)/p.Channels/p.FrameCount {
		return nil, fmt.Errorf("opus encode fixed: invalid dimensions")
	}
	nsamples := p.FrameSize * p.Channels * p.FrameCount
	if len(p.PCM) != nsamples || (len(p.ResetBefore) != 0 && len(p.ResetBefore) != p.FrameCount) {
		return nil, fmt.Errorf("opus encode fixed: PCM/reset length mismatch")
	}
	application := p.Application
	if application == 0 {
		application = OpusApplicationAudio
	}
	maxBytes := p.MaxPacketBytes
	if maxBytes == 0 {
		maxBytes = 4000
	}
	if maxBytes < 1 || maxBytes > 4000 {
		return nil, fmt.Errorf("opus encode fixed: invalid output cap %d", maxBytes)
	}
	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}
	payload := NewOraclePayloadVersion(opusEncodeFixedInputMagic, 2)
	for _, v := range []uint32{
		uint32(p.SampleRate), uint32(p.Channels), uint32(p.ForceMode), uint32(p.Bandwidth),
		uint32(p.Bitrate), uint32(p.Complexity), b2u(p.VBR), b2u(p.VBRConstraint),
		uint32(p.ForceChannels), uint32(p.FrameSize), uint32(p.FrameCount), uint32(nsamples),
		uint32(application), uint32(maxBytes), 0, // input_format=0: signed-16 API.
	} {
		payload.U32(v)
	}
	for _, sample := range p.PCM {
		payload.I16(sample)
	}
	if pad := (4 - (nsamples*2)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}
	for i := range p.FrameCount {
		reset := len(p.ResetBefore) != 0 && p.ResetBefore[i]
		payload.U32(b2u(reset))
	}
	reader, err := RunOracleVersion(binPath, payload.Bytes(), "opus encode fixed records", opusEncodeFixedOutputMagic, 2)
	if err != nil {
		return nil, err
	}
	return parseOpusEncodeFixedRecords(reader, p.FrameCount, maxBytes)
}

func parseOpusEncodeFixedRecords(reader *OracleReader, frameCount, maxBytes int) ([]OpusEncodeFixedRecord, error) {
	if got := reader.Count(frameCount); got != frameCount {
		return nil, fmt.Errorf("opus encode fixed: record count=%d want=%d", got, frameCount)
	}
	records := make([]OpusEncodeFixedRecord, frameCount)
	for i := range records {
		records[i].Status = int32(reader.U32())
		count := int(reader.U32())
		records[i].FinalRange = reader.U32()
		if count < 0 || count > maxBytes {
			return nil, fmt.Errorf("opus encode fixed: record %d packet length=%d", i, count)
		}
		records[i].Packet = append([]byte(nil), reader.Bytes(count)...)
		if pad := (4 - count%4) % 4; pad > 0 {
			reader.Bytes(pad)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return records, nil
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
	for i := range nFrames {
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
