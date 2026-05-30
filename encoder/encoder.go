// Package encoder implements the unified Opus encoder per RFC 6716.
// It orchestrates SILK and CELT sub-encoders for hybrid mode encoding,
// which combines SILK (0-8kHz) with CELT (8-20kHz) for super-wideband
// and fullband speech encoding.
//
// Reference: RFC 6716 Section 3.2
package encoder

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// Mode represents the encoding mode.
type Mode int

const (
	// ModeAuto automatically selects the best mode based on content and bandwidth.
	ModeAuto Mode = iota
	// ModeSILK uses SILK-only encoding (configs 0-11).
	ModeSILK
	// ModeHybrid uses combined SILK+CELT encoding (configs 12-15).
	ModeHybrid
	// ModeCELT uses CELT-only encoding (configs 16-31).
	ModeCELT
)

// Errors for the encoder.
var (
	// ErrInvalidSampleRate indicates a sample rate outside the Opus API set.
	ErrInvalidSampleRate = errors.New("encoder: invalid sample rate (must be 8000, 12000, 16000, 24000, or 48000)")

	// ErrInvalidChannels indicates an invalid channel count.
	ErrInvalidChannels = errors.New("encoder: invalid channels (must be 1 or 2)")

	// ErrInvalidFrameSize indicates an invalid frame size.
	ErrInvalidFrameSize = errors.New("encoder: invalid frame size")

	// ErrInvalidHybridFrameSize indicates a frame size invalid for hybrid mode.
	ErrInvalidHybridFrameSize = errors.New("encoder: hybrid mode only supports 10ms (480) or 20ms (960) frames")

	// ErrEncodingFailed indicates a general encoding failure.
	ErrEncodingFailed = errors.New("encoder: encoding failed")

	// ErrInvalidDREDDuration indicates DRED duration is outside libopus bounds.
	ErrInvalidDREDDuration = errors.New("encoder: invalid DRED duration")

	// ErrInvalidFECConfig indicates an invalid in-band FEC configuration.
	ErrInvalidFECConfig = errors.New("encoder: invalid in-band FEC config")

	// ErrInvalidVoiceRatio indicates a voice ratio outside libopus bounds.
	ErrInvalidVoiceRatio = errors.New("encoder: invalid voice ratio")
)

const (
	defaultScratchPacketBytes   = maxSilkPacketBytes
	extensionScratchPacketBytes = 3826
)

const (
	InBandFECDisabled  = 0
	InBandFECEnabled   = 1
	InBandFECMusicSafe = 2
)

// Encoder is the unified Opus encoder that orchestrates SILK and CELT sub-encoders.
type Encoder struct {
	// Sub-encoders (created lazily)
	silkEncoder     *silk.Encoder
	silkSideEncoder *silk.Encoder // For stereo side channel in hybrid mode
	celtEncoder     *celt.Encoder

	// Configuration
	mode              Mode
	bandwidth         types.Bandwidth
	sampleRate        int32
	channels          int32
	frameSize         int32 // In samples at 48kHz
	lowDelay          bool
	voipApp           bool
	restrictedSilkApp bool

	// Bitrate controls
	bitrateMode   BitrateMode
	useVBR        bool
	vbrConstraint bool
	bitrate       int32 // Target bits per second
	// celtCVBRBoundScale scales CELT constrained-VBR burst bound.
	// 1.0 matches libopus single-stream behavior.
	celtCVBRBoundScale opusVal16

	// FEC controls
	fecEnabled                  bool
	packetLoss                  int32 // Expected packet loss percentage (0-100)
	lastVADActivityQ8           int32
	lastVADInputTiltQ15         int32
	lastVADInputQualityBandsQ15 [4]int32
	lastVADActive               bool
	lastVADValid                bool
	lastOpusVADActive           bool
	lastOpusVADValid            bool
	lastOpusVADProb             float32
	silkVAD                     *VADState
	silkVADMidFeedback          *VADState
	silkVADSide                 *VADState
	fec                         *fecState

	// DTX (Discontinuous Transmission) controls
	dtxEnabled bool
	dtx        *dtxState
	rng        uint32 // RNG for comfort noise
	finalRange uint32
	// hybridFinalRange stores the libopus final range for the last hybrid frame,
	// including any CELT transition redundancy range.
	hybridFinalRange uint32

	// Complexity control (0-10, higher = better quality but slower)
	complexity int32

	// Signal type hint for mode selection
	signalType types.Signal

	// Maximum bandwidth limit (actual bandwidth is clamped to this)
	maxBandwidth types.Bandwidth

	// Force channels (-1=auto, 1=mono, 2=stereo)
	forceChannels int32

	// LFE mode flag.
	// When true, force CELT-only narrowband behavior for this stream.
	lfe bool

	// LSB depth of input signal (8-24 bits, affects DTX sensitivity)
	lsbDepth int32

	// Prediction disabled (reduces inter-frame dependency for error resilience)
	predictionDisabled bool

	// Phase inversion disabled (for stereo decorrelation)
	phaseInversionDisabled bool

	// celtSurroundTrim carries multistream surround-trim bias into CELT alloc-trim.
	celtSurroundTrim opusVal32

	// celtEnergyMask carries per-band surround masking into CELT dynalloc control.
	celtEnergyMask []float32

	encoderQEXTFields
	encoderFixedCELTFields

	// dnnBlob retains a validated USE_WEIGHTS_FILE blob for future optional
	// extension paths (DRED/OSCE). Keeping it here mirrors libopus ctl lifetime.
	dnnBlob *dnnblob.Blob
	encoderDREDFields

	// DC rejection / variable-cutoff HP filter state.
	hpMem [4]float32
	// variableHPSmth2Q15 is the Opus-level smoothed HP cutoff (log2 domain, Q15)
	// driving hp_cutoff() for VoIP input (src/opus_encoder.c). -1 means
	// "not yet initialized" so it is seeded on first use.
	variableHPSmth2Q15    int32
	variableHPSmth2Inited bool

	// Hybrid mode state for improved SILK/CELT coordination
	hybridState *HybridState

	// Audio scene analyzer (The "Brain")
	analyzer *TonalityAnalysisState
	// Last frame analysis info from RunAnalysis(), used by mode heuristics.
	lastAnalysisInfo    AnalysisInfo
	lastAnalysisValid   bool
	lastAnalysisFresh   bool
	analysisReadPosBak  int32
	analysisSubframeBak int32
	analysisReadBakSet  bool
	celtForceIntra      bool
	prevMode            Mode
	prevPacketMode      Mode
	prevAutoMode        Mode
	inputBuffer         []opusRes
	delayBuffer         []opusRes

	// Auto-mode state (matching libopus OpusEncoder fields)
	voiceRatio        int32           // Persistent voice ratio (-1 = unset, 0-100)
	detectedBandwidth types.Bandwidth // Analysis-detected bandwidth (0 = undetected)
	streamChannels    int32           // Actual encoding channels (1 or 2)
	prevChannels      int32           // Previous frame's streamChannels
	autoBandwidth     types.Bandwidth // Last auto-selected bandwidth (for hysteresis)
	first             bool            // First frame flag
	lbrrCoded         bool            // Previous frame FEC coding decision
	userBandwidth     types.Bandwidth // User-set bandwidth value
	userBandwidthSet  bool            // Whether userBandwidth is explicitly set
	widthMem          StereoWidthMem  // Stateful stereo width computation memory
	toMono            int32           // Stereo->mono transition countdown (0=inactive)
	fecConfig         int32           // FEC config: 0=disabled, 1=enabled, 2=music-safe

	// SILK downsampling
	silkResampler       *silk.DownsamplingResampler
	silkResamplerRight  *silk.DownsamplingResampler
	silkResamplerRate   int32
	silkResampled       []float32
	silkResampledR      []float32
	silkResampledBuffer []float32
	silkMonoInputHist   [2]float32
	scratchSilkAligned  []float32

	// Scratch buffers for zero-allocation encoding
	scratchDCPCM      []opusRes // DC rejected PCM buffer
	scratchInputPCM   []opusRes // Public PCM rounded into the libopus opus_res domain
	scratchPCM32      []float32 // Reusable float32 analysis/SILK scratch
	scratchLeft       []float32 // Left channel deinterleave buffer
	scratchRight      []float32 // Right channel deinterleave buffer
	scratchMono       []float32 // Mono mix buffer (VAD)
	scratchVADFlags   [silk.MaxFramesPerPacket]bool
	scratchVADStates  [silk.MaxFramesPerPacket]silk.VADFrameState
	scratchPacket     []byte    // Output packet buffer
	scratchDelayedPCM []opusRes // Delay-compensated CELT input
	scratchDelayState []opusRes // Packet-local delay history for transition-prefill replay
	// Snapshot of libopus delay-history CELT transition prefill window (Fs/400).
	scratchTransitionPrefill []opusRes
	scratchSilkPrefill       []opusRes
	scratchCELTPrefill       []opusRes // CELT transition prefill source (Fs/400 * channels)
	hasCELTPrefill           bool
	scratchQuantPCM          []opusRes // LSB-depth quantized input
	floatInputFrame          []float32 // Current public float32 frame view, if available
	floatInputExact          bool      // True when pcm originated from float32 samples
}

