package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/types"
)

// SetMode sets the encoding mode.
func (e *Encoder) SetMode(mode Mode) {
	e.mode = mode
}

// Mode returns the configured encoding mode. ModeAuto lets the encoder select
// a coding mode for each frame.
func (e *Encoder) Mode() Mode {
	return e.mode
}

// FirstFrameCoded reports whether a frame has been committed since the encoder
// was created or reset, mirroring libopus !st->first.
//
// C ref: opus_encoder.c clears st->first = 0 only after a frame reaches the end
// of opus_encode_native (line 2562), AFTER the SILK nBytes==0 early return
// (line 2242) which leaves st->first = 1. OPUS_SET_APPLICATION uses
// !st->first to reject an application change once a frame has been coded.
func (e *Encoder) FirstFrameCoded() bool {
	return !e.first
}

// SetLowDelay toggles low-delay application behavior.
//
// When enabled, CELT delay compensation is disabled to match restricted
// low-delay semantics.
func (e *Encoder) SetLowDelay(enabled bool) {
	e.lowDelay = enabled
}

// LowDelay reports whether low-delay application behavior is enabled.
func (e *Encoder) LowDelay() bool {
	return e.lowDelay
}

// SetVoIPApplication toggles VoIP application bias for mode decisions.
func (e *Encoder) SetVoIPApplication(enabled bool) {
	e.voipApp = enabled
}

// VoIPApplication reports whether VoIP application bias is enabled.
func (e *Encoder) VoIPApplication() bool {
	return e.voipApp
}

// SetRestrictedSilkApplication toggles restricted-SILK application behavior.
func (e *Encoder) SetRestrictedSilkApplication(enabled bool) {
	e.restrictedSilkApp = enabled
}

// SetVoiceRatio sets the private libopus voice-ratio control.
func (e *Encoder) SetVoiceRatio(ratio int) error {
	if ratio < -1 || ratio > 100 {
		return ErrInvalidVoiceRatio
	}
	e.voiceRatio = int32(ratio)
	return nil
}

// VoiceRatio returns the current private libopus voice-ratio control value.
func (e *Encoder) VoiceRatio() int {
	return int(e.voiceRatio)
}