// NewEncoder creates a new unified Opus encoder.
func NewEncoder(sampleRate, channels int) *Encoder {
	validRates := map[int]bool{8000: true, 12000: true, 16000: true, 24000: true, 48000: true}
	if !validRates[sampleRate] {
		sampleRate = 48000
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	maxSamples := 5760 * channels

	return &Encoder{
		mode:                   ModeAuto,
		bandwidth:              types.BandwidthFullband,
		sampleRate:             int32(sampleRate),
		channels:               int32(channels),
		frameSize:              960,
		lowDelay:               false,
		bitrateMode:            ModeCVBR,
		useVBR:                 true,
		vbrConstraint:          true,
		celtCVBRBoundScale:     1.0,
		bitrate:                64000,
		fecEnabled:             false,
		packetLoss:             0,
		fec:                    newFECState(),
		dtxEnabled:             false,
		dtx:                    newDTXState(),
		rng:                    22222,
		complexity:             9,
		signalType:             types.SignalAuto,
		maxBandwidth:           types.BandwidthFullband,
		forceChannels:          -1,
		lsbDepth:               24,
		predictionDisabled:     false,
		phaseInversionDisabled: false,
		analyzer:               NewTonalityAnalysisState(sampleRate),
		scratchPCM32:           make([]float32, maxSamples),
		scratchLeft:            make([]float32, maxSamples),
		scratchRight:           make([]float32, maxSamples),
		scratchMono:            make([]float32, maxSamples),
		scratchPacket:          make([]byte, defaultScratchPacketBytes),
		prevMode:               ModeAuto,
		prevPacketMode:         ModeAuto,
		prevAutoMode:           ModeAuto,
		voiceRatio:             -1,
		streamChannels:         int32(channels),
		prevChannels:           int32(channels),
		autoBandwidth:          types.BandwidthFullband,
		first:                  true,
	}
}

// SetMode sets the encoding mode.
func (e *Encoder) SetMode(mode Mode) {
	e.mode = mode
}

// Mode returns the current encoding mode.
func (e *Encoder) Mode() Mode {
	return e.mode
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
	e.bandwidth = bandwidth
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

// SetFrameSize sets the frame size in samples at 48kHz.
func (e *Encoder) SetFrameSize(frameSize int) {
	e.frameSize = int32(frameSize)
}

// FrameSize returns the current frame size in samples at 48kHz.
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

// Reset clears the encoder state for a new stream.
func (e *Encoder) Reset() {
	if len(e.delayBuffer) > 0 {
		clear(e.delayBuffer)
	}
	if len(e.inputBuffer) > 0 {
		e.inputBuffer = e.inputBuffer[:0]
	}
	if e.silkEncoder != nil {
		e.silkEncoder.Reset()
		e.silkEncoder.SetReducedDependency(e.predictionDisabled)
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.Reset()
		e.silkSideEncoder.SetReducedDependency(e.predictionDisabled)
	}
	if e.celtEncoder != nil {
		e.celtEncoder.Reset()
		e.celtEncoder.SetPrediction(e.celtPredictionMode())
		e.syncQEXTToCELT()
	}
	e.resetFixedCELT()
	if len(e.celtEnergyMask) > 0 {
		clear(e.celtEnergyMask)
		e.celtEnergyMask = e.celtEnergyMask[:0]
	}
	e.silkMonoInputHist = [2]float32{}
	e.resetFECState()
	if e.dtx != nil {
		e.dtx.reset()
	}
	e.finalRange = 0
	if e.analyzer != nil {
		e.analyzer.Reset()
	}
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	e.analysisReadBakSet = false
	e.prevMode = ModeAuto
	e.prevPacketMode = ModeAuto
	e.prevAutoMode = ModeAuto
	e.detectedBandwidth = 0
	e.streamChannels = int32(e.channels)
	e.prevChannels = int32(e.channels)
	e.autoBandwidth = types.BandwidthFullband
	e.first = true
	e.lbrrCoded = false
	e.widthMem = StereoWidthMem{}
	e.toMono = 0
	if extsupport.DREDRuntime {
		e.resetDREDControls()
	}
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
	if e.silkEncoder != nil {
		e.silkEncoder.SetComplexity(complexity)
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.SetComplexity(complexity)
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

func (e *Encoder) currentFinalRange(mode Mode) uint32 {
	switch mode {
	case ModeSILK:
		if e.silkEncoder != nil {
			return e.silkEncoder.FinalRange()
		}
	case ModeHybrid, ModeCELT:
		if mode == ModeHybrid {
			return e.hybridFinalRange
		}
		if r, ok := e.fixedCELTFinalRange(); ok {
			return r
		}
		if e.celtEncoder != nil {
			return e.celtEncoder.FinalRange()
		}
	default:
		if e.celtEncoder != nil {
			return e.celtEncoder.FinalRange()
		}
		if e.silkEncoder != nil {
			return e.silkEncoder.FinalRange()
		}
	}
	return 0
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

// SetCELTCVBRBoundScale scales CELT constrained-VBR burst bound.
// Valid range is [0, 1], where 1 keeps libopus single-stream behavior.
func (e *Encoder) SetCELTCVBRBoundScale(scale float32) {
	if scale < 0 {
		scale = 0
	} else if scale > 1 {
		scale = 1
	}
	e.celtCVBRBoundScale = scale
	if e.celtEncoder != nil {
		e.celtEncoder.SetConstrainedVBRBoundScale(scale)
	}
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

func bitrateToBits(bitrate int, frameSize int) int {
	return (bitrate * frameSize) / 48000
}

// silkInputBitrate mirrors the Opus bits_target reservation before SILK allocation.
// Opus reserves 8 bits for TOC/signaling before deriving the SILK bitrate.
func (e *Encoder) silkInputBitrate(frameSize int) int {
	if e.bitrate <= 0 || frameSize <= 0 {
		return 0
	}
	overheadBps := (8 * 48000) / frameSize
	rate := int(e.bitrate) - overheadBps
	if rate < 0 {
		return 0
	}
	return rate
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
	if actualMode == ModeSILK || actualMode == ModeHybrid {
		if complexity < 2 {
			equiv = (equiv * 4) / 5
		}
		if loss > 0 {
			equiv -= (equiv * loss) / (6*loss + 10)
		}
	} else if actualMode == ModeCELT {
		if complexity < 5 {
			equiv = (equiv * 9) / 10
		}
	} else {
		// Mode not known yet: libopus applies half the SILK packet-loss penalty.
		if loss > 0 {
			equiv -= (equiv * loss) / (12*loss + 20)
		}
	}
	return equiv
}

// Encode encodes a frame of libopus float-build PCM audio to an Opus packet.
func (e *Encoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
	return e.EncodeWithAnalysis(pcm, frameSize, pcm)
}

// EncodeFloat32 encodes a frame of libopus float PCM audio to an Opus packet.
func (e *Encoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	return e.Encode(pcm, frameSize)
}

// EncodeFloat32WithAnalysisMaxBytes is the float32 PCM entrypoint matching
// libopus opus_encode_float().
func (e *Encoder) EncodeFloat32WithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	return e.EncodeWithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxDataBytes)
}

// EncodeWithAnalysis encodes the selected frame while allowing analysis to see
// a larger caller frame, matching libopus expert-frame-duration handling.
func (e *Encoder) EncodeWithAnalysis(pcm []float32, frameSize int, analysisPCM []float32) ([]byte, error) {
	return e.EncodeWithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxSilkPacketBytes)
}

// EncodeWithAnalysisMaxBytes encodes with a caller output budget. maxDataBytes
// mirrors libopus max_data_bytes after packet-size-cap clamping.
func (e *Encoder) EncodeWithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	if len(pcm) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if analysisPCM == nil {
		analysisPCM = pcm
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%channels != 0 {
		return nil, ErrInvalidFrameSize
	}
	inputPCM := e.ensureInputPCM(expectedLen)
	copy(inputPCM, pcm[:expectedLen])
	e.SetFloatInputFrame(pcm)
	defer e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}

func (e *Encoder) encodeOpusResWithAnalysisMaxBytes(inputPCM []opusRes, frameSize int, maxDataBytes int, refreshAnalysis func()) ([]byte, error) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	expectedLen := frameSize * channels
	if len(inputPCM) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if maxDataBytes <= 0 {
		return nil, ErrEncodingFailed
	}
	packetCapBytes := maxSilkPacketBytes * 6
	if maxDataBytes > packetCapBytes {
		maxDataBytes = packetCapBytes
	}
	userBitrate := e.bitrate
	resolvedBitrate := e.resolvedBitrateForFrame(frameSize, maxDataBytes)
	if int32(resolvedBitrate) != userBitrate {
		e.bitrate = int32(resolvedBitrate)
		defer func() {
			e.bitrate = userBitrate
		}()
	}
	isSilence := isDigitalSilenceRes(inputPCM, e.lsbDepth)
	e.hasCELTPrefill = false
	defer func() {
		e.analysisReadBakSet = false
		e.celtForceIntra = false
	}()
	// Run Opus analysis on the original input frame (before top-level dc_reject
	// and LSB quantization) to match libopus run_analysis ordering.
	if refreshAnalysis != nil {
		refreshAnalysis()
	}
	lookaheadSamples := 0
	vadPCM := inputPCM
	// Update the SILK variable-HP-cutoff smoother before the Opus-level HP
	// filter reads it. libopus calls silk_HP_variable_cutoff at the start of
	// each SILK packet (enc_API.c), using prevLag/prevSignalType from the
	// previous packet's final frame; the Opus-level hp_cutoff() (which runs
	// before silk_Encode) then reads the just-updated value. Calling it here —
	// before preprocessInputHP and before this packet's SILK encode mutates
	// prevLag — reproduces that ordering exactly.
	if e.voipApp && e.silkEncoder != nil && e.mode != ModeCELT {
		e.silkEncoder.UpdateVariableHPCutoff()
	}
	pcmRes := e.quantizeInputToLSBDepth(inputPCM)
	pcmRes = e.preprocessInputHP(pcmRes, frameSize)
	frameEnd := frameSize * channels
	samplesNeeded := frameEnd + lookaheadSamples
	directFrameInput := lookaheadSamples == 0 && len(e.inputBuffer) == 0
	var framePCM []opusRes
	var lookaheadSlice []opusRes
	if directFrameInput {
		framePCM = pcmRes[:frameEnd]
		lookaheadSlice = pcmRes[frameEnd:frameEnd]
	} else {
		e.inputBuffer = append(e.inputBuffer, pcmRes...)
		if len(e.inputBuffer) < samplesNeeded {
			return nil, nil
		}
		framePCM = e.inputBuffer[:frameEnd]
		lookaheadSlice = e.inputBuffer[frameEnd:samplesNeeded]
	}

	var requestedMode Mode
	if e.mode == ModeAuto {
		// Full libopus auto-mode decision chain: voice_ratio, stereo_width,
		// stream_channels, mode threshold interpolation, auto-bandwidth,
		// bandwidth clamping, decide_fec, mode fixup.
		requestedMode = e.autoModeAndBandwidthDecision(framePCM, frameSize, maxDataBytes, isSilence)
	} else {
		signalHint := e.signalType
		if signalHint == types.SignalAuto {
			signalHint = e.autoSignalFromPCM(framePCM, frameSize)
		}
		e.updateStreamChannelsForFrame(frameSize)
		requestedMode = e.selectMode(frameSize, signalHint)
		if e.lfe {
			requestedMode = ModeCELT
		}
		// Run decide_fec for non-auto modes too. In libopus, decide_fec()
		// runs unconditionally at line 1675 (not just in auto mode).
		// This controls whether LBRR is actually coded based on bitrate,
		// bandwidth, packet loss, and hysteresis.
		frameRate := sampleRate / frameSize
		if frameRate <= 0 {
			frameRate = 50
		}
		useVBR := e.bitrateMode != ModeCBR
		equivRate := e.computeEquivRate(e.bitrate, int32(channels), int32(frameRate),
			useVBR, requestedMode, e.complexity, e.packetLoss)
		e.bandwidth = e.autoClampBandwidth(e.bandwidth, requestedMode, equivRate, e.maxRateForFrame(frameSize, maxDataBytes))
		bw := e.bandwidth
		e.lbrrCoded = decideFEC(e.fecEnabled, e.packetLoss, e.lbrrCoded,
			requestedMode, &bw, equivRate)
		e.bandwidth = bw
		if requestedMode == ModeSILK && e.bandwidth > types.BandwidthWideband {
			e.bandwidth = types.BandwidthWideband
		} else {
			requestedMode = autoModeFixup(requestedMode, e.bandwidth)
		}
	}
	actualMode, prevModeNext := e.applyCELTTransitionDelay(frameSize, requestedMode)
	transitionToCELT := requestedMode == ModeCELT && actualMode != ModeCELT

	dredExtraDelay := 0
	if !e.lowDelay {
		dredExtraDelay = sampleRate / 250
	}
	dredInSubframes := (actualMode == ModeCELT && frameSize > 960 && frameSize%960 == 0) ||
		(actualMode == ModeHybrid && frameSize > 960 && frameSize%960 == 0) ||
		(actualMode == ModeSILK && frameSize > 2880)
	if e.dredEncodingActive() && !dredInSubframes {
		e.processDREDLatentsForPacket(framePCM, frameSize, dredExtraDelay, actualMode)
	} else if !e.dredEncodingActive() {
		e.clearInactiveDREDHistory()
	}

	// DTX activity detection matches libopus opus_encoder.c:1246+1911-1930:
	// is_digital_silence() and compute_frame_energy() both run on the original
	// unfiltered PCM (before hp_cutoff/dc_reject). Pass inputPCM so that
	// literal-zero silence is detected correctly even when the dc-reject filter
	// state carries residual energy from preceding speech frames.
	suppressFrame, _ := e.shouldUseDTXRes(inputPCM)
	if suppressFrame {
		if !directFrameInput {
			remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
			e.inputBuffer = e.inputBuffer[:remaining]
		}
		if isConcreteMode(actualMode) {
			e.prevPacketMode = actualMode
		}
		if isConcreteMode(prevModeNext) {
			e.prevMode = prevModeNext
			if e.mode == ModeAuto {
				e.prevAutoMode = prevModeNext
			}
		}
		e.first = false
		e.prevChannels = e.streamChannels
		e.finalRange = 0
		// Match libopus: return a 1-byte TOC-only packet for DTX frames.
		// The decoder triggers its own CNG when it sees a TOC with no frame data.
		// Returning nil here would cause WebRTC to see missing packets and apply
		// degrading PLC instead of smooth comfort noise.
		return e.buildDTXPacketForMode(frameSize, actualMode)
	}

	if (actualMode == ModeSILK && frameSize <= 2880) || (actualMode == ModeHybrid && frameSize <= 960) {
		// Match libopus application semantics:
		// explicit ModeSILK mirrors restricted-silk, where Opus-level activity
		// stays VAD_NO_DECISION except true digital-silence input, which libopus
		// still forwards as VAD_NO_ACTIVITY before SILK VAD runs.
		if actualMode == ModeSILK && e.mode == ModeSILK {
			e.updateRestrictedSilkOpusVADRes(vadPCM, frameSize)
		} else {
			// Audio/VoIP SILK and short hybrid lanes still use Opus-level activity.
			e.updateOpusVADRes(vadPCM, frameSize)
		}
	}

	encodingBitrate := e.bitrate
	dredBitrate := 0
	var dredPlan dredEmissionPlan
	dredPlanOK := false
	if e.dredEncodingActive() {
		if plan, ok := e.computeDREDEmissionPlan(frameSize); ok {
			dredPlan = plan
			dredPlanOK = true
			dredBitrate = int(dredPlan.bitrate)
			// Reserve DRED bytes from the primary encoder's bitrate budget.
			// libopus opus_encoder.c (line 1338) reduces st->bitrate_bps by
			// dred_bitrate_bps before passing it to all three primary modes
			// (SILK/Hybrid/CELT). The reduced bitrate then flows into each
			// mode's compute_vbr step, shrinking the primary-frame target.
			encodingBitrate -= int32(dredPlan.bitrate)
			if encodingBitrate < 1 {
				encodingBitrate = 1
			}
		}
	}

	var frameData []byte
	var packet []byte
	var err error
	switch actualMode {
	case ModeSILK:
		e.maybePrefillSILKOnModeTransition(actualMode)
		if frameSize > 2880 {
			packet, err = e.encodeSILKMultiFramePacket(framePCM, vadPCM, frameSize, int(e.bitrate), int(encodingBitrate), dredBitrate, dredExtraDelay)
		} else {
			originalBitrate := e.bitrate
			if encodingBitrate != originalBitrate {
				e.bitrate = encodingBitrate
			}
			dredNoDecision := e.dredEncodingActive() && !e.lastOpusVADValid
			frameData, err = e.encodeSILKFrameWithDRED(framePCM, lookaheadSlice, frameSize, int(originalBitrate), dredBitrate)
			if encodingBitrate != originalBitrate {
				e.bitrate = originalBitrate
			}
			if err == nil {
				// Match libopus opus_encoder.c SILK-only behavior:
				// strip trailing zero bytes after range coder finalization.
				frameData = trimSilkTrailingZeros(frameData)
				if dredNoDecision {
					silkSignalType, _ := e.silkEncoder.LastEncodedSignalInfo()
					e.backfillDREDActivityForFrame(frameSize, silkSignalType != 0)
				}
			}
		}
		e.updateDelayBuffer(framePCM, frameSize)
	case ModeHybrid:
		if frameSize > 960 {
			delayState := e.ensureDelayState(len(e.delayBuffer))
			copy(delayState, e.delayBuffer)
			celtPCM := e.applyDelayCompensation(framePCM, frameSize)
			packet, err = e.encodeHybridMultiFramePacket(framePCM, celtPCM, vadPCM, lookaheadSlice, delayState, frameSize, transitionToCELT, int(e.bitrate), int(encodingBitrate), dredBitrate, dredExtraDelay)
		} else {
			e.maybePrefillSILKOnModeTransition(actualMode)
			celtPCM := e.applyDelayCompensation(framePCM, frameSize)
			originalBitrate := e.bitrate
			maxPacketBytes := 0
			if encodingBitrate != originalBitrate {
				if dredPlanOK && e.bitrateMode != ModeCBR {
					maxPacketBytes = e.hybridDREDPrimaryBudget(int(originalBitrate), frameSize, dredPlan)
				}
				e.bitrate = encodingBitrate
			}
			dredNoDecision := e.dredEncodingActive() && !e.lastOpusVADValid
			frameData, err = e.encodeHybridFrameWithMaxPacketAndTransition(framePCM, celtPCM, lookaheadSlice, frameSize, maxPacketBytes, maxDataBytes, dredBitrate, false, true, transitionToCELT, false)
			if encodingBitrate != originalBitrate {
				e.bitrate = originalBitrate
			}
			if err == nil && dredNoDecision {
				silkSignalType, _ := e.silkEncoder.LastEncodedSignalInfo()
				e.backfillDREDActivityForFrame(frameSize, silkSignalType != 0)
			}
		}
	case ModeCELT:
		celtPCM := e.prepareCELTPCM(framePCM, frameSize)
		e.maybePrefillCELTOnModeTransition(actualMode, celtPCM, frameSize)
		if frameSize > 960 {
			// Long CELT packets are encoded as multi-frame packets.
			packet, err = e.encodeCELTMultiFramePacket(framePCM, vadPCM, celtPCM, frameSize, int(e.bitrate), int(encodingBitrate), dredBitrate, dredExtraDelay)
		} else {
			originalBitrate := e.bitrate
			if encodingBitrate != originalBitrate {
				e.bitrate = encodingBitrate
			}
			// libopus opus_encode_frame_native() bounds the CELT range coder by
			// nb_compr_bytes = max_data_bytes-1 (opus_encoder.c line 2392;
			// redundancy_bytes==0 for CELT-only). Thread the caller budget as the
			// payload cap so per-stream curr_max ceilings (multistream LFE/last
			// stream) are honored. Standalone callers pass a large budget, leaving
			// behavior unchanged.
			celtMaxPayload := 0
			if maxDataBytes > 1 {
				celtMaxPayload = maxDataBytes - 1
			}
			frameData, err = e.encodeCELTFrameWithBitrateAndMaxPayload(celtPCM, frameSize, int(e.bitrate), celtMaxPayload)
			if encodingBitrate != originalBitrate {
				e.bitrate = originalBitrate
			}
		}
	default:
		return nil, ErrEncodingFailed
	}
	if err != nil {
		return nil, err
	}
	if !directFrameInput {
		remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
		e.inputBuffer = e.inputBuffer[:remaining]
	}
	qextPayload := []byte(nil)
	if extsupport.QEXT && actualMode == ModeCELT && e.celtEncoder != nil {
		qextPayload = e.lastQEXTPayload()
	}
	var qextExtensionBuf [1]packetExtension
	qextExtensions := []packetExtension(nil)
	if len(qextPayload) > 0 {
		qextExtensionBuf[0] = packetExtension{ID: qextExtensionID, Data: qextPayload}
		qextExtensions = qextExtensionBuf[:]
	}
	dredPacketBuilt := false
	if packet == nil {
		stereo := e.packetStereoForMode(actualMode)
		packetBW := e.effectiveBandwidth()
		if actualMode == ModeSILK && packetBW > types.BandwidthWideband {
			packetBW = types.BandwidthWideband
		}
		if e.dredEncodingActive() {
			if dredPacket, ok, dredErr := e.maybeBuildSingleFrameDREDPacket(frameData, actualMode, packetBW, frameSize, stereo, qextExtensions); dredErr != nil {
				return nil, dredErr
			} else if ok {
				packet = dredPacket
				dredPacketBuilt = true
			}
		}
		var (
			packetLen int
			pktErr    error
		)
		if packet == nil && len(qextPayload) > 0 {
			packetLen, pktErr = buildPacketWithSingleExtensionInto(
				e.scratchPacket,
				frameData,
				modeToTypes(actualMode),
				packetBW,
				frameSize,
				stereo,
				qextExtensionID,
				qextPayload,
				0,
				false,
			)
		} else if packet == nil {
			targetSize := targetBytesForBitrate(int(e.bitrate), frameSize)
			if e.bitrateMode == ModeCBR && targetSize >= 2+len(frameData) {
				if targetSize == 2+len(frameData) {
					config := configFromParams(modeToTypes(actualMode), packetBW, frameSize)
					if config < 0 || len(e.scratchPacket) < targetSize {
						pktErr = ErrInvalidConfig
					} else {
						e.scratchPacket[0] = generateTOC(uint8(config), stereo, 3)
						e.scratchPacket[1] = 0x01
						copy(e.scratchPacket[2:], frameData)
						packetLen = targetSize
					}
				} else {
					packetLen, pktErr = buildPacketWithExtensionsInto(
						e.scratchPacket,
						frameData,
						modeToTypes(actualMode),
						packetBW,
						frameSize,
						stereo,
						nil,
						targetSize,
						true,
					)
				}
			} else {
				packetLen, pktErr = BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), packetBW, frameSize, stereo)
			}
		}
		if packet == nil && pktErr != nil {
			return nil, pktErr
		}
		if packet == nil {
			packet = e.scratchPacket[:packetLen]
		}
	}
	if isConcreteMode(actualMode) {
		e.prevPacketMode = actualMode
	}
	if isConcreteMode(prevModeNext) {
		e.prevMode = prevModeNext
		if e.mode == ModeAuto {
			e.prevAutoMode = prevModeNext
		}
	}
	switch e.bitrateMode {
	case ModeCBR:
		if dredPacketBuilt {
			break
		}
		targetSize := targetBytesForBitrate(int(e.bitrate), frameSize)
		if len(qextPayload) > 0 && len(packet) < targetSize {
			stereo := e.packetStereoForMode(actualMode)
			packetBW := e.effectiveBandwidth()
			if actualMode == ModeSILK && packetBW > types.BandwidthWideband {
				packetBW = types.BandwidthWideband
			}
			packetLen, pktErr := buildPacketWithSingleExtensionInto(
				e.scratchPacket,
				frameData,
				modeToTypes(actualMode),
				packetBW,
				frameSize,
				stereo,
				qextExtensionID,
				qextPayload,
				targetSize,
				true,
			)
			if pktErr == nil {
				packet = e.scratchPacket[:packetLen]
			}
		} else {
			packet = padToSizeInto(e.scratchPacket, packet, targetSize)
		}
	case ModeCVBR:
		if !dredPacketBuilt && len(qextPayload) == 0 {
			packet = constrainSize(packet, targetBytesForBitrate(int(e.bitrate), frameSize), CVBRTolerance)
		}
	}
	e.prevChannels = e.streamChannels
	e.finalRange = e.currentFinalRange(actualMode)
	return packet, nil
}

// buildDTXPacket generates a 1-byte TOC-only Opus packet for DTX frames.
// This matches libopus opus_encoder.c behavior where DTX returns:
//
//	data[0] = gen_toc(mode, Fs/frame_size, bandwidth, channels);
//	return 1;
//
// The decoder's CNG (Comfort Noise Generation) activates when it receives
// a TOC-only packet, producing natural-sounding silence. This is preferred
// over returning nil/0 bytes, which WebRTC interprets as packet loss.
func (e *Encoder) buildDTXPacket(frameSize int) ([]byte, error) {
	actualMode := e.selectMode(frameSize, e.signalType)
	return e.buildDTXPacketForMode(frameSize, actualMode)
}

func (e *Encoder) buildDTXPacketForMode(frameSize int, actualMode Mode) ([]byte, error) {
	packetBW := e.effectiveBandwidth()
	if actualMode == ModeSILK && packetBW > types.BandwidthWideband {
		packetBW = types.BandwidthWideband
	}
	stereo := e.packetStereoForMode(actualMode)

	// Build TOC-only packet (no frame data) into scratch buffer.
	n, err := BuildPacketInto(e.scratchPacket, nil, modeToTypes(actualMode), packetBW, frameSize, stereo)
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:n], nil
}

// modeToTypes converts internal encoder Mode to types.Mode.
func modeToTypes(m Mode) types.Mode {
	switch m {
	case ModeSILK:
		return types.ModeSILK
	case ModeHybrid:
		return types.ModeHybrid
	case ModeCELT:
		return types.ModeCELT
	default:
		return types.ModeCELT
	}
}

func (e *Encoder) silkInternalChannels() int {
	if e.channels != 2 {
		return 1
	}
	streamChannels := e.streamChannels
	if streamChannels <= 0 {
		streamChannels = int32(e.channels)
	}
	if streamChannels <= 1 {
		return 1
	}
	return 2
}

func (e *Encoder) packetStereoForMode(mode Mode) bool {
	if e.channels != 2 {
		return false
	}
	switch mode {
	case ModeSILK:
		return e.silkInternalChannels() == 2
	case ModeHybrid, ModeCELT:
		return e.celtInternalChannelsForMode(mode) == 2
	}
	return true
}

func (e *Encoder) celtInternalChannelsForMode(mode Mode) int {
	if e.channels != 2 {
		return 1
	}
	streamChannels := e.streamChannels
	if streamChannels <= 0 {
		streamChannels = int32(e.channels)
	}
	if (mode == ModeCELT || mode == ModeHybrid) && streamChannels <= 1 {
		return 1
	}
	return 2
}

// preprocessInputHP applies the input high-pass stage that precedes SILK/CELT,
// matching src/opus_encoder.c: VoIP uses the adaptive hp_cutoff() biquad,
// every other application uses the fixed 3 Hz dc_reject(). The cutoff for
// hp_cutoff is driven by the SILK variable-HP-cutoff smoother.
func (e *Encoder) preprocessInputHP(in []opusRes, frameSize int) []opusRes {
	if !e.voipApp {
		return e.dcReject(in, frameSize)
	}
	return e.hpCutoff(in, frameSize)
}

// hpCutoff applies the adaptive second-order high-pass filter used for VoIP
// input, ported from hp_cutoff() + silk_biquad_res() (float path) in
// src/opus_encoder.c. The cutoff frequency adapts from the SILK encoder's
// variable_HP_smth1_Q15 estimate, smoothed at the Opus level into
// variable_HP_smth2_Q15.
func (e *Encoder) hpCutoff(in []opusRes, frameSize int) []opusRes {
	channels := int(e.channels)
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}

	// Determine hp_freq_smth1: in CELT-only mode libopus uses the min-cutoff
	// floor; otherwise it reads the SILK encoder's variable_HP_smth1_Q15.
	var hpFreqSmth1 int32
	if e.silkEncoder != nil && e.mode != ModeCELT {
		hpFreqSmth1 = e.silkEncoder.VariableHPSmth1Q15()
	} else {
		hpFreqSmth1 = silk.MinCutoffLogSmth2Q15()
	}

	if !e.variableHPSmth2Inited {
		e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15()
		e.variableHPSmth2Inited = true
	}
	e.variableHPSmth2Q15 = silk.SmoothVariableHPSmth2Q15(e.variableHPSmth2Q15, hpFreqSmth1)
	cutoffHz := silk.VariableHPCutoffHz(e.variableHPSmth2Q15)

	bQ28, aQ28 := silk.HPCutoffCoefsQ28(cutoffHz, int32(fs))
	var b [3]float32
	var a [2]float32
	const inv28 = float32(1.0) / float32(int32(1)<<28)
	b[0] = float32(bQ28[0]) * inv28
	b[1] = float32(bQ28[1]) * inv28
	b[2] = float32(bQ28[2]) * inv28
	a[0] = float32(aQ28[0]) * inv28
	a[1] = float32(aQ28[1]) * inv28
	const verySmall = float32(1e-30)

	src32 := e.floatInputFrame
	if e.LSBDepth() != 24 || !e.floatInputExact || len(src32) < n {
		src32 = nil
	}

	// silk_biquad_res, float path (Direct Form II Transposed), per channel.
	for c := 0; c < channels; c++ {
		s0 := e.hpMem[2*c]
		s1 := e.hpMem[2*c+1]
		for i := 0; i < frameSize; i++ {
			idx := i*channels + c
			var inval float32
			if src32 != nil {
				inval = src32[idx]
			} else {
				inval = float32(in[idx])
			}
			vout := s0 + b[0]*inval
			s0 = s1 - vout*a[0] + b[1]*inval
			s1 = -vout*a[1] + b[2]*inval + verySmall
			out[idx] = opusRes(vout)
		}
		e.hpMem[2*c] = s0
		e.hpMem[2*c+1] = s1
	}
	return out
}

// dcReject applies a DC rejection filter (1st-order high-pass filter at 3Hz).
func (e *Encoder) dcReject(in []opusRes, frameSize int) []opusRes {
	channels := int(e.channels)
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	coef := float32(6.3) * float32(3) / float32(fs)
	coef2 := float32(1.0) - coef
	const verySmall = float32(1e-30)
	src32 := e.floatInputFrame
	if e.LSBDepth() != 24 || !e.floatInputExact || len(src32) < n {
		src32 = nil
	}
	if channels == 2 {
		m0 := e.hpMem[0]
		m2 := e.hpMem[2]
		if src32 != nil {
			for i := 0; i < frameSize; i++ {
				x0 := src32[2*i]
				x1 := src32[2*i+1]
				out0 := x0 - m0
				out1 := x1 - m2
				m0 = coef*x0 + verySmall + coef2*m0
				m2 = coef*x1 + verySmall + coef2*m2
				out[2*i] = out0
				out[2*i+1] = out1
			}
		} else {
			for i := 0; i < frameSize; i++ {
				x0 := in[2*i]
				x1 := in[2*i+1]
				out0 := x0 - m0
				out1 := x1 - m2
				m0 = coef*x0 + verySmall + coef2*m0
				m2 = coef*x1 + verySmall + coef2*m2
				out[2*i] = out0
				out[2*i+1] = out1
			}
		}
		e.hpMem[0] = m0
		e.hpMem[2] = m2
	} else {
		m0 := e.hpMem[0]
		if src32 != nil {
			for i := 0; i < n; i++ {
				x := src32[i]
				y := x - m0
				m0 = coef*x + verySmall + coef2*m0
				out[i] = y
			}
		} else {
			for i := 0; i < n; i++ {
				x := in[i]
				y := x - m0
				m0 = coef*x + verySmall + coef2*m0
				out[i] = y
			}
		}
		e.hpMem[0] = m0
	}
	return out
}

func quantizeOpusResToLSBDepthInPlace(samples []opusRes, depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	scale := opusVal32(math.Ldexp(1.0, depth-1))
	invScale := opusVal32(1.0) / scale
	for i, v := range samples {
		x := opusVal32(v)
		q := floorOpusVal32(opusVal32(0.5) + x*scale)
		samples[i] = opusRes(q * invScale)
	}
}

func floorOpusVal32(x opusVal32) opusVal32 {
	absBits := math.Float32bits(float32(x)) & 0x7fffffff
	if absBits > 0x7f800000 || x > 9.22e18 || x < -9.22e18 {
		return x
	}
	i := int64(x)
	if opusVal32(i) > x {
		i--
	}
	return opusVal32(i)
}

func (e *Encoder) quantizeInputToLSBDepth(pcm []opusRes) []opusRes {
	if e.LSBDepth() == 24 {
		return pcm
	}
	out := e.ensureQuantPCM(len(pcm))
	copy(out, pcm)
	quantizeOpusResToLSBDepthInPlace(out, e.LSBDepth())
	return out
}

func (e *Encoder) ensureInputPCM(size int) []opusRes {
	if cap(e.scratchInputPCM) < size {
		e.scratchInputPCM = make([]opusRes, size)
	}
	return e.scratchInputPCM[:size]
}

func (e *Encoder) ensureQuantPCM(size int) []opusRes {
	if cap(e.scratchQuantPCM) < size {
		e.scratchQuantPCM = make([]opusRes, size)
	}
	return e.scratchQuantPCM[:size]
}

func (e *Encoder) ensureDCPCM(size int) []opusRes {
	if cap(e.scratchDCPCM) < size {
		e.scratchDCPCM = make([]opusRes, size)
	}
	return e.scratchDCPCM[:size]
}

func trimSilkTrailingZeros(frameData []byte) []byte {
	for len(frameData) > 2 && frameData[len(frameData)-1] == 0 {
		frameData = frameData[:len(frameData)-1]
	}
	return frameData
}

func (e *Encoder) refreshFrameAnalysisF32(pcm32 []float32, frameSize int) {
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	e.analysisReadBakSet = false
	if e.analyzer == nil || frameSize <= 0 || len(pcm32) == 0 {
		return
	}
	if !e.analysisEnabled() {
		if e.analyzer.Initialized {
			e.analyzer.Reset()
		}
		return
	}
	// Mirror libopus opus_encoder.c: back up analysis read cursor before
	// run_analysis() so long packets can consume per-subframe info later.
	e.analysisReadPosBak = e.analyzer.ReadPos
	e.analysisSubframeBak = e.analyzer.ReadSubframe
	e.analysisReadBakSet = true
	// Keep analysis on float-domain samples to match opus_encode_float / opus_demo -f32.
	info := e.analyzer.RunAnalysis(pcm32, frameSize, int(e.channels))
	if !info.Valid {
		return
	}
	e.lastAnalysisInfo = info
	e.lastAnalysisValid = true
	e.lastAnalysisFresh = true
}

func (e *Encoder) analysisEnabled() bool {
	return !e.restrictedSilkApp && e.complexity >= 7 && e.sampleRate >= 16000 && e.sampleRate <= 48000
}

// primeSubframeAnalysis advances tonality_get_info() for long packets and keeps
// a reusable per-subframe analysis snapshot for downstream VAD/CELT decisions.
func (e *Encoder) primeSubframeAnalysis(frameSize int) {
	if !e.analysisReadBakSet || e.analyzer == nil {
		return
	}
	info := e.analyzer.tonalityGetInfo(frameSize)
	if info.Valid {
		e.lastAnalysisInfo = info
		e.lastAnalysisValid = true
		e.lastAnalysisFresh = true
		return
	}
	// Keep the last valid snapshot to avoid forcing a fallback RunAnalysis()
	// mid-packet when tonality_get_info has insufficient lookahead.
	if e.lastAnalysisValid {
		e.lastAnalysisFresh = true
	}
}

func (e *Encoder) syncCELTAnalysisToCELT() {
	if e.celtEncoder == nil {
		return
	}
	if !e.lastAnalysisValid {
		e.celtEncoder.SetAnalysisInfoWithTonality(0, [19]uint8{}, 0, 0, 0, 0, false)
		return
	}
	e.celtEncoder.SetAnalysisInfoWithTonality(
		int(e.lastAnalysisInfo.BandwidthIndex),
		e.lastAnalysisInfo.LeakBoost,
		e.lastAnalysisInfo.Activity,
		e.lastAnalysisInfo.Tonality,
		e.lastAnalysisInfo.TonalitySlope,
		e.lastAnalysisInfo.MaxPitchRatio,
		true,
	)
}

func quantizeFloat32ToInt16LibopusInPlace(samples []float32) {
	const invScale = float32(1.0 / 32768.0)
	for i, v := range samples {
		samples[i] = float32(opusmath.Float32ToInt16(v)) * invScale
	}
}

func downmixStereoToSilkMonoLibopus(dst, interleaved []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := 0; i < samples; i++ {
		sum := float32ToInt16Libopus(interleaved[2*i] + interleaved[2*i+1])
		dst[i] = float32(silkRShiftRound1(sum)) * invScale
	}
}

func averageSilkResamplerOutputsLibopus(dst, right []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := 0; i < samples; i++ {
		leftQ0 := float32ToInt16Libopus(dst[i])
		rightQ0 := float32ToInt16Libopus(right[i])
		dst[i] = float32((leftQ0+rightQ0)>>1) * invScale
	}
}

func float32ToInt16Libopus(v float32) int32 {
	return int32(opusmath.Float32ToInt16(v))
}

func silkRShiftRound1(v int32) int32 {
	return (v >> 1) + (v & 1)
}