// SetBandwidth sets the target audio bandwidth.
func (e *Encoder) SetBandwidth(bandwidth types.Bandwidth) {
	// C ref: opus_encoder.c OPUS_SET_BANDWIDTH writes only st->user_bandwidth;
	// st->bandwidth (the decided value reported by OPUS_GET_BANDWIDTH) is left
	// untouched and recomputed during encode. Keep e.bandwidth as the decided
	// value so the getter mirrors libopus get-after-set.
	e.userBandwidth = bandwidth
	e.userBandwidthSet = true
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// SetBandwidthAuto clears an explicit bandwidth request and restores automatic selection.
func (e *Encoder) SetBandwidthAuto() {
	e.userBandwidth = 0
	e.userBandwidthSet = false
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() types.Bandwidth {
	return e.bandwidth
}

// DNNBlobLoaded reports whether a validated model blob is retained.
func (e *Encoder) DNNBlobLoaded() bool {
	return e.dnnBlob != nil
}

// frame20ms returns the number of native-Fs samples in a 20 ms frame (Fs/50).
// This is the libopus opus_encode_native multi-frame split unit and the upper
// bound for a single CELT/Hybrid encode (longer frames are split). At 48 kHz it
// is 960 samples, the standard 20 ms frame size.
func (e *Encoder) frame20ms() int {
	return int(e.sampleRate) / 50
}

// isMultiFramePacket reports whether the given mode/frameSize is encoded as an
// Opus multi-frame packet (N internal 20ms — or for SILK 20/40/60ms — sub-frames
// repacketized together). This mirrors libopus opus_encode_native's condition at
// opus_encoder.c:1698: any >20ms CELT/Hybrid packet, or any SILK packet >60ms.
// For these packets the per-sub-frame DTX decision and Opus-level activity are
// handled inside the encode*MultiFramePacket loop, not at the whole-frame level.
func (e *Encoder) isMultiFramePacket(mode Mode, frameSize int) bool {
	f20 := e.frame20ms()
	if frameSize <= 0 || f20 <= 0 || frameSize%f20 != 0 {
		return false
	}
	switch mode {
	case ModeCELT, ModeHybrid:
		return frameSize > f20
	case ModeSILK:
		return frameSize > 3*f20
	default:
		return false
	}
}

// multiFrameSubframeCount returns how many internal sub-frames a multi-frame
// packet of this mode/frameSize is split into, matching the encode loop counts:
// CELT/Hybrid split into frameSize/20ms sub-frames; SILK splits 80ms->2x40ms,
// 100ms->5x20ms, 120ms->2x60ms (libopus opus_encoder.c:1713-1725). Returns 0 if
// the packet is not a multi-frame packet.
func (e *Encoder) multiFrameSubframeCount(mode Mode, frameSize int) int {
	if !e.isMultiFramePacket(mode, frameSize) {
		return 0
	}
	f20 := e.frame20ms()
	switch mode {
	case ModeCELT, ModeHybrid:
		return frameSize / f20
	case ModeSILK:
		switch frameSize {
		case 4 * f20: // 80 ms -> 2x40 ms
			return 2
		case 5 * f20: // 100 ms -> 5x20 ms
			return 5
		case 6 * f20: // 120 ms -> 2x60 ms
			return 2
		}
	}
	return 0
}

// SetFrameSize stores the per-channel frame size in samples at the configured
// input sample rate.
func (e *Encoder) SetFrameSize(frameSize int) {
	e.frameSize = int32(frameSize)
}

// FrameSize returns the configured per-channel frame size in samples at the
// input sample rate.
func (e *Encoder) FrameSize() int {
	return int(e.frameSize)
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return int(e.channels)
}

// SampleRate returns the input sample rate.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// SetFEC enables or disables in-band Forward Error Correction.
func (e *Encoder) SetFEC(enabled bool) {
	config := InBandFECDisabled
	if enabled {
		config = InBandFECEnabled
	}
	_ = e.SetInBandFEC(config)
}

// SetInBandFEC sets the libopus-compatible in-band FEC configuration.
func (e *Encoder) SetInBandFEC(config int) error {
	if config < InBandFECDisabled || config > InBandFECMusicSafe {
		return ErrInvalidFECConfig
	}
	e.fecConfig = int32(config)
	e.fecEnabled = config != InBandFECDisabled
	if e.fecEnabled && e.fec == nil {
		e.fec = newFECState()
	}
	return nil
}

// FECEnabled returns whether FEC is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.fecEnabled
}

// InBandFEC returns the in-band FEC configuration.
func (e *Encoder) InBandFEC() int {
	return int(e.fecConfig)
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLoss = int32(lossPercent)
	if e.celtEncoder != nil {
		e.celtEncoder.SetPacketLoss(int(e.packetLoss))
	}
}

// PacketLoss returns the expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return int(e.packetLoss)
}

// SetDTX enables or disables Discontinuous Transmission.
func (e *Encoder) SetDTX(enabled bool) {
	e.dtxEnabled = enabled
	if enabled && e.dtx == nil {
		e.dtx = newDTXState()
	}
}

// DTXEnabled returns whether DTX is enabled.
func (e *Encoder) DTXEnabled() bool {
	return e.dtxEnabled
}

// SetComplexity sets encoder complexity (0-10).
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = int32(complexity)
	if e.celtEncoder != nil {
		e.celtEncoder.SetComplexity(complexity)
	}
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return int(e.complexity)
}

// FinalRange returns the final range coder state after encoding.
func (e *Encoder) FinalRange() uint32 {
	return e.finalRange
}