func (e *Encoder) ensureDelayedPCM(size int) []opusRes {
	if cap(e.scratchDelayedPCM) < size {
		e.scratchDelayedPCM = make([]opusRes, size)
	}
	return e.scratchDelayedPCM[:size]
}

func (e *Encoder) ensureDelayState(size int) []opusRes {
	if cap(e.scratchDelayState) < size {
		e.scratchDelayState = make([]opusRes, size)
	}
	return e.scratchDelayState[:size]
}

func (e *Encoder) ensureTransitionPrefill(size int) []opusRes {
	if cap(e.scratchTransitionPrefill) < size {
		e.scratchTransitionPrefill = make([]opusRes, size)
	}
	return e.scratchTransitionPrefill[:size]
}

func (e *Encoder) ensureSilkPrefill(size int) []opusRes {
	if cap(e.scratchSilkPrefill) < size {
		e.scratchSilkPrefill = make([]opusRes, size)
	}
	return e.scratchSilkPrefill[:size]
}

func (e *Encoder) ensureCELTPrefill(size int) []opusRes {
	if cap(e.scratchCELTPrefill) < size {
		e.scratchCELTPrefill = make([]opusRes, size)
	}
	return e.scratchCELTPrefill[:size]
}

// applyDelayCompensation prepends the Opus delay buffer (Fs/250) to the current frame
// and returns a frame-sized slice for CELT processing. The delay buffer is updated
// with the latest samples after constructing the output.
func (e *Encoder) applyDelayCompensation(pcm []opusRes, frameSize int) []opusRes {
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}
	frameSamples := frameSize * channels
	if len(pcm) < frameSamples {
		frameSamples = len(pcm)
	}
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm[:frameSamples])
		return out
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	if delaySamples <= 0 || frameSamples <= 0 {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm[:frameSamples])
		return out
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]opusRes, encoderBufferSamples)
	}

	tailStart := encoderBufferSamples - delaySamples

	// Preserve the libopus delay-history snapshot window used by CELT transition prefill:
	// delay_buffer[encoder_buffer-delay_comp-Fs/400 : +Fs/400].
	prefillFrameSize := sampleRate / 400
	prefillSamples := prefillFrameSize * channels
	prefillStart := encoderBufferSamples - delaySamples - prefillSamples
	if prefillSamples > 0 && prefillStart >= 0 && prefillStart+prefillSamples <= len(e.delayBuffer) {
		prefill := e.ensureTransitionPrefill(prefillSamples)
		copy(prefill, e.delayBuffer[prefillStart:prefillStart+prefillSamples])
	} else {
		e.scratchTransitionPrefill = e.scratchTransitionPrefill[:0]
	}

	out := e.ensureDelayedPCM(frameSize * channels)
	if frameSamples <= delaySamples {
		copy(out, e.delayBuffer[tailStart:tailStart+frameSamples])
		clear(out[frameSamples:])
	} else {
		copy(out, e.delayBuffer[tailStart:])
		copy(out[delaySamples:], pcm[:frameSamples-delaySamples])
		clear(out[frameSamples:])
	}

	e.updateDelayBufferInternal(pcm, frameSamples, encoderBufferSamples)
	return out
}

func (e *Encoder) maybePrefillCELTOnModeTransition(actualMode Mode, celtPCM []opusRes, frameSize int) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	e.celtForceIntra = false
	if actualMode == ModeSILK || e.lowDelay {
		return
	}
	prev := e.prevMode
	if !isConcreteMode(prev) || prev == actualMode {
		return
	}

	prefillFrameSize := sampleRate / 400
	if prefillFrameSize <= 0 || !ValidFrameSize(prefillFrameSize, ModeCELT) {
		return
	}
	prefillSamples := prefillFrameSize * channels
	if prefillSamples <= 0 || len(celtPCM) < prefillSamples {
		return
	}
	prefillInput := celtPCM[:prefillSamples]
	if len(e.scratchTransitionPrefill) == prefillSamples {
		prefillInput = e.scratchTransitionPrefill
	}
	if e.hasCELTPrefill && len(e.scratchCELTPrefill) >= prefillSamples {
		prefillInput = e.scratchCELTPrefill[:prefillSamples]
	} else if delayComp := sampleRate / 250; delayComp > 0 {
		// Match libopus tmp_prefill source as closely as possible with the
		// available delay-compensated CELT window.
		delayCompSamples := delayComp * channels
		if delayCompSamples > len(celtPCM) {
			delayCompSamples = len(celtPCM)
		}
		prefillStart := delayCompSamples - prefillSamples
		if prefillStart < 0 {
			prefillStart = 0
		}
		prefillEnd := prefillStart + prefillSamples
		if prefillEnd > len(celtPCM) {
			prefillEnd = len(celtPCM)
			prefillStart = prefillEnd - prefillSamples
			if prefillStart < 0 {
				prefillStart = 0
			}
		}
		if prefillEnd-prefillStart == prefillSamples {
			prefillInput = celtPCM[prefillStart:prefillEnd]
		}
	}
	e.hasCELTPrefill = false

	e.ensureCELTEncoder()
	e.celtEncoder.Reset()
	e.celtEncoder.SetHybrid(actualMode == ModeHybrid)
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(actualMode))
	e.celtEncoder.SetTopLevelDelayCompensatedInput(true)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	// libopus re-drives CELT from the Opus wrapper after reset, so the
	// transition prefill still sees the current top-level analysis snapshot.
	e.syncCELTAnalysisToCELT()
	// Match libopus mode-transition cadence: prefill uses normal prediction,
	// then the next real frame is forced intra.
	e.celtEncoder.SetPrediction(e.celtPredictionMode())
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))

	switch actualMode {
	case ModeHybrid:
		e.celtEncoder.SetBitrate(CELTMaxBitrate)
		if e.bitrateMode == ModeCBR {
			e.celtEncoder.SetVBR(false)
			e.celtEncoder.SetConstrainedVBR(false)
		} else {
			e.celtEncoder.SetVBR(true)
			e.celtEncoder.SetConstrainedVBR(false)
		}
	case ModeCELT:
		e.celtEncoder.SetBitrate(CELTMaxBitrate)
		switch e.bitrateMode {
		case ModeCBR:
			e.celtEncoder.SetVBR(false)
			e.celtEncoder.SetConstrainedVBR(false)
		case ModeCVBR:
			e.celtEncoder.SetVBR(true)
			e.celtEncoder.SetConstrainedVBR(true)
		default:
			e.celtEncoder.SetVBR(true)
			e.celtEncoder.SetConstrainedVBR(false)
		}
	}

	e.celtEncoder.SetMaxPayloadBytes(2)
	e.celtEncoder.EncodeFrame(prefillInput, prefillFrameSize)
	e.celtEncoder.SetMaxPayloadBytes(0)
	// Match libopus mode-switch behavior: the next real CELT frame is forced intra.
	e.celtForceIntra = true
}

func (e *Encoder) maybePrefillSILKOnModeTransition(actualMode Mode) {
	e.maybePrefillSILKOnModeTransitionWithOptions(actualMode, true, true)
}

func (e *Encoder) maybePrefillSILKOnModeTransitionWithOptions(actualMode Mode, preserveLP bool, captureCELTPrefill bool) {
	if !e.shouldPrefillSILKOnModeTransition(actualMode) {
		return
	}
	e.runPendingSilkTransitionPrefill(preserveLP, captureCELTPrefill)
}

func (e *Encoder) shouldPrefillSILKOnModeTransition(actualMode Mode) bool {
	if actualMode == ModeCELT || e.lowDelay {
		return false
	}
	prev := e.prevMode
	if !isConcreteMode(prev) || prev != ModeCELT {
		return false
	}
	if e.channels < 1 || e.sampleRate <= 0 {
		return false
	}
	return true
}

func (e *Encoder) runPendingSilkTransitionPrefill(preserveLP bool, captureCELTPrefill bool) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// libopus prefill uses 10 ms of delay-buffer history on CELT->SILK/HYBRID.
	prefillFrameSize := sampleRate / 100
	if prefillFrameSize <= 0 {
		return
	}
	prefillSamples := prefillFrameSize * channels
	if prefillSamples <= 0 {
		return
	}
	prefill := e.ensureSilkPrefill(prefillSamples)
	for i := range prefill {
		prefill[i] = 0
	}
	if len(e.delayBuffer) >= prefillSamples {
		copy(prefill, e.delayBuffer[:prefillSamples])
	} else if len(e.delayBuffer) > 0 {
		copy(prefill[prefillSamples-len(e.delayBuffer):], e.delayBuffer)
	}
	e.runSilkTransitionPrefill(prefill, preserveLP, captureCELTPrefill)
}

func (e *Encoder) runSilkTransitionPrefill(prefill []opusRes, preserveLP bool, captureCELTPrefill bool) {
	if len(prefill) == 0 || e.channels < 1 || e.sampleRate <= 0 {
		return
	}
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	prefillFrameSize := len(prefill) / channels
	if prefillFrameSize <= 0 || prefillFrameSize*channels != len(prefill) {
		return
	}

	e.applySilkTransitionPrefillRamp(prefill, prefillFrameSize)

	if captureCELTPrefill {
		// CELT mode-transition prefill consumes this exact history slice in libopus:
		// delay_buffer[encoder_buffer-delay_comp-Fs/400 : +Fs/400].
		prefillLen := sampleRate / 400
		delayComp := sampleRate / 250
		prefillOffset := prefillFrameSize - delayComp - prefillLen
		celtPrefillSamples := prefillLen * channels
		if prefillLen > 0 && prefillOffset >= 0 && celtPrefillSamples > 0 {
			start := prefillOffset * channels
			end := start + celtPrefillSamples
			if start >= 0 && end <= len(prefill) {
				out := e.ensureCELTPrefill(celtPrefillSamples)
				copy(out, prefill[start:end])
				e.hasCELTPrefill = true
			}
		}
	}

	e.ensureSILKEncoder()
	var savedMainLP silk.LPState
	if preserveLP {
		savedMainLP = e.silkEncoder.GetLPState()
	}
	e.silkEncoder.Reset()
	e.silkEncoder.ResetTransitionPrefillState()
	if preserveLP {
		// Match libopus prefillFlag==2 semantics: keep LP transition state while
		// resetting other SILK encoder state for CELT->SILK/Hybrid prefill.
		e.silkEncoder.SetLPState(savedMainLP)
	}
	e.silkEncoder.SetComplexity(int(e.complexity))
	e.silkEncoder.SetReducedDependency(e.predictionDisabled)
	if e.channels == 2 {
		e.ensureSILKSideEncoder()
		var savedSideLP silk.LPState
		if preserveLP {
			savedSideLP = e.silkSideEncoder.GetLPState()
		}
		e.silkSideEncoder.Reset()
		e.silkSideEncoder.ResetTransitionPrefillState()
		if preserveLP {
			e.silkSideEncoder.SetLPState(savedSideLP)
		}
		e.silkSideEncoder.SetComplexity(int(e.complexity))
		e.silkSideEncoder.SetReducedDependency(e.predictionDisabled)
	}
	if !preserveLP {
		e.silkMonoInputHist = [2]float32{}
	}

	targetRate := silk.GetBandwidthConfig(e.silkBandwidth()).SampleRate
	if targetRate <= 0 {
		targetRate = 16000
	}
	e.ensureSILKResampler(targetRate)
	if e.silkResampler != nil {
		e.silkResampler.SetState(silk.DownsamplingResamplerState{})
	}
	if e.silkResamplerRight != nil {
		e.silkResamplerRight.SetState(silk.DownsamplingResamplerState{})
	}
	e.ensureSilkVAD()
	e.silkVAD.Reset()
	if e.channels == 2 {
		e.ensureSilkVADMidFeedback()
		e.silkVADMidFeedback.Reset()
		e.ensureSilkVADSide()
		e.silkVADSide.Reset()
	}

	if e.channels != 1 {
		e.runSilkStereoTransitionPrefill(prefill, prefillFrameSize, targetRate)
		return
	}

	pcm32 := e.scratchPCM32[:prefillFrameSize]
	for i := 0; i < prefillFrameSize; i++ {
		pcm32[i] = float32(prefill[i])
	}
	quantizeFloat32ToInt16LibopusInPlace(pcm32)

	silkIn := pcm32
	if targetRate != 48000 {
		targetSamples := prefillFrameSize * targetRate / 48000
		if targetSamples <= 0 {
			return
		}
		out := e.ensureSilkResampled(targetSamples)
		n := e.silkResampler.ProcessInto(pcm32, out)
		if n <= 0 {
			return
		}
		silkIn = out[:n]
	}
	silkIn = e.alignSilkMonoInput(silkIn)
	fsKHz := targetRate / 1000
	if fsKHz <= 0 {
		fsKHz = 16
	}
	// libopus prefill runs silk_encode_do_VAD_Fxx and advances the VAD/noise
	// estimators before the first coded SILK/Hybrid frame after a CELT stretch.
	state, active := computeSilkVADFrameState(e.silkVAD, silkIn, len(silkIn), fsKHz)
	if state.Valid {
		state, active = e.applyOpusVADToSilkState(state, active)
		e.lastVADActivityQ8 = state.SpeechActivityQ8
		e.lastVADInputTiltQ15 = state.InputTiltQ15
		e.lastVADInputQualityBandsQ15 = state.InputQualityBandsQ15
		e.lastVADActive = active
		e.lastVADValid = true
	}
	e.silkEncoder.PrefillFrame(silkIn)
}

func (e *Encoder) runSilkStereoTransitionPrefill(prefill []opusRes, prefillFrameSize, targetRate int) {
	if e.silkEncoder == nil || e.silkSideEncoder == nil || prefillFrameSize <= 0 || targetRate <= 0 {
		return
	}
	if len(prefill) < prefillFrameSize*2 {
		return
	}

	left := e.scratchLeft[:prefillFrameSize]
	right := e.scratchRight[:prefillFrameSize]
	for i := 0; i < prefillFrameSize; i++ {
		base := i * 2
		left[i] = float32(prefill[base])
		right[i] = float32(prefill[base+1])
	}
	quantizeFloat32ToInt16LibopusInPlace(left)
	quantizeFloat32ToInt16LibopusInPlace(right)

	if targetRate != 48000 {
		targetSamples := prefillFrameSize * targetRate / 48000
		if targetSamples <= 0 {
			return
		}
		if e.silkResampler == nil || e.silkResamplerRight == nil {
			e.ensureSILKResampler(targetRate)
			if e.silkResampler == nil || e.silkResamplerRight == nil {
				return
			}
		}
		leftOut := e.ensureSilkResampled(targetSamples)
		rightOut := e.ensureSilkResampledR(targetSamples)
		nL := e.silkResampler.ProcessInto(left, leftOut)
		nR := e.silkResamplerRight.ProcessInto(right, rightOut)
		if nL <= 0 || nR <= 0 {
			return
		}
		if nL < nR {
			rightOut = rightOut[:nL]
			leftOut = leftOut[:nL]
		} else if nR < nL {
			leftOut = leftOut[:nR]
			rightOut = rightOut[:nR]
		} else {
			leftOut = leftOut[:nL]
			rightOut = rightOut[:nR]
		}
		left = leftOut
		right = rightOut
		quantizeFloat32ToInt16LibopusInPlace(left)
		quantizeFloat32ToInt16LibopusInPlace(right)
	}
	if len(left) == 0 || len(right) == 0 {
		return
	}

	totalRate := e.silkInputBitrate(prefillFrameSize)
	if totalRate <= 0 {
		totalRate = int(e.bitrate)
	}
	if totalRate <= 0 {
		totalRate = 20000
	}
	fsKHz := targetRate / 1000
	if fsKHz <= 0 {
		fsKHz = 16
	}
	mid, side, _, midOnly, midRate, sideRate, widthQ14 := e.silkEncoder.StereoLRToMSWithRates(
		left,
		right,
		len(left),
		fsKHz,
		totalRate,
		0,
		false,
	)
	if len(mid) == 0 {
		return
	}
	if e.hybridState != nil {
		e.hybridState.silkStereoWidthQ14 = widthQ14
	}
	if midRate > 0 {
		e.silkEncoder.SetBitrate(midRate)
	}
	if sideRate > 0 {
		e.silkSideEncoder.SetBitrate(sideRate)
	}

	e.ensureSilkVADMidFeedback()
	midState, midActive := computeSilkVADFrameState(e.silkVADMidFeedback, mid, len(mid), fsKHz)
	midState, midActive = e.applyOpusVADToSilkState(midState, midActive)
	if midState.Valid {
		e.lastVADActivityQ8 = midState.SpeechActivityQ8
		e.lastVADInputTiltQ15 = midState.InputTiltQ15
		e.lastVADInputQualityBandsQ15 = midState.InputQualityBandsQ15
		e.lastVADActive = midActive
		e.lastVADValid = true
		applySilkVADFrameState(e.silkEncoder, midState)
	}
	e.silkEncoder.PrefillFrame(mid)

	if midOnly || sideRate <= 0 || len(side) == 0 {
		return
	}
	e.ensureSilkVADSide()
	sideState, sideActive := computeSilkVADFrameState(e.silkVADSide, side, len(side), fsKHz)
	sideState, _ = e.applyOpusVADToSilkState(sideState, sideActive)
	if sideState.Valid {
		applySilkVADFrameState(e.silkSideEncoder, sideState)
	}
	e.silkSideEncoder.PrefillFrame(side)
	e.silkSideEncoder.SetBitsExceeded(e.silkEncoder.BitsExceeded())
}

func (e *Encoder) applySilkTransitionPrefillRamp(prefill []opusRes, prefillFrameSize int) {
	if len(prefill) == 0 || prefillFrameSize <= 0 {
		return
	}
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	prefillLen := sampleRate / 400
	start := prefillFrameSize - delayComp - prefillLen
	if start < 0 {
		start = 0
	}
	if start > prefillFrameSize {
		start = prefillFrameSize
	}

	prefix := start * channels
	if prefix > len(prefill) {
		prefix = len(prefill)
	}
	for i := 0; i < prefix; i++ {
		prefill[i] = 0
	}
	if prefillLen <= 0 {
		return
	}
	if start+prefillLen > prefillFrameSize {
		prefillLen = prefillFrameSize - start
	}
	if prefillLen <= 0 {
		return
	}

	inc := 48000 / sampleRate
	if inc < 1 {
		inc = 1
	}
	window := celt.GetWindowBufferF32(prefillLen * inc)
	maxByWindow := prefillLen
	if len(window) > 0 {
		maxByWindow = len(window) / inc
		if maxByWindow < prefillLen {
			prefillLen = maxByWindow
		}
	}
	if prefillLen <= 0 {
		return
	}

	if len(window) == 0 {
		den := opusVal16(prefillLen)
		if den < 1 {
			den = 1
		}
		for i := 0; i < prefillLen; i++ {
			g := opusVal16(i) / den
			base := (start + i) * channels
			for c := 0; c < channels && base+c < len(prefill); c++ {
				prefill[base+c] *= g
			}
		}
		return
	}

	for i := 0; i < prefillLen; i++ {
		w := window[i*inc]
		g := w * w
		base := (start + i) * channels
		for c := 0; c < channels && base+c < len(prefill); c++ {
			prefill[base+c] *= g
		}
	}
}

// updateDelayBuffer advances the delay buffer without generating a compensated frame.
// This keeps the delay history in sync during SILK-only frames.
func (e *Encoder) updateDelayBuffer(pcm []opusRes, frameSize int) {
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		return
	}
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	frameSamples := frameSize * channels
	if len(pcm) < frameSamples {
		frameSamples = len(pcm)
	}
	if delaySamples <= 0 || frameSamples <= 0 {
		return
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]opusRes, encoderBufferSamples)
	}
	e.updateDelayBufferInternal(pcm, frameSamples, encoderBufferSamples)
}

func (e *Encoder) updateDelayBufferInternal(pcm []opusRes, frameSamples, encoderBufferSamples int) {
	if frameSamples <= 0 || encoderBufferSamples <= 0 {
		return
	}
	if frameSamples >= encoderBufferSamples {
		copy(e.delayBuffer, pcm[frameSamples-encoderBufferSamples:frameSamples])
		return
	}

	keep := encoderBufferSamples - frameSamples
	copy(e.delayBuffer[:keep], e.delayBuffer[frameSamples:frameSamples+keep])
	copy(e.delayBuffer[keep:], pcm[:frameSamples])
}

// prepareCELTPCM applies CELT delay compensation unless low-delay mode is active.
func (e *Encoder) prepareCELTPCM(framePCM []opusRes, frameSize int) []opusRes {
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}
	frameSamples := frameSize * channels
	if len(framePCM) < frameSamples {
		frameSamples = len(framePCM)
	}
	if e.lowDelay {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, framePCM[:frameSamples])
		return out
	}
	return e.applyDelayCompensation(framePCM, frameSize)
}

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	if e.restrictedSilkApp {
		return ModeSILK
	}
	if e.lowDelay {
		return ModeCELT
	}
	if frameSize > 960 {
		if e.mode != ModeAuto {
			// Hybrid long packets are encoded as 20ms multi-frame packets.
			if e.mode == ModeHybrid {
				return ModeHybrid
			}
			// CELT 40/60ms is encoded as multi-frame (2/3 x 20ms) packets.
			return e.mode
		}
		bw := e.effectiveBandwidth()

		// Fullband long frames in auto mode follow CELT-only path in libopus audio app.
		if bw == types.BandwidthFullband {
			return ModeCELT
		}
		if bw == types.BandwidthSuperwideband {
			return e.selectLongSWBAutoMode(frameSize, signalHint)
		}
		// Respect explicit or analyzed signal hints.
		switch signalHint {
		case types.SignalVoice:
			// In SWB long-frame auto mode, libopus only uses Hybrid or CELT.
			// Avoid raw SILK packets in this lane.
			if bw == types.BandwidthSuperwideband {
				return ModeHybrid
			}
			return ModeSILK
		case types.SignalMusic:
			return ModeCELT
		}
		// In auto-signal mode for long frames, bias by bandwidth instead of the
		// per-frame classifier to avoid unstable SILK/CELT switching.
		if bw == types.BandwidthSuperwideband {
			return ModeCELT
		}
		return ModeSILK
	}
	if e.mode != ModeAuto {
		return e.mode
	}
	return e.selectShortAutoMode(frameSize, signalHint)
}

func isConcreteMode(mode Mode) bool {
	return mode == ModeSILK || mode == ModeHybrid || mode == ModeCELT
}

// applyCELTTransitionDelay mirrors libopus to_celt handling:
// when switching from SILK/Hybrid to CELT on >=10 ms frames, hold one frame in
// the previous non-CELT mode but advance prev-mode state to CELT for next frame.
func (e *Encoder) applyCELTTransitionDelay(frameSize int, requested Mode) (actual Mode, prevNext Mode) {
	actual = requested
	prevNext = requested

	prev := e.prevMode
	if !isConcreteMode(prev) || !isConcreteMode(requested) {
		return actual, prevNext
	}

	switchingAcrossCELT := (requested == ModeCELT && prev != ModeCELT) ||
		(requested != ModeCELT && prev == ModeCELT)
	if !switchingAcrossCELT {
		return actual, prevNext
	}

	// libopus delays SILK/Hybrid->CELT transition for 10ms+ frames.
	if requested == ModeCELT {
		minDelayFrame := int(e.sampleRate) / 100
		if minDelayFrame <= 0 {
			minDelayFrame = 480
		}
		if frameSize >= minDelayFrame {
			actual = prev
			prevNext = ModeCELT
		}
	}
	return actual, prevNext
}

// selectShortAutoMode ports libopus auto mode-threshold control for 10/20 ms
// frames (SILK/hybrid vs CELT), including previous-mode hysteresis.
func (e *Encoder) selectShortAutoMode(frameSize int, signalHint types.Signal) Mode {
	_ = signalHint
	bw := e.effectiveBandwidth()

	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, int32(frameRate), useVBR, ModeAuto, e.complexity, e.packetLoss)

	prev := e.prevAutoMode
	if prev != ModeSILK && prev != ModeHybrid && prev != ModeCELT {
		prev = ModeAuto
	}

	voiceEst := e.autoVoiceEstimate(prev)
	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384
	if e.voipApp {
		threshold += 8000
	}
	if prev == ModeCELT {
		threshold -= 4000
	} else if prev == ModeSILK || prev == ModeHybrid {
		threshold += 4000
	}

	mode := ModeSILK
	if equivRate >= int32(threshold) {
		mode = ModeCELT
	}
	// Match libopus behavior: with in-band FEC and sufficient expected loss,
	// force SILK unless music-safe FEC is confident the signal is music.
	if e.fecEnabled && e.packetLoss > int32((128-voiceEst)>>4) &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeSILK
	}
	// Match libopus behavior: when DTX is enabled for voiced content, favor SILK.
	if e.dtxEnabled && voiceEst > 100 {
		mode = ModeSILK
	}
	// For SWB/FB lanes, SILK-only maps to hybrid in libopus.
	if mode == ModeSILK && bw > types.BandwidthWideband {
		mode = ModeHybrid
	}
	if mode == ModeHybrid && bw <= types.BandwidthWideband {
		mode = ModeSILK
	}

	if !ValidFrameSize(frameSize, mode) {
		if ValidFrameSize(frameSize, ModeCELT) {
			return ModeCELT
		}
		if ValidFrameSize(frameSize, ModeSILK) {
			return ModeSILK
		}
		return ModeCELT
	}
	return mode
}

// selectLongSWBAutoMode mirrors libopus mode-threshold control for long-frame SWB
// auto mode (Celt-only vs Silk/Hybrid lane), using analysis-derived voice estimate
// and previous-mode hysteresis.
func (e *Encoder) selectLongSWBAutoMode(frameSize int, signalHint types.Signal) Mode {
	_ = signalHint
	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, int32(frameRate), useVBR, ModeAuto, e.complexity, e.packetLoss)

	prev := e.prevAutoMode
	if prev != ModeCELT && prev != ModeSILK && prev != ModeHybrid {
		prev = ModeAuto
	}
	voiceEst := e.autoVoiceEstimate(prev)

	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384

	// Match libopus auto-mode threshold bias for VoIP.
	if e.voipApp {
		threshold += 8000
	}

	// libopus hysteresis: bias against rapid CELT<->SILK/HYBRID switching.
	if prev == ModeCELT {
		threshold -= 4000
	} else if prev == ModeSILK || prev == ModeHybrid {
		threshold += 4000
	}

	mode := ModeHybrid
	if equivRate >= int32(threshold) {
		mode = ModeCELT
	}
	// Match libopus behavior: with in-band FEC and sufficient expected loss,
	// force SILK unless music-safe FEC is confident the signal is music.
	if e.fecEnabled && e.packetLoss > int32((128-voiceEst)>>4) &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeHybrid
	}
	// Match libopus behavior: when DTX is enabled for voiced content, favor SILK lane.
	if e.dtxEnabled && voiceEst > 100 {
		mode = ModeHybrid
	}
	return mode
}

// autoSignalFromPCM is kept for backward compatibility but RunAnalysis is preferred.
func (e *Encoder) autoSignalFromPCM(pcm []opusRes, frameSize int) types.Signal {
	if len(pcm) == 0 || frameSize <= 0 {
		return types.SignalAuto
	}
	if !e.analysisEnabled() {
		return types.SignalAuto
	}
	if !e.lastAnalysisFresh {
		pcm32 := []float32(pcm)
		runAnalyzer := frameSize > 960
		if !runAnalyzer && e.mode == ModeAuto && frameSize == 960 && e.effectiveBandwidth() == types.BandwidthSuperwideband {
			runAnalyzer = true
		}
		if runAnalyzer && e.analyzer != nil {
			info := e.analyzer.RunAnalysis(pcm32, frameSize, int(e.channels))
			if info.Valid {
				e.lastAnalysisInfo = info
				e.lastAnalysisValid = true
				e.lastAnalysisFresh = true
			}
		}
	}

	// Only trust clear decisions from analysis probabilities on long frames.
	if frameSize > 960 && e.lastAnalysisValid {
		if e.lastAnalysisInfo.MusicProb >= 0.65 {
			return types.SignalMusic
		}
		if e.lastAnalysisInfo.MusicProb <= 0.60 {
			return types.SignalVoice
		}
		return types.SignalAuto
	}
	// libopus mode-auto fallback when analysis is unavailable/invalid is to keep
	// OPUS_SIGNAL_AUTO and rely on threshold control with default voice estimate.
	return types.SignalAuto
}

func (e *Encoder) autoVoiceEstimate(prev Mode) int {
	voiceEst := 48 // OPUS_APPLICATION_AUDIO fallback.
	if e.voipApp {
		voiceEst = 115
	}
	if e.signalType == types.SignalVoice {
		return 127
	}
	if e.signalType == types.SignalMusic {
		return 0
	}
	if !e.lastAnalysisValid {
		return voiceEst
	}
	prob := e.lastAnalysisInfo.MusicProb
	if prev == ModeCELT {
		prob = e.lastAnalysisInfo.MusicProbMax
	} else if prev == ModeSILK || prev == ModeHybrid {
		prob = e.lastAnalysisInfo.MusicProbMin
	}
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}
	voiceRatio := int(opusmath.FloorHalfPlusF32ToInt32(float32(100) * (float32(1) - prob)))
	voiceEst = (voiceRatio * 327) >> 8
	// OPUS_APPLICATION_AUDIO clamp.
	if voiceEst > 115 {
		voiceEst = 115
	}
	return voiceEst
}

// effectiveBandwidth returns the resolved bandwidth for encoder submodules.
func (e *Encoder) effectiveBandwidth() types.Bandwidth {
	if e.lfe {
		return types.BandwidthNarrowband
	}
	return e.bandwidth
}

func (e *Encoder) celtPredictionMode() int {
	if e.predictionDisabled {
		return 0
	}
	return 2
}

func (e *Encoder) celtPredictionModeForFrame() int {
	if e.celtForceIntra {
		e.celtForceIntra = false
		return 0
	}
	return e.celtPredictionMode()
}

func (e *Encoder) encodeSILKFrameWithDRED(pcm []opusRes, lookahead []opusRes, frameSize, originalBitrate, dredBitrate int) ([]byte, error) {
	return e.encodeSILKFrameWithDREDAndMax(pcm, lookahead, frameSize, originalBitrate, dredBitrate, 0)
}