// SetBitrateMode sets the bitrate mode (VBR, CVBR, or CBR).
func (e *Encoder) SetBitrateMode(mode BitrateMode) {
	switch mode {
	case ModeCBR:
		e.useVBR = false
	case ModeCVBR:
		e.useVBR = true
		e.vbrConstraint = true
	case ModeVBR:
		e.useVBR = true
		e.vbrConstraint = false
	default:
		e.useVBR = true
		e.vbrConstraint = false
	}
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// BitrateMode returns the current bitrate mode.
func (e *Encoder) GetBitrateMode() BitrateMode {
	return modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// SetVBR enables/disables VBR while preserving the existing constraint setting.
func (e *Encoder) SetVBR(enabled bool) {
	e.useVBR = enabled
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// VBR reports whether VBR is enabled.
func (e *Encoder) VBR() bool {
	return e.useVBR
}

// SetVBRConstraint toggles VBR constraint without forcing VBR on/off.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	e.vbrConstraint = constrained
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// VBRConstraint reports whether constrained VBR is enabled.
func (e *Encoder) VBRConstraint() bool {
	return e.vbrConstraint
}

func modeFromVBRFlags(useVBR, vbrConstraint bool) BitrateMode {
	if !useVBR {
		return ModeCBR
	}
	if vbrConstraint {
		return ModeCVBR
	}
	return ModeVBR
}

// SetBitrate sets the target bitrate in bits per second.
func (e *Encoder) SetBitrate(bitrate int) {
	e.bitrate = int32(clampBitrateForChannels(bitrate, int(e.channels)))
}

// SetAllocatedBitrate sets a bitrate allocated by the multistream encoder.
func (e *Encoder) SetAllocatedBitrate(bitrate int) {
	e.bitrate = int32(clampAllocatedBitrate(bitrate, int(e.channels)))
}

// Bitrate returns the current target bitrate.
func (e *Encoder) Bitrate() int {
	return int(e.bitrate)
}

func (e *Encoder) resolvedBitrateForFrame(frameSize, maxDataBytes int) int {
	return resolveUserBitrate(int(e.bitrate), int(e.sampleRate), int(e.channels), frameSize, maxDataBytes)
}

func (e *Encoder) maxRateForFrame(frameSize, maxDataBytes int) int {
	if frameSize <= 0 || maxDataBytes <= 0 {
		return 0
	}
	return maxDataBytes * 8 * int(e.sampleRate) / frameSize
}

// bitrateToBitsFs mirrors libopus celt.h bitrate_to_bits():
// bitrate*6/(6*Fs/frame_size).
func bitrateToBitsFs(bitrate, fs, frameSize int) int {
	d := 6 * fs / frameSize
	if d == 0 {
		return 0
	}
	return bitrate * 6 / d
}

// bitsToBitrateFs mirrors libopus celt.h bits_to_bitrate(): bits*(6*Fs/frame_size)/6.
func bitsToBitrateFs(bits, fs, frameSize int) int {
	return bits * (6 * fs / frameSize) / 6
}

// silkTotalBitrate returns the total_bitRate opus_encode_frame_native splits
// between SILK and CELT: the frame budget bits_target =
// IMIN(8*(max_data_bytes-redundancy_bytes), bitrate_to_bits(bitrate_bps)) - 8
// (src/opus_encoder.c:1960), which reserves the TOC byte, converted back to a
// rate by bits_to_bitrate (src/opus_encoder.c:2051). maxDataBytes is the
// frame's orig_max_data_bytes; the 1276-byte cap is applied here.
func (e *Encoder) silkTotalBitrate(frameSize, maxDataBytes, redundancyBytes int) int {
	sampleRate := int(e.sampleRate)
	maxDataBytes = min(maxDataBytes, libopusMaxDataBytesCap)
	bitsTarget := min(8*(maxDataBytes-redundancyBytes), bitrateToBitsFs(int(e.bitrate), sampleRate, frameSize)) - 8
	return bitsToBitrateFs(bitsTarget, sampleRate, frameSize)
}

// computeEquivRate calculates the equivalent bitrate based on frame rate, VBR mode,
// complexity, and packet loss. Matches libopus compute_equiv_rate().
func (e *Encoder) computeEquivRate(bitrate, channels, frameRate int32, vbr bool, actualMode Mode, complexity, loss int32) int32 {
	equiv := bitrate
	if frameRate > 50 {
		equiv -= (40*channels + 20) * (frameRate - 50)
	}
	if !vbr {
		equiv -= equiv / 12
	}
	equiv = (equiv * (90 + complexity)) / 100
	switch actualMode {
	case ModeSILK, ModeHybrid:
		if complexity < 2 {
			equiv = (equiv * 4) / 5
		}
		if loss > 0 {
			equiv -= (equiv * loss) / (6*loss + 10)
		}
	case ModeCELT:
		if complexity < 5 {
			equiv = (equiv * 9) / 10
		}
	default:
		// Mode not known yet: libopus applies half the SILK packet-loss penalty.
		if loss > 0 {
			equiv -= (equiv * loss) / (12*loss + 20)
		}
	}
	return equiv
}

// SetSignalType sets the signal type hint for mode selection.
func (e *Encoder) SetSignalType(signal types.Signal) {
	e.signalType = signal
}

// SignalType returns the current signal type hint.
func (e *Encoder) SignalType() types.Signal {
	return e.signalType
}

// LastOpusVADProb returns the last Opus-level VAD probability (0..1).
func (e *Encoder) LastOpusVADProb() float32 {
	return e.lastOpusVADProb
}

// LastOpusVADActive returns whether the Opus-level VAD classified the last frame as active.
func (e *Encoder) LastOpusVADActive() bool {
	return e.lastOpusVADActive
}

// SetMaxBandwidth sets the maximum bandwidth limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	e.maxBandwidth = bw
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// MaxBandwidth returns the maximum bandwidth limit.
func (e *Encoder) MaxBandwidth() types.Bandwidth {
	return e.maxBandwidth
}

// SetForceChannels sets the forced channel count.
func (e *Encoder) SetForceChannels(channels int) {
	e.forceChannels = int32(channels)
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *Encoder) ForceChannels() int {
	return int(e.forceChannels)
}

// SetLFE enables or disables LFE mode.
func (e *Encoder) SetLFE(enabled bool) {
	e.lfe = enabled
	e.setFixedCELTLFE(enabled)
	if e.celtEncoder != nil {
		e.celtEncoder.SetLFE(enabled)
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// LFE reports whether LFE mode is enabled.
func (e *Encoder) LFE() bool {
	return e.lfe
}

// Lookahead returns the encoder's algorithmic delay in samples at its
// configured input sample rate.
func (e *Encoder) Lookahead() int {
	baseLookahead := int(e.sampleRate) / 400
	if e.lowDelay {
		return baseLookahead
	}
	delayComp := int(e.sampleRate) / 250
	return baseLookahead + delayComp
}

// SetLSBDepth sets the input signal's LSB depth (8-24 bits).
func (e *Encoder) SetLSBDepth(depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	e.lsbDepth = int32(depth)
	if e.analyzer != nil {
		e.analyzer.SetLSBDepth(depth)
	}
}

// LSBDepth returns the current LSB depth setting.
func (e *Encoder) LSBDepth() int {
	return int(e.lsbDepth)
}

// SetFloatInputFrame exposes the current public float32 frame to the encoder hot
// path so analysis can consume it directly and 24-bit quantization can skip a
// no-op round-trip.
func (e *Encoder) SetFloatInputFrame(pcm []float32) {
	e.floatInputFrame = pcm
	e.floatInputExact = pcm != nil
}

// ClearFloatInputFrame clears the per-call float32 input override.
func (e *Encoder) ClearFloatInputFrame() {
	e.floatInputFrame = nil
	e.floatInputExact = false
}

// SetPredictionDisabled disables inter-frame prediction
// (silk_mode.reducedDependency). CELT picks it up at the next CELT or hybrid
// frame (src/opus_encoder.c:2288-2295).
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
}

// PredictionDisabled returns whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	return e.predictionDisabled
}

// SetPhaseInversionDisabled disables stereo phase inversion.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	if e.restrictedSilkApp {
		return
	}
	e.phaseInversionDisabled = disabled
	if e.celtEncoder != nil {
		e.celtEncoder.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	if e.restrictedSilkApp {
		return false
	}
	return e.phaseInversionDisabled
}

// SetCELTEnergyMask sets per-band CELT surround masking (21 mono, 42 stereo).
func (e *Encoder) SetCELTEnergyMask(mask []float32) {
	needed := celt.MaxBands * int(e.channels)
	if needed <= 0 || len(mask) < needed {
		if len(e.celtEnergyMask) > 0 {
			clear(e.celtEnergyMask)
			e.celtEnergyMask = e.celtEnergyMask[:0]
		}
		if e.celtEncoder != nil {
			e.celtEncoder.SetEnergyMask(nil)
		}
		e.syncFixedCELTEnergyMask()
		return
	}
	if cap(e.celtEnergyMask) < needed {
		e.celtEnergyMask = make([]float32, needed)
	} else {
		e.celtEnergyMask = e.celtEnergyMask[:needed]
	}
	copy(e.celtEnergyMask, mask[:needed])
	e.syncCELTEnergyMask()
	e.syncFixedCELTEnergyMask()
}

// CELTEnergyMask returns the current CELT energy mask.
func (e *Encoder) CELTEnergyMask() []float32 {
	out := make([]float32, len(e.celtEnergyMask))
	copy(out, e.celtEnergyMask)
	return out
}

func (e *Encoder) syncCELTEnergyMask() {
	if e.celtEncoder == nil {
		return
	}
	if len(e.celtEnergyMask) == 0 {
		e.celtEncoder.SetEnergyMask(nil)
		return
	}
	n := min(len(e.celtEnergyMask), celt.MaxBands*2)
	e.celtEncoder.SetEnergyMask(e.celtEnergyMask[:n])
}