func (e *Encoder) encodeSILKFrameWithDREDAndMax(pcm []opusRes, lookahead []opusRes, frameSize, originalBitrate, dredBitrate, maxPacketBytes int) ([]byte, error) {
	e.ensureSILKEncoder()
	pcm32 := e.scratchPCM32[:len(pcm)]
	copy(pcm32, pcm)
	var lookahead32 []float32
	if len(lookahead) > 0 {
		start := len(pcm)
		if len(e.scratchPCM32) >= start+len(lookahead) {
			lookahead32 = e.scratchPCM32[start : start+len(lookahead)]
		} else {
			lookahead32 = make([]float32, len(lookahead))
		}
		copy(lookahead32, lookahead)
	}
	internalChannels := e.silkInternalChannels()
	if e.channels != 2 {
		// Match libopus enc_API.c float path: quantize to int16 precision
		// before SILK resampling/input buffering. Stereo uses its own
		// per-channel path below so it can match libopus predictor state.
		quantizeFloat32ToInt16LibopusInPlace(pcm32)
		quantizeFloat32ToInt16LibopusInPlace(lookahead32)
	}

	cfg := silk.GetBandwidthConfig(e.silkBandwidth())
	targetRate := cfg.SampleRate
	if targetRate != 48000 {
		e.ensureSILKResampler(targetRate)
	}
	targetSamples := frameSize * targetRate / 48000
	if targetSamples <= 0 {
		targetSamples = len(pcm32)
	}
	if e.channels == 2 && internalChannels == 2 {
		// Set bitrates: total rate on mid encoder (StereoLRToMSWithRates splits it),
		// per-channel rate on side encoder for its own SNR control.
		totalSilkRate := e.silkInputBitrate(frameSize)
		perChannelRate := totalSilkRate / int(e.channels)
		if totalSilkRate > 0 {
			e.silkEncoder.SetBitrate(totalSilkRate)
		}
		e.silkEncoder.SetFEC(e.lbrrCoded)
		e.silkEncoder.SetPacketLoss(int(e.packetLoss))
		e.ensureSILKSideEncoder()
		if totalSilkRate > 0 {
			e.silkSideEncoder.SetBitrate(totalSilkRate)
		} else if perChannelRate > 0 {
			e.silkSideEncoder.SetBitrate(perChannelRate)
		}
		e.silkSideEncoder.SetFEC(e.lbrrCoded)
		e.silkSideEncoder.SetPacketLoss(int(e.packetLoss))

		// Set VBR mode on both encoders (matching mono path).
		silkVBR := e.bitrateMode != ModeCBR || dredBitrate > 0
		e.silkEncoder.SetVBR(silkVBR)
		e.silkSideEncoder.SetVBR(silkVBR)

		// Set max bits for both encoders.
		if e.bitrate > 0 {
			maxBits := e.silkMaxBits(frameSize, totalSilkRate, originalBitrate, dredBitrate)
			if maxPacketBytes > 0 {
				maxBits = e.silkMaxBitsForPacketBytes(frameSize, totalSilkRate, maxPacketBytes, dredBitrate)
			}
			e.silkEncoder.SetMaxBits(maxBits)
			e.silkSideEncoder.SetMaxBits(maxBits)
		}

		left := e.scratchLeft[:frameSize]
		right := e.scratchRight[:frameSize]
		for i := 0; i < frameSize; i++ {
			left[i] = pcm32[i*2]
			right[i] = pcm32[i*2+1]
		}
		lookaheadSize := len(lookahead32) / 2
		leftLookahead := e.scratchLeft[frameSize : frameSize+lookaheadSize]
		rightLookahead := e.scratchRight[frameSize : frameSize+lookaheadSize]
		for i := 0; i < lookaheadSize; i++ {
			leftLookahead[i] = lookahead32[i*2]
			rightLookahead[i] = lookahead32[i*2+1]
		}
		// Match libopus FLOAT2INT16 quantization on the stereo feed before
		// SILK resampling; small tie-breaking differences here materially
		// change packet-0 stereo predictor/range state.
		quantizeFloat32ToInt16LibopusInPlace(left)
		quantizeFloat32ToInt16LibopusInPlace(right)
		quantizeFloat32ToInt16LibopusInPlace(leftLookahead)
		quantizeFloat32ToInt16LibopusInPlace(rightLookahead)
		if targetRate != 48000 {
			leftOut := e.ensureSilkResampled(targetSamples)
			rightOut := e.ensureSilkResampledR(targetSamples)
			nL := e.silkResampler.ProcessInto(left, leftOut)
			nR := e.silkResamplerRight.ProcessInto(right, rightOut)
			if nL < nR {
				rightOut = rightOut[:nL]
				leftOut = leftOut[:nL]
			} else if nR < nL {
				leftOut = leftOut[:nR]
				rightOut = rightOut[:nR]
			}
			left = leftOut
			right = rightOut
		}
		quantizeFloat32ToInt16LibopusInPlace(left)
		quantizeFloat32ToInt16LibopusInPlace(right)
		e.ensureSilkVADMidFeedback()
		midFeedbackAnalyzer := func(frame []float32, frameSamples, fsKHz int) (silk.VADFrameState, bool) {
			state, active := computeSilkVADFrameState(e.silkVADMidFeedback, frame, frameSamples, fsKHz)
			return e.applyOpusVADToSilkState(state, active)
		}
		e.ensureSilkVADSide()
		sideAnalyzer := func(frame []float32, frameSamples, fsKHz int) (silk.VADFrameState, bool) {
			state, active := computeSilkVADFrameState(e.silkVADSide, frame, frameSamples, fsKHz)
			return e.applyOpusVADToSilkState(state, active)
		}
		return silk.EncodeStereoWithEncoderVADAnalyzersWithSide(
			e.silkEncoder,
			e.silkSideEncoder,
			left,
			right,
			e.silkBandwidth(),
			nil,
			nil,
			midFeedbackAnalyzer,
			nil,
			nil,
			sideAnalyzer,
		)
	}
	if e.channels == 2 {
		mono := e.scratchMono[:frameSize]
		downmixStereoToSilkMonoLibopus(mono, pcm32, frameSize)
		pcm32 = mono
		if len(lookahead32) > 0 {
			lookaheadSize := len(lookahead32) / 2
			monoLookahead := e.scratchLeft[:lookaheadSize]
			downmixStereoToSilkMonoLibopus(monoLookahead, lookahead32, lookaheadSize)
			lookahead32 = monoLookahead
		} else {
			lookahead32 = nil
		}
	}
	var lookaheadOut []float32
	if targetRate != 48000 {
		out := e.ensureSilkResampled(targetSamples)
		n := e.silkResampler.ProcessInto(pcm32, out)
		if e.channels == 2 && internalChannels == 1 && e.prevChannels == 2 && e.silkResamplerRight != nil {
			rightOut := e.ensureSilkResampledR(targetSamples)
			nR := e.silkResamplerRight.ProcessInto(pcm32, rightOut)
			if nR < n {
				n = nR
			}
			averageSilkResamplerOutputsLibopus(out, rightOut, n)
		}
		if n < len(out) {
			out = out[:n]
		}
		pcm32 = out
		if len(lookahead32) > 0 {
			targetLaSamples := len(lookahead32) * targetRate / 48000
			if len(e.silkResampledBuffer) < targetLaSamples {
				e.silkResampledBuffer = make([]float32, targetLaSamples)
			}
			lookaheadOut = e.silkResampledBuffer[:targetLaSamples]
			state := e.silkResampler.State()
			e.silkResampler.ProcessInto(lookahead32, lookaheadOut)
			e.silkResampler.SetState(state)
		}
	} else {
		lookaheadOut = lookahead32
	}
	// Match libopus mono SILK buffering path (enc_API.c):
	// mono internal channels use sStereo.sMid history across frames.
	// This applies to all SILK internal rates (8/12/16 kHz), not only WB.
	if internalChannels == 1 {
		pcm32 = e.alignSilkMonoInput(pcm32)
	}
	quantizeFloat32ToInt16LibopusInPlace(pcm32)
	perChannelRate := 0
	if e.bitrate > 0 {
		perChannelRate = e.silkInputBitrate(frameSize) / internalChannels
		if perChannelRate > 0 {
			e.silkEncoder.SetBitrate(perChannelRate)
		}
	}
	e.silkEncoder.SetVBR(e.bitrateMode != ModeCBR || dredBitrate > 0)
	// Set SILK max bits based on bitrate mode (matches opus_encoder.c behavior).
	if e.bitrate > 0 {
		maxBits := e.silkMaxBits(frameSize, perChannelRate, originalBitrate, dredBitrate)
		if maxPacketBytes > 0 {
			maxBits = e.silkMaxBitsForPacketBytes(frameSize, perChannelRate, maxPacketBytes, dredBitrate)
		}
		e.silkEncoder.SetMaxBits(maxBits)
	}
	e.silkEncoder.SetFEC(e.lbrrCoded)
	e.silkEncoder.SetPacketLoss(int(e.packetLoss))
	fsKHz := targetRate / 1000
	vadFlags, vadStates, nFrames := e.computeSilkVADFlagsAndStates(pcm32, fsKHz)
	if e.lbrrCoded || nFrames > 1 {
		return e.silkEncoder.EncodePacketWithFECWithVADStates(pcm32, lookaheadOut, vadFlags, vadStates), nil
	}
	vadFlag := false
	if len(vadFlags) > 0 {
		vadFlag = vadFlags[0]
	}
	if len(vadStates) > 0 {
		applySilkVADFrameState(e.silkEncoder, vadStates[0])
	} else if e.lastVADValid {
		e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityBandsQ15)
	}
	res := e.silkEncoder.EncodeFrame(pcm32, lookaheadOut, vadFlag)
	return res, nil
}

func (e *Encoder) silkMaxBits(frameSize, silkBitrate, originalBitrate, dredBitrate int) int {
	maxBitrate := int(e.bitrate)
	if e.bitrateMode == ModeCBR && dredBitrate > 0 && originalBitrate > 0 {
		maxBitrate = originalBitrate
	}
	targetBytes := targetBytesForBitrate(maxBitrate, frameSize)
	maxBytes := targetBytes
	switch e.bitrateMode {
	case ModeVBR:
		// libopus opus_encoder.c line 2155: silk_mode.maxBits = (max_data_bytes-1)*8
		// with max_data_bytes = IMIN(orig_max_data_bytes, 1276). The SILK VBR
		// rate-control loop's bits_margin/exit conditions key off this budget.
		maxBytes = libopusMaxDataBytesCap
	case ModeCVBR:
		maxBytes = libopusMaxDataBytesCap
	}
	maxBits := silkPayloadMaxBits(maxBytes)
	if e.bitrateMode == ModeCBR && dredBitrate > 0 {
		if e.sampleRate <= 0 {
			return maxBits
		}
		if silkBitrate <= 0 {
			silkBitrate = int(e.bitrate)
		}
		otherBits := maxBits - silkBitrate*frameSize/int(e.sampleRate)
		if otherBits > 0 {
			maxBits -= otherBits * 3 / 4
		}
		if maxBits < 0 {
			maxBits = 0
		}
	}
	return maxBits
}

func (e *Encoder) silkMaxBitsForPacketBytes(frameSize, silkBitrate, maxPacketBytes, dredBitrate int) int {
	maxBits := silkPayloadMaxBits(maxPacketBytes)
	if e.bitrateMode == ModeCBR && dredBitrate > 0 {
		if e.sampleRate <= 0 {
			return maxBits
		}
		if silkBitrate <= 0 {
			silkBitrate = int(e.bitrate)
		}
		otherBits := maxBits - silkBitrate*frameSize/int(e.sampleRate)
		if otherBits > 0 {
			maxBits -= otherBits * 3 / 4
		}
		if maxBits < 0 {
			maxBits = 0
		}
	}
	return maxBits
}

// encodeCELTFrame encodes a frame using CELT-only mode.
func (e *Encoder) encodeCELTFrame(pcm []opusRes, frameSize int) ([]byte, error) {
	return e.encodeCELTFrameWithBitrateAndMaxPayload(pcm, frameSize, int(e.bitrate), 0)
}

func (e *Encoder) encodeCELTFrameWithBitrateAndMaxPayload(pcm []opusRes, frameSize int, bitrate int, maxPayloadBytes int) ([]byte, error) {
	return e.encodeCELTFrameWithBitrateMaxPayloadAndDRED(pcm, frameSize, bitrate, maxPayloadBytes, 0)
}

func celtDREDPayloadCap(maxPayloadBytes, dredBitrate, frameSize int) int {
	if maxPayloadBytes <= 0 || dredBitrate <= 0 || frameSize <= 0 {
		return maxPayloadBytes
	}
	dredBytes := bitrateToBits(dredBitrate, frameSize) / 8
	maxCELTBytes := maxPayloadBytes - dredBytes*3/4
	if maxCELTBytes < 5 {
		maxCELTBytes = 5
	}
	if maxCELTBytes < maxPayloadBytes {
		return maxCELTBytes
	}
	return maxPayloadBytes
}

func (e *Encoder) encodeCELTFrameWithBitrateMaxPayloadAndDRED(pcm []opusRes, frameSize int, bitrate int, maxPayloadBytes int, dredBitrate int) ([]byte, error) {
	e.ensureCELTEncoder()
	e.syncQEXTToCELT()
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(ModeCELT))
	e.celtEncoder.SetBitrate(bitrate)
	maxPayloadBytes = celtDREDPayloadCap(maxPayloadBytes, dredBitrate, frameSize)
	e.celtEncoder.SetMaxPayloadBytes(maxPayloadBytes)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetTopLevelDelayCompensatedInput(true)
	e.celtEncoder.SetPrediction(e.celtPredictionModeForFrame())
	e.celtEncoder.SetDCRejectEnabled(false)
	e.celtEncoder.SetPacketLoss(int(e.packetLoss))
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))
	switch e.bitrateMode {
	case ModeCBR:
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(false)
	case ModeCVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(true)
	case ModeVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false)
	}
	defer e.celtEncoder.SetMaxPayloadBytes(0)
	if out, ok, err := e.encodeCELTFrameFixed(pcm, frameSize, bitrate, maxPayloadBytes); ok || err != nil {
		return out, err
	}
	return e.celtEncoder.EncodeFrame(pcm, frameSize)
}

// encodeCELTMultiFramePacket encodes long CELT packets by splitting into
// 20ms CELT frames and packing them with Opus multi-frame framing.
func (e *Encoder) encodeCELTMultiFramePacket(framePCM []opusRes, vadPCM []opusRes, celtPCM []opusRes, frameSize, originalBitrate, encodingBitrate, dredBitrate, dredExtraDelay int) ([]byte, error) {
	if frameSize <= 960 || frameSize%960 != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / 960
	if frameCount < 2 || frameCount > 6 {
		return nil, ErrInvalidFrameSize
	}
	channels := int(e.channels)
	if len(framePCM) != frameSize*channels || len(vadPCM) != frameSize*channels || len(celtPCM) != frameSize*channels {
		return nil, ErrInvalidFrameSize
	}
	if e.analysisReadBakSet && e.analyzer != nil {
		e.analyzer.ReadPos = e.analysisReadPosBak
		e.analyzer.ReadSubframe = e.analysisSubframeBak
	}

	frameStride := 960 * channels
	frames := make([][]byte, frameCount)
	sameSize := true
	prevSize := -1
	packetTargetBytes := targetBytesForBitrate(originalBitrate, frameSize)
	if packetTargetBytes < 1 {
		packetTargetBytes = 1
	}
	if packetTargetBytes > maxSilkPacketBytes {
		packetTargetBytes = maxSilkPacketBytes
	}
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	if extsupport.QEXT && e.qextActive() {
		maxHeaderBytes += frameCount
	}
	maxLenSum := frameCount + packetTargetBytes - maxHeaderBytes
	if maxLenSum < frameCount {
		maxLenSum = frameCount
	}
	subframeBitrate := int(e.bitrate)
	if encodingBitrate > 0 {
		subframeBitrate = encodingBitrate
	}
	currMaxByRate := subframeBitrate * 960 / 48000 / 8
	if currMaxByRate < 2 {
		currMaxByRate = 2
	}
	dredBytes := 0
	if dredBitrate > 0 {
		dredBytes = bitrateToBits(dredBitrate, frameSize) / 8
	}
	dredActive := e.dredEncodingActive()
	if dredActive {
		e.clearDREDPacketSnapshot()
	}
	totSize := 0
	firstFrameMaxBytes := 0
	var qextExtensions [6]packetExtension
	qextExtensionCount := 0
	savedBitrate := e.bitrate
	e.bitrate = int32(subframeBitrate)
	for i := 0; i < frameCount; i++ {
		e.primeSubframeAnalysis(960)
		start := i * frameStride
		end := start + frameStride
		subFramePCM := framePCM[start:end]
		subVADPCM := vadPCM[start:end]
		if dredActive {
			e.updateOpusVADRes(subVADPCM, 960)
			e.processDREDLatentsWithActivity(subFramePCM, dredExtraDelay, e.lastOpusVADActive)
			if i == 0 {
				e.snapshotDREDPacketState()
			}
		}
		currMax := currMaxByRate
		capPerFrame := maxLenSum / frameCount
		if currMax > capPerFrame {
			currMax = capPerFrame
		}
		if dredBytes > 0 {
			dredCap := (maxLenSum - dredBytes) / frameCount
			if dredCap < 2 {
				dredCap = 2
			}
			if currMax > dredCap {
				currMax = dredCap
			}
			if i == 0 {
				currMax += dredBytes
			}
		}
		remainingCap := maxLenSum - totSize
		if currMax > remainingCap {
			currMax = remainingCap
		}
		if currMax < 2 {
			currMax = 2
		}
		if i == 0 {
			firstFrameMaxBytes = currMax
		}
		maxPayload := currMax - 1
		frameData, err := e.encodeCELTFrameWithBitrateMaxPayloadAndDRED(celtPCM[start:end], 960, int(e.bitrate), maxPayload, dredBitrate)
		if err != nil {
			e.bitrate = savedBitrate
			return nil, err
		}
		totSize += len(frameData) + 1
		// Keep a stable copy because the range coder output buffer is reused.
		frameCopy := append([]byte(nil), frameData...)
		frames[i] = frameCopy
		if extsupport.QEXT && e.celtEncoder != nil {
			qextPayload := e.lastQEXTPayload()
			if len(qextPayload) > 0 {
				qextExtensions[qextExtensionCount] = packetExtension{
					ID:    qextExtensionID,
					Data:  append([]byte(nil), qextPayload...),
					Frame: i,
				}
				qextExtensionCount++
			}
		}
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}
	e.bitrate = savedBitrate
	e.analysisReadBakSet = false

	if e.dredEncodingActive() {
		if dredPacket, ok, err := e.maybeBuildMultiFrameDREDPacket(frames, ModeCELT, e.effectiveBandwidth(), frameSize, 960, firstFrameMaxBytes, e.packetStereoForMode(ModeCELT), !sameSize, qextExtensions[:qextExtensionCount]); err != nil {
			return nil, err
		} else if ok {
			return dredPacket, nil
		}
	}
	if qextExtensionCount > 0 {
		packetLen, err := buildMultiFramePacketWithExtensionsInto(
			e.scratchPacket,
			frames,
			types.ModeCELT,
			e.effectiveBandwidth(),
			960,
			e.packetStereoForMode(ModeCELT),
			!sameSize,
			qextExtensions[:qextExtensionCount],
			0,
			false,
		)
		if err != nil {
			return nil, err
		}
		return e.scratchPacket[:packetLen], nil
	}
	return BuildMultiFramePacket(
		frames,
		types.ModeCELT,
		e.effectiveBandwidth(),
		960,
		e.packetStereoForMode(ModeCELT),
		!sameSize,
	)
}

// encodeHybridMultiFramePacket encodes long hybrid packets by splitting into
// 20ms hybrid frames and packing them with Opus multi-frame framing.
func (e *Encoder) encodeHybridMultiFramePacket(pcm []opusRes, celtPCM []opusRes, vadPCM []opusRes, lookahead []opusRes, delayState []opusRes, frameSize int, transitionToCELT bool, originalBitrate, encodingBitrate, dredBitrate, dredExtraDelay int) ([]byte, error) {
	if frameSize <= 960 || frameSize%960 != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / 960
	if frameCount < 2 || frameCount > 6 {
		return nil, ErrInvalidFrameSize
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels || len(celtPCM) != frameSize*channels || len(vadPCM) != frameSize*channels {
		return nil, ErrInvalidFrameSize
	}
	if e.analysisReadBakSet && e.analyzer != nil {
		e.analyzer.ReadPos = e.analysisReadPosBak
		e.analyzer.ReadSubframe = e.analysisSubframeBak
	}

	savedDelayBuffer := e.delayBuffer
	if len(delayState) == len(savedDelayBuffer) {
		e.delayBuffer = delayState
		defer func() {
			e.delayBuffer = savedDelayBuffer
		}()
	}

	frameStride := 960 * channels
	frames := make([][]byte, frameCount)
	sameSize := true
	prevSize := -1
	packetTargetBytes := targetBytesForBitrate(originalBitrate, frameSize)
	if packetTargetBytes < 1 {
		packetTargetBytes = 1
	}
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	maxLenSum := frameCount + packetTargetBytes - maxHeaderBytes
	if maxLenSum < frameCount {
		maxLenSum = frameCount
	}
	subframeBitrate := int(e.bitrate)
	if encodingBitrate > 0 {
		subframeBitrate = encodingBitrate
	}
	currMaxByRate := subframeBitrate * 960 / 48000 / 8
	if currMaxByRate < 2 {
		currMaxByRate = 2
	}
	dredBytes := 0
	if dredBitrate > 0 {
		dredBytes = bitrateToBits(dredBitrate, frameSize) / 8
	}
	totSize := 0
	firstFrameMaxBytes := 0
	packetPrefillFromCELT := e.shouldPrefillSILKOnModeTransition(ModeHybrid)
	dredActive := e.dredEncodingActive()
	if dredActive {
		e.clearDREDPacketSnapshot()
	}
	savedBitrate := e.bitrate
	e.bitrate = int32(subframeBitrate)
	for i := 0; i < frameCount; i++ {
		e.primeSubframeAnalysis(960)
		start := i * frameStride
		end := start + frameStride
		subPCM := pcm[start:end]
		subCELTPCM := celtPCM[start:end]
		subVADPCM := vadPCM[start:end]

		// Match libopus long-packet cadence: compute DRED activity from the
		// same per-subframe analysis snapshot used by the primary frame.
		e.updateOpusVADRes(subVADPCM, 960)
		dredNoDecision := !e.lastOpusVADValid
		if dredActive {
			e.processDREDLatentsWithActivity(subPCM, dredExtraDelay, e.lastOpusVADActive)
		}

		if packetPrefillFromCELT {
			// libopus keeps the packet-level CELT->SILK/HYBRID prefill active for
			// each 20 ms internal frame of long packets. The first subframe also
			// snapshots CELT's transition-prefill window; later ones only re-prime
			// the SILK state from the rolling delay history.
			e.maybePrefillSILKOnModeTransitionWithOptions(ModeHybrid, i > 0, i == 0)
		}

		// Hybrid subframes in multi-frame packets should be encoded exactly like
		// independent 20ms frames. Do not leak future subframe samples as lookahead.
		subLookahead := lookahead

		currMax := currMaxByRate
		capPerFrame := maxLenSum / frameCount
		if currMax > capPerFrame {
			currMax = capPerFrame
		}
		if dredBytes > 0 {
			dredCap := (maxLenSum - dredBytes) / frameCount
			if dredCap < 2 {
				dredCap = 2
			}
			if currMax > dredCap {
				currMax = dredCap
			}
			if i == 0 {
				currMax += dredBytes
			}
		}
		remainingCap := maxLenSum - totSize
		if currMax > remainingCap {
			currMax = remainingCap
		}
		if currMax < 2 {
			currMax = 2
		}
		if i == 0 {
			firstFrameMaxBytes = currMax
		}
		allowTransitionRedundancy := (!transitionToCELT && i == 0) || (transitionToCELT && i == frameCount-1)
		prevPacketMode := e.prevPacketMode
		runCELTTransitionPrefill := i == 0 && !e.lowDelay && isConcreteMode(prevPacketMode) && prevPacketMode != ModeHybrid
		subframeToCELT := transitionToCELT && i == frameCount-1
		frameData, err := e.encodeHybridFrameWithMaxPacketAndTransition(subPCM, subCELTPCM, subLookahead, 960, currMax, 0, dredBitrate, true, allowTransitionRedundancy, subframeToCELT, runCELTTransitionPrefill)
		if err != nil {
			e.bitrate = savedBitrate
			return nil, err
		}
		if dredActive && dredNoDecision {
			silkSignalType, _ := e.silkEncoder.LastEncodedSignalInfo()
			e.backfillDREDActivityForFrame(960, silkSignalType != 0)
		}
		if dredActive && i == 0 {
			e.snapshotDREDPacketState()
		}
		// Keep a stable copy because encoder scratch buffers are reused.
		frameCopy := append([]byte(nil), frameData...)
		frames[i] = frameCopy
		totSize += len(frameCopy) + 1
		if len(e.delayBuffer) > 0 {
			e.updateDelayBufferInternal(subPCM, len(subPCM), len(e.delayBuffer))
		}
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}
	e.bitrate = savedBitrate
	e.analysisReadBakSet = false

	packetBW := e.effectiveBandwidth()
	if e.dredEncodingActive() {
		stereo := e.packetStereoForMode(ModeHybrid)
		if dredPacket, ok, err := e.maybeBuildMultiFrameDREDPacket(frames, ModeHybrid, packetBW, frameSize, 960, firstFrameMaxBytes, stereo, !sameSize, nil); err != nil {
			return nil, err
		} else if ok {
			return dredPacket, nil
		}
	}
	return BuildMultiFramePacket(frames, types.ModeHybrid, packetBW, 960, e.packetStereoForMode(ModeHybrid), !sameSize)
}

// encodeSILKMultiFramePacket encodes 80/100/120ms SILK packets by splitting
// them into libopus-compatible 20/40/60ms SILK frames and repacketizing them.
func (e *Encoder) encodeSILKMultiFramePacket(pcm []opusRes, vadPCM []opusRes, frameSize int, originalBitrate, encodingBitrate, dredBitrate, dredExtraDelay int) ([]byte, error) {
	channels := int(e.channels)
	if len(pcm) != frameSize*channels || len(vadPCM) != frameSize*channels {
		return nil, ErrInvalidFrameSize
	}

	var encFrameSize int
	switch frameSize {
	case 3840:
		encFrameSize = 1920
	case 4800:
		encFrameSize = 960
	case 5760:
		encFrameSize = 2880
	default:
		return nil, ErrInvalidFrameSize
	}

	frameCount := frameSize / encFrameSize
	frames := make([][]byte, frameCount)
	sameSize := true
	prevSize := -1
	frameStride := encFrameSize * channels
	if e.analysisReadBakSet && e.analyzer != nil {
		e.analyzer.ReadPos = e.analysisReadPosBak
		e.analyzer.ReadSubframe = e.analysisSubframeBak
	}

	subframeBitrate := int(e.bitrate)
	if encodingBitrate > 0 {
		subframeBitrate = encodingBitrate
	}
	packetTargetBytes := targetBytesForBitrate(originalBitrate, frameSize)
	if packetTargetBytes < 1 {
		packetTargetBytes = 1
	}
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	maxLenSum := frameCount + packetTargetBytes - maxHeaderBytes
	if maxLenSum < frameCount {
		maxLenSum = frameCount
	}
	currMaxByRate := subframeBitrate * encFrameSize / 48000 / 8
	if currMaxByRate < 2 {
		currMaxByRate = 2
	}
	dredBytes := 0
	if dredBitrate > 0 {
		dredBytes = bitrateToBits(dredBitrate, frameSize) / 8
	}
	dredActive := e.dredEncodingActive()
	if dredActive {
		e.clearDREDPacketSnapshot()
	}
	totSize := 0
	firstFrameMaxBytes := 0
	savedBitrate := e.bitrate
	e.bitrate = int32(subframeBitrate)

	for i := 0; i < frameCount; i++ {
		e.primeSubframeAnalysis(encFrameSize)
		start := i * frameStride
		end := start + frameStride
		subPCM := pcm[start:end]
		subVADPCM := vadPCM[start:end]

		if e.mode == ModeSILK {
			e.updateRestrictedSilkOpusVADRes(subVADPCM, encFrameSize)
		} else {
			e.updateOpusVADRes(subVADPCM, encFrameSize)
		}
		dredNoDecision := !e.lastOpusVADValid
		if dredActive {
			e.processDREDLatentsWithActivity(subPCM, dredExtraDelay, e.lastOpusVADActive)
		}

		currMax := currMaxByRate
		capPerFrame := maxLenSum / frameCount
		if currMax > capPerFrame {
			currMax = capPerFrame
		}
		if dredBytes > 0 {
			dredCap := (maxLenSum - dredBytes) / frameCount
			if dredCap < 2 {
				dredCap = 2
			}
			if currMax > dredCap {
				currMax = dredCap
			}
			if i == 0 {
				currMax += dredBytes
			}
		}
		remainingCap := maxLenSum - totSize
		if currMax > remainingCap {
			currMax = remainingCap
		}
		if currMax < 2 {
			currMax = 2
		}
		if i == 0 {
			firstFrameMaxBytes = currMax
		}
		frameData, err := e.encodeSILKFrameWithDREDAndMax(subPCM, nil, encFrameSize, originalBitrate, dredBitrate, currMax)
		if err != nil {
			e.bitrate = savedBitrate
			return nil, err
		}
		if dredActive && dredNoDecision {
			silkSignalType, _ := e.silkEncoder.LastEncodedSignalInfo()
			e.backfillDREDActivityForFrame(encFrameSize, silkSignalType != 0)
		}
		if dredActive && i == 0 {
			e.snapshotDREDPacketState()
		}
		frameCopy := append([]byte(nil), trimSilkTrailingZeros(frameData)...)
		frames[i] = frameCopy
		totSize += len(frameCopy) + 1
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}
	e.bitrate = savedBitrate
	e.analysisReadBakSet = false

	packetBW := e.effectiveBandwidth()
	if packetBW > types.BandwidthWideband {
		packetBW = types.BandwidthWideband
	}
	if e.dredEncodingActive() {
		if dredPacket, ok, err := e.maybeBuildMultiFrameDREDPacket(frames, ModeSILK, packetBW, frameSize, encFrameSize, firstFrameMaxBytes, e.packetStereoForMode(ModeSILK), !sameSize, nil); err != nil {
			return nil, err
		} else if ok {
			return dredPacket, nil
		}
	}
	return BuildMultiFramePacket(frames, types.ModeSILK, packetBW, encFrameSize, e.packetStereoForMode(ModeSILK), !sameSize)
}

// ensureSILKEncoder creates the SILK encoder if it doesn't exist.
func (e *Encoder) ensureSILKEncoder() {
	bw := e.silkBandwidth()
	if e.silkEncoder != nil && e.silkEncoder.Bandwidth() == bw {
		e.silkEncoder.SetReducedDependency(e.predictionDisabled)
		return
	}
	e.silkEncoder = silk.NewEncoder(bw)
	e.silkEncoder.SetComplexity(int(e.complexity))
	e.silkEncoder.SetReducedDependency(e.predictionDisabled)
	// Mono SILK handoff state tracks the two-sample sMid history across frames.
	// Reset whenever the SILK core bandwidth/sample-rate changes.
	e.silkMonoInputHist = [2]float32{}
}

// ensureSILKSideEncoder creates the SILK side channel encoder for stereo hybrid mode.
func (e *Encoder) ensureSILKSideEncoder() {
	if e.channels != 2 {
		return
	}
	bw := e.silkBandwidth()
	if e.silkSideEncoder != nil && e.silkSideEncoder.Bandwidth() == bw {
		e.silkSideEncoder.SetReducedDependency(e.predictionDisabled)
		return
	}
	e.silkSideEncoder = silk.NewEncoder(bw)
	e.silkSideEncoder.SetComplexity(int(e.complexity))
	e.silkSideEncoder.SetReducedDependency(e.predictionDisabled)
}

func (e *Encoder) ensureSILKResampler(rate int) {
	if rate <= 0 {
		return
	}
	if e.silkResampler == nil || e.silkResamplerRate != int32(rate) {
		e.silkResampler = silk.NewDownsamplingResampler(48000, rate)
		e.silkResamplerRate = int32(rate)
		e.silkResamplerRight = nil
		if e.channels == 2 {
			e.silkResamplerRight = silk.NewDownsamplingResampler(48000, rate)
		}
		return
	}
	if e.channels == 2 && e.silkResamplerRight == nil {
		e.silkResamplerRight = silk.NewDownsamplingResampler(48000, rate)
	}
}

func (e *Encoder) ensureSilkVAD() {
	if e.silkVAD == nil {
		e.silkVAD = NewVADState()
	}
}

func (e *Encoder) ensureSilkVADMidFeedback() {
	if e.silkVADMidFeedback == nil {
		e.silkVADMidFeedback = NewVADState()
	}
}

func (e *Encoder) ensureSilkVADSide() {
	if e.silkVADSide == nil {
		e.silkVADSide = NewVADState()
	}
}

func (e *Encoder) alignSilkMonoInput(in []float32) []float32 {
	n := len(in)
	if n == 0 {
		return in
	}
	if cap(e.scratchSilkAligned) < n {
		e.scratchSilkAligned = make([]float32, n)
	}
	out := e.scratchSilkAligned[:n]
	out[0] = e.silkMonoInputHist[1]
	if n > 1 {
		copy(out[1:], in[:n-1])
		e.silkMonoInputHist[0] = in[n-2]
		e.silkMonoInputHist[1] = in[n-1]
	} else {
		e.silkMonoInputHist[0] = e.silkMonoInputHist[1]
		e.silkMonoInputHist[1] = in[0]
	}
	return out
}

// updateOpusVADRes updates the Opus-level VAD activity state from the tonality analyzer.
// This mirrors opus_encoder.c behavior where SILK VAD is suppressed if Opus VAD is inactive.
func (e *Encoder) updateOpusVADRes(pcm []opusRes, frameSize int) {
	if frameSize <= 0 || len(pcm) == 0 {
		e.lastOpusVADValid = false
		e.lastOpusVADActive = true
		e.lastOpusVADProb = 1.0
		return
	}
	isSilence := isDigitalSilenceRes(pcm, e.lsbDepth)
	if isSilence {
		// Match libopus opus_encoder.c: digital silence forces activity=0
		// before any tonality/VAD analysis is considered.
		e.lastOpusVADProb = 0
		e.lastOpusVADValid = true
		e.lastOpusVADActive = false
		return
	}

	analysisValid := false
	analysisProb := float32(1.0)

	// libopus opus_encoder.c derives the Opus-level VAD activity from the
	// already-computed analysis_info (run_analysis result carried into
	// opus_encode_frame_native); it never re-runs the tonality analysis here.
	// Re-running RunAnalysis would mutate the analyzer (advance write_pos and the
	// read cursor, re-buffer the same PCM), desynchronising curr_lookahead for
	// the next frame's mode-decision tonality_get_info. Reuse the last analysis
	// snapshot exactly as libopus reuses analysis_info.
	if e.lastAnalysisFresh {
		e.lastAnalysisFresh = false
		analysisValid = e.lastAnalysisValid
		analysisProb = e.lastAnalysisInfo.VADProb
	} else if e.lastAnalysisValid {
		analysisValid = true
		analysisProb = e.lastAnalysisInfo.VADProb
	}

	// Match libopus peak signal energy tracking in opus_encoder.c.
	// Update when analysis is invalid or clearly active (> threshold), and skip
	// true digital silence frames.
	if e.dtx != nil && (!analysisValid || analysisProb > DTXActivityThreshold) && !isSilence {
		frameEnergy := computeFrameEnergyRes(pcm)
		e.dtx.peakSignalEnergy = maxf(0.999*e.dtx.peakSignalEnergy, frameEnergy)
	}

	e.lastOpusVADProb = analysisProb
	e.lastOpusVADValid = analysisValid
	if !analysisValid {
		// Mirror libopus activity=VAD_NO_DECISION behavior for SILK/hybrid lanes:
		// do not clamp SILK VAD when Opus analysis is unavailable.
		e.lastOpusVADActive = true
		return
	}

	active := analysisProb >= DTXActivityThreshold
	if !active {
		// Match libopus safety net: if this "noise" frame is loud enough
		// relative to the tracked peak, keep activity active.
		frameEnergy := computeFrameEnergyRes(pcm)
		peak := opusVal32(0)
		if e.dtx != nil {
			peak = e.dtx.peakSignalEnergy
		}
		active = peak < pseudoSNRThreshold*frameEnergy
	}
	e.lastOpusVADActive = active
}

func (e *Encoder) clearOpusVADDecision() {
	e.lastOpusVADValid = false
	e.lastOpusVADActive = true
	e.lastOpusVADProb = 1.0
}

func (e *Encoder) updateRestrictedSilkOpusVADRes(pcm []opusRes, frameSize int) {
	if frameSize > 0 && len(pcm) > 0 && isDigitalSilenceRes(pcm, e.lsbDepth) {
		e.lastOpusVADProb = 0
		e.lastOpusVADValid = true
		e.lastOpusVADActive = false
		return
	}
	e.clearOpusVADDecision()
}

func computeSilkVADFrameState(state *VADState, mono []float32, frameSamples, fsKHz int) (silk.VADFrameState, bool) {
	if state == nil || frameSamples <= 0 || fsKHz <= 0 || len(mono) < frameSamples {
		return silk.VADFrameState{}, false
	}
	activityQ8, active := state.GetSpeechActivity(mono, frameSamples, fsKHz)
	return silk.VADFrameState{
		SpeechActivityQ8:     int32(activityQ8),
		InputTiltQ15:         state.InputTiltQ15,
		InputQualityBandsQ15: state.InputQualityBandsQ15,
		Valid:                true,
	}, active
}

func applySilkVADFrameState(enc *silk.Encoder, state silk.VADFrameState) {
	if enc == nil || !state.Valid {
		return
	}
	enc.SetVADState(state.SpeechActivityQ8, state.InputTiltQ15, state.InputQualityBandsQ15)
}

// applyOpusVADToSilkState mirrors libopus silk_encode_do_VAD_Fxx:
// when Opus VAD is inactive but SILK VAD is active, clamp SILK activity to
// just below threshold so SILK emits a no-voice frame.
func (e *Encoder) applyOpusVADToSilkState(state silk.VADFrameState, active bool) (silk.VADFrameState, bool) {
	if !state.Valid {
		return state, active
	}
	if e.lastOpusVADValid && !e.lastOpusVADActive && state.SpeechActivityQ8 >= speechActivityThresholdQ8 {
		state.SpeechActivityQ8 = speechActivityThresholdQ8 - 1
		active = false
	}
	return state, active
}

func (e *Encoder) computeSilkVAD(mono []float32, frameSamples, fsKHz int) bool {
	if frameSamples <= 0 || fsKHz <= 0 {
		e.lastVADValid = false
		return false
	}
	e.ensureSilkVAD()
	state, active := computeSilkVADFrameState(e.silkVAD, mono, frameSamples, fsKHz)
	if !state.Valid {
		e.lastVADValid = false
		return false
	}
	state, active = e.applyOpusVADToSilkState(state, active)
	e.lastVADActivityQ8 = state.SpeechActivityQ8
	e.lastVADInputTiltQ15 = state.InputTiltQ15
	e.lastVADInputQualityBandsQ15 = state.InputQualityBandsQ15
	e.lastVADActive = active
	e.lastVADValid = true
	return active
}

func (e *Encoder) computeSilkVADSide(mono []float32, frameSamples, fsKHz int) bool {
	if frameSamples <= 0 || fsKHz <= 0 {
		return false
	}
	e.ensureSilkVADSide()
	state, active := computeSilkVADFrameState(e.silkVADSide, mono, frameSamples, fsKHz)
	state, active = e.applyOpusVADToSilkState(state, active)
	return active
}

func computeSilkFrameLayout(pcmLen, fsKHz int) (frameSamples, nFrames int) {
	if pcmLen <= 0 || fsKHz <= 0 {
		return 0, 0
	}
	frameSamples = fsKHz * 20
	if frameSamples <= 0 {
		return 0, 0
	}
	if pcmLen < frameSamples {
		frameSamples = pcmLen
	}
	nFrames = pcmLen / frameSamples
	if nFrames < 1 {
		nFrames = 1
	}
	if nFrames > silk.MaxFramesPerPacket {
		nFrames = silk.MaxFramesPerPacket
	}
	return frameSamples, nFrames
}

func (e *Encoder) computeSilkVADFlagsAndStates(pcm []float32, fsKHz int) ([]bool, []silk.VADFrameState, int) {
	frameSamples, nFrames := computeSilkFrameLayout(len(pcm), fsKHz)
	if nFrames == 0 {
		e.lastVADValid = false
		return nil, nil, 0
	}
	e.ensureSilkVAD()
	flags := e.scratchVADFlags[:nFrames]
	states := e.scratchVADStates[:nFrames]
	for i := 0; i < nFrames; i++ {
		start := i * frameSamples
		end := start + frameSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		framePCM := pcm[start:end]
		state, active := computeSilkVADFrameState(e.silkVAD, framePCM, len(framePCM), fsKHz)
		state, active = e.applyOpusVADToSilkState(state, active)
		flags[i] = active
		states[i] = state
		if state.Valid {
			e.lastVADActivityQ8 = state.SpeechActivityQ8
			e.lastVADInputTiltQ15 = state.InputTiltQ15
			e.lastVADInputQualityBandsQ15 = state.InputQualityBandsQ15
			e.lastVADValid = true
			e.lastVADActive = active
		} else {
			e.lastVADValid = false
		}
	}
	return flags, states, nFrames
}

func (e *Encoder) ensureSilkResampled(size int) []float32 {
	if size <= 0 {
		return nil
	}
	if cap(e.silkResampled) < size {
		e.silkResampled = make([]float32, size)
	}
	return e.silkResampled[:size]
}

func (e *Encoder) ensureSilkResampledR(size int) []float32 {
	if size <= 0 {
		return nil
	}
	if cap(e.silkResampledR) < size {
		e.silkResampledR = make([]float32, size)
	}
	return e.silkResampledR[:size]
}

// ensureCELTEncoder creates the CELT encoder if it doesn't exist.
func (e *Encoder) ensureCELTEncoder() {
	if e.celtEncoder == nil {
		e.celtEncoder = celt.NewEncoder(int(e.channels))
		e.celtEncoder.SetComplexity(int(e.complexity))
		// Opus encoder already rounds input to the configured LSB depth.
		e.celtEncoder.SetLSBQuantizationEnabled(false)
		// Opus encoder already applies dc_reject at the top level.
		e.celtEncoder.SetDCRejectEnabled(false)
		// Opus encoder already applies CELT delay compensation at the top level.
		e.celtEncoder.SetDelayCompensationEnabled(false)
		e.celtEncoder.SetPhaseInversionDisabled(e.phaseInversionDisabled)
	}
	e.syncQEXTToCELT()
	e.celtEncoder.SetPrediction(e.celtPredictionMode())
	e.celtEncoder.SetLFE(e.lfe)
	e.celtEncoder.SetSurroundTrim(e.celtSurroundTrim)
	e.syncCELTEnergyMask()
	e.celtEncoder.SetConstrainedVBRBoundScale(e.celtCVBRBoundScale)
	e.celtEncoder.SetStreamChannels(int(e.streamChannels))
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetPacketLoss(int(e.packetLoss))
}

// silkBandwidth converts the Opus bandwidth to SILK bandwidth.
func (e *Encoder) silkBandwidth() silk.Bandwidth {
	switch e.effectiveBandwidth() {
	case types.BandwidthNarrowband:
		return silk.BandwidthNarrowband
	case types.BandwidthMediumband:
		return silk.BandwidthMediumband
	case types.BandwidthWideband:
		return silk.BandwidthWideband
	case types.BandwidthSuperwideband, types.BandwidthFullband:
		return silk.BandwidthWideband
	default:
		return silk.BandwidthWideband
	}
}

// ValidFrameSize returns true if the frame size is valid for the given mode.
func ValidFrameSize(frameSize int, mode Mode) bool {
	switch mode {
	case ModeSILK:
		return frameSize == 480 || frameSize == 960 || frameSize == 1920 ||
			frameSize == 2880 || frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	case ModeHybrid:
		return frameSize == 480 || frameSize == 960 || frameSize == 1920 ||
			frameSize == 2880 || frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	case ModeCELT:
		return frameSize == 120 || frameSize == 240 || frameSize == 480 ||
			frameSize == 960 || frameSize == 1920 || frameSize == 2880 ||
			frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	default:
		return frameSize == 120 || frameSize == 240 || frameSize == 480 ||
			frameSize == 960 || frameSize == 1920 || frameSize == 2880 ||
			frameSize == 3840 || frameSize == 4800 || frameSize == 5760
	}
}

// SetSignalType sets the signal type hint for mode selection.
func (e *Encoder) SetSignalType(signal types.Signal) {
	e.signalType = signal
}

// SignalType returns the current signal type hint.
func (e *Encoder) SignalType() types.Signal {
	return e.signalType
}

// LastSilkVADActivity returns the last SILK VAD speech activity (Q8, 0-255).
func (e *Encoder) LastSilkVADActivity() int {
	return int(e.lastVADActivityQ8)
}

// LastSilkVADInputTiltQ15 returns the last SILK VAD input tilt (Q15).
func (e *Encoder) LastSilkVADInputTiltQ15() int {
	return int(e.lastVADInputTiltQ15)
}

// LastOpusVADProb returns the last Opus-level VAD probability (0..1).
func (e *Encoder) LastOpusVADProb() float32 {
	return e.lastOpusVADProb
}

// LastOpusVADActive returns whether the Opus-level VAD classified the last frame as active.
func (e *Encoder) LastOpusVADActive() bool {
	return e.lastOpusVADActive
}

// LastSilkLTPCorr returns the last SILK pitch correlation estimate.
func (e *Encoder) LastSilkLTPCorr() float32 {
	if e.silkEncoder == nil {
		return 0
	}
	return e.silkEncoder.LTPCorr()
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
	if e.celtEncoder != nil {
		e.celtEncoder.SetLFE(enabled)
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// LFE reports whether LFE mode is enabled.
func (e *Encoder) LFE() bool {
	return e.lfe
}

// Lookahead returns the encoder's algorithmic delay in samples at 48kHz.
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

// SetPredictionDisabled disables inter-frame prediction.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
	if e.silkEncoder != nil {
		e.silkEncoder.SetReducedDependency(disabled)
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.SetReducedDependency(disabled)
	}
	if e.celtEncoder != nil {
		e.celtEncoder.SetPrediction(e.celtPredictionMode())
	}
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

// SetCELTSurroundTrim sets the CELT alloc-trim surround bias.
func (e *Encoder) SetCELTSurroundTrim(trim opusVal32) {
	e.celtSurroundTrim = trim
	if e.celtEncoder != nil {
		e.celtEncoder.SetSurroundTrim(trim)
	}
}

// CELTSurroundTrim returns the current CELT alloc-trim surround bias.
func (e *Encoder) CELTSurroundTrim() opusVal32 {
	return e.celtSurroundTrim
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
		return
	}
	if cap(e.celtEnergyMask) < needed {
		e.celtEnergyMask = make([]float32, needed)
	} else {
		e.celtEnergyMask = e.celtEnergyMask[:needed]
	}
	copy(e.celtEnergyMask, mask[:needed])
	e.syncCELTEnergyMask()
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
	n := len(e.celtEnergyMask)
	if n > celt.MaxBands*2 {
		n = celt.MaxBands * 2
	}
	e.celtEncoder.SetEnergyMask(e.celtEnergyMask[:n])
}
