// Package encoder implements the unified Opus encoder defined by RFC 6716. It is
// a behavior-for-behavior Go port of the orchestration layer in libopus 1.6.1
// (src/opus_encoder.c): it owns the SILK and CELT sub-encoders, decides which of
// the three coding modes to use for each frame, runs the rate/bandwidth control
// loop, and assembles the final Opus packet.
//
// # Coding modes
//
// Every Opus frame is coded in exactly one mode (RFC 6716 Section 2):
//
//   - SILK-only (configs 0-11): linear-prediction speech coder for narrowband
//     through wideband, the lowest-rate VoIP path.
//   - Hybrid (configs 12-15): SILK codes the 0-8kHz core while CELT codes the
//     8-20kHz high band, for super-wideband and fullband speech.
//   - CELT-only (configs 16-31): transform coder for music and low-latency audio.
//
// ModeAuto lets the encoder choose per frame from signal type, bitrate and the
// tonality analyzer, mirroring the decision chain in opus_encoder.c.
//
// # Pipeline
//
// Encode and its variants run the libopus opus_encode_native pipeline for one
// frame: optional variable high-pass / DC rejection on the input, the tonality
// analysis ("the brain", see TonalityAnalysisState), mode and bandwidth
// selection, delay compensation and mode-transition prefill, the SILK/CELT/Hybrid
// bridge, the VBR/CBR/CVBR rate controller, DTX activity detection, and packet
// assembly (see BuildPacket). Sub-encoders and large scratch buffers are created
// lazily and reused across frames so steady-state encoding is allocation-free.
//
// # Determinism and parity
//
// The package is written to match libopus output frame-for-frame: the internal
// numeric types deliberately mirror the C types (opus_val16/opus_val32/opus_res),
// and FinalRange exposes the range-coder state so output can be checked against a
// reference encoder. Set the same controls (bitrate, complexity, VBR, FEC, DTX,
// bandwidth) in the same order as libopus to reproduce its bitstream.
//
// References: RFC 6716; libopus 1.6.1 src/opus_encoder.c, src/analysis.c.
package encoder

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
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
	// defaultScratchPacketBytes holds a single-frame packet: the TOC byte plus
	// a 1275-byte frame (opus_encode_frame_native max_data_bytes cap).
	defaultScratchPacketBytes   = libopusMaxDataBytesCap
	extensionScratchPacketBytes = 3826
)

// In-band FEC configuration values accepted by Encoder.SetInBandFEC, matching
// the libopus OPUS_SET_INBAND_FEC argument (src/opus_encoder.c).
const (
	// InBandFECDisabled turns in-band forward error correction off.
	InBandFECDisabled = 0
	// InBandFECEnabled enables LBRR-based FEC for all SILK/Hybrid frames that
	// carry speech (libopus value 1).
	InBandFECEnabled = 1
	// InBandFECMusicSafe enables FEC but lets the encoder suppress it on frames
	// classified as music, trading resilience for quality (libopus value 2).
	InBandFECMusicSafe = 2
)

// Encoder is the unified Opus encoder that orchestrates SILK and CELT sub-encoders.
type Encoder struct {
	// Sub-encoders (created lazily)
	silk        *silk.PacketEncoder
	celtEncoder *celt.Encoder

	// silkMode mirrors libopus st->silk_mode: the controls handed to silk_Encode
	// for the current frame and the status it reports back.
	silkMode silk.EncControl
	// silkPrefillPending marks that silkPrefill holds the 10 ms CELT->SILK
	// transition prefill for the next silk_Encode call of this frame.
	silkPrefillPending bool
	// silkRangeEncoder and silkPayload hold a SILK-only frame's range coder and
	// payload (opus_encode_frame_native's ec_enc over data+1).
	silkRangeEncoder rangecoding.Encoder
	silkPayload      []byte
	// silkFinalRange is the final range of the last SILK-only frame.
	silkFinalRange uint32

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
	fecEnabled        bool
	packetLoss        int32 // Expected packet loss percentage (0-100)
	lastOpusVADActive bool
	lastOpusVADValid  bool
	lastOpusVADProb   float32
	// multiFrameDTXCount is the number of internal sub-frames the most recent
	// encode*MultiFramePacket call suppressed via the per-sub-frame DTX decision
	// (libopus opus_encoder.c dtx_count). It is transient per Encode call.
	multiFrameDTXCount int
	// multiFrameLastSubframeDTX records whether the final internal sub-frame of
	// the most recent encode*MultiFramePacket call was DTX-suppressed. libopus
	// reports st->rangeFinal from the last opus_encode_frame_native call in the
	// repacketizer loop, and that call zeroes rangeFinal when it DTXes
	// (opus_encoder.c:2569), so a packet whose last sub-frame is suppressed has a
	// final range of 0. It is transient per Encode call.
	multiFrameLastSubframeDTX bool
	fec                       *fecState

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
	// intMode / intBandwidth mirror libopus opus_encoder.c st->mode / st->bandwidth:
	// the internal selected mode/bandwidth state carried between frames. They are
	// initialized to MODE_HYBRID / OPUS_BANDWIDTH_FULLBAND (opus_encoder.c:319-320)
	// and updated to the actual selected values after every full encode. The
	// low-rate "PLC frame" early-exit (opus_encoder.c:1340) reads these STALE values
	// to build its minimal TOC-only packet, before the per-frame mode/bandwidth
	// selection would overwrite them.
	intMode      Mode
	intBandwidth types.Bandwidth
	inputBuffer  []opusRes
	delayBuffer  []opusRes

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

	// pcmBump backs the three frameSize-sized input-domain PCM scratch buffers
	// (scratchInputPCM/scratchQuantPCM/scratchDCPCM) with one contiguous
	// allocation, carved per-frame at the encode entry and re-carved only when a
	// larger frame is seen (so it sizes to the current frame, not the max).
	pcmBump arena.Bump[opusRes]

	// Scratch buffers for zero-allocation encoding
	scratchDCPCM    []opusRes // DC rejected PCM buffer
	scratchInputPCM []opusRes // Public PCM rounded into the libopus opus_res domain
	scratchPacket   []byte    // Output packet buffer
	// Reusable long-packet assembly scratch (40/60/80/100/120 ms paths).
	scratchFrameSlots       [6][]byte // Per-subframe slice headers for long packets
	scratchFrameBytes       []byte    // Backing storage for kept subframe payloads
	scratchQEXTPayloadBytes []byte    // Backing storage for kept QEXT payloads
	scratchDelayedPCM       []opusRes // Delay-compensated CELT input
	scratchDelayState       []opusRes // Packet-local delay history for transition-prefill replay
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
	switch sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
	default:
		sampleRate = 48000
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	e := &Encoder{
		mode:                   ModeAuto,
		bandwidth:              types.BandwidthFullband,
		sampleRate:             int32(sampleRate),
		channels:               int32(channels),
		frameSize:              int32(sampleRate / 50),
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
		scratchPacket:          make([]byte, defaultScratchPacketBytes),
		prevMode:               ModeAuto,
		prevPacketMode:         ModeAuto,
		prevAutoMode:           ModeAuto,
		intMode:                ModeHybrid,
		intBandwidth:           types.BandwidthFullband,
		voiceRatio:             -1,
		streamChannels:         int32(channels),
		prevChannels:           int32(channels),
		autoBandwidth:          types.BandwidthFullband,
		first:                  true,
	}
	return e
}

// SetMode sets the encoding mode.
func (e *Encoder) SetMode(mode Mode) {
	e.mode = mode
}

// Mode returns the current encoding mode.
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
// is 960, matching the legacy 48 kHz-relative frame-size convention.
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
	if e.silk != nil {
		e.silk.Init()
	}
	e.silkPrefillPending = false
	e.silkFinalRange = 0
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
	e.intMode = ModeHybrid
	e.intBandwidth = types.BandwidthFullband
	e.detectedBandwidth = 0
	// C ref: opus_encoder.c OPUS_RESET_STATE sets st->bandwidth =
	// OPUS_BANDWIDTH_FULLBAND. st->bandwidth (the decided bandwidth reported by
	// OPUS_GET_BANDWIDTH) sits after OPUS_ENCODER_RESET_START, so the reset
	// region clears it and the handler re-seeds it to FULLBAND. The user
	// bandwidth request (userBandwidth/userBandwidthSet) is before the reset
	// start and is preserved.
	e.bandwidth = types.BandwidthFullband
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
		return e.silkFinalRange
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

func (e *Encoder) bitrateToBits(bitrate int, frameSize int) int {
	return (bitrate * frameSize) / int(e.sampleRate)
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
	// Back the three frameSize-sized input-domain PCM scratch buffers with one
	// contiguous arena (carved to the current frame; the ensure* helpers reslice
	// within their slots, falling back to a fresh make only if a stage ever needs
	// more than expectedLen).
	if expectedLen > 0 {
		e.pcmBump.Ensure(3 * expectedLen)
		e.scratchInputPCM = e.pcmBump.AllocN(expectedLen)
		e.scratchQuantPCM = e.pcmBump.AllocN(expectedLen)
		e.scratchDCPCM = e.pcmBump.AllocN(expectedLen)
	}
	inputPCM := e.ensureInputPCM(expectedLen)
	copy(inputPCM, pcm[:expectedLen])
	e.SetFloatInputFrame(pcm)
	defer e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}

// encodeOpusResWithAnalysisMaxBytes is the core single-frame encode pipeline,
// the Go counterpart of libopus opus_encode_native (src/opus_encoder.c). All
// public Encode* entry points funnel here after converting their input to the
// internal opusRes representation.
//
// inputPCM is one frame of interleaved samples (frameSize per channel);
// maxDataBytes is the caller's output budget after packet-size clamping; and
// refreshAnalysis, if non-nil, runs the tonality analyzer on the untouched input
// before any high-pass/DC/LSB processing, matching libopus run_analysis ordering.
//
// The function applies LSB quantization and the variable high-pass / DC-reject
// filters, refreshes the SILK variable-HP-cutoff smoother in the
// hp_cutoff-before-silk_Encode order libopus uses, handles the "too little
// space" TOC-only fast path, selects the coding mode and bandwidth (auto chain or
// forced mode), performs delay compensation and mode-transition prefill, drives
// the SILK/CELT/Hybrid sub-encoders under the active rate-control mode, and
// returns the assembled packet (or nil when more lookahead input is still
// buffered). It returns ErrInvalidFrameSize / ErrEncodingFailed for malformed
// requests and never panics on valid configuration.
func (e *Encoder) encodeOpusResWithAnalysisMaxBytes(inputPCM []opusRes, frameSize int, maxDataBytes int, refreshAnalysis func()) ([]byte, error) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// A non-positive frame size is never a valid Opus duration; reject it before
	// any sampleRate/frameSize division (libopus opus_encode_native returns
	// OPUS_BAD_ARG). Without this guard frameSize==0 passes the length check below
	// (expectedLen==0) and divides by zero in the frame-rate computation.
	if frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}
	expectedLen := frameSize * channels
	if len(inputPCM) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if maxDataBytes <= 0 {
		return nil, ErrEncodingFailed
	}
	// Just avoid insane packet sizes here; the per-frame caps apply later.
	packetCapBytes := libopusMaxDataBytesCap * 6
	if maxDataBytes > packetCapBytes {
		maxDataBytes = packetCapBytes
	}
	// e.bitrate carries st->bitrate_bps for this frame: the user bitrate bounded
	// by the output budget (user_bitrate_to_bitrate) and, in CBR, rounded to the
	// whole-byte frame size below.
	userBitrate := e.bitrate
	defer func() {
		e.bitrate = userBitrate
	}()
	e.bitrate = int32(e.resolvedBitrateForFrame(frameSize, maxDataBytes))
	isSilence := isDigitalSilenceRes(inputPCM, e.lsbDepth)
	e.hasCELTPrefill = false
	e.silkPrefillPending = false
	e.clearFixedCELTUsed()
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
	pcmRes := e.quantizeInputToLSBDepth(inputPCM)
	pcmRes = e.preprocessInputHP(pcmRes, frameSize)
	frameEnd := frameSize * channels
	samplesNeeded := frameEnd + lookaheadSamples
	directFrameInput := lookaheadSamples == 0 && len(e.inputBuffer) == 0
	var framePCM []opusRes
	if directFrameInput {
		framePCM = pcmRes[:frameEnd]
	} else {
		e.inputBuffer = append(e.inputBuffer, pcmRes...)
		if len(e.inputBuffer) < samplesNeeded {
			return nil, nil
		}
		framePCM = e.inputBuffer[:frameEnd]
	}

	// libopus "too little space" fast path (opus_encoder.c:1340). The resolved
	// bitrate is already in e.bitrate; derive the CBR-clamped budget and effective
	// bitrate, then emit a minimal TOC-only packet when neither the byte budget
	// nor the bitrate can support a real encode. This mirrors the per-stream
	// curr_max squeeze the multistream encoder applies to high-channel layouts.
	frameRate := sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 1
	}
	cbrMaxDataBytes := maxDataBytes
	if e.bitrateMode == ModeCBR {
		// src/opus_encoder.c:1327-1334: CBR codes whole bytes, so the frame
		// bitrate is the one the rounded byte count carries.
		cbrBytes := min((bitrateToBitsFs(int(e.bitrate), sampleRate, frameSize)+4)/8, maxDataBytes)
		e.bitrate = int32(bitsToBitrateFs(cbrBytes*8, sampleRate, frameSize))
		cbrMaxDataBytes = max(1, cbrBytes)
	}
	effBitrate := int(e.bitrate)
	if e.dredEncodingActive() {
		if plan, ok := e.computeDREDEmissionPlan(frameSize); ok {
			effBitrate -= int(plan.bitrate)
			if effBitrate < 0 {
				effBitrate = 0
			}
		}
	}
	if cbrMaxDataBytes < 3 || effBitrate < 3*frameRate*8 ||
		(frameRate < 50 && (cbrMaxDataBytes*frameRate < 300 || effBitrate < 2400)) {
		pkt, err := e.emitLowSpacePacket(frameSize, maxDataBytes, cbrMaxDataBytes, effBitrate)
		if err != nil {
			return nil, err
		}
		if !directFrameInput {
			remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
			e.inputBuffer = e.inputBuffer[:remaining]
		}
		return pkt, nil
	}

	var requestedMode Mode
	if e.mode == ModeAuto {
		// Full libopus auto-mode decision chain: voice_ratio, stereo_width,
		// stream_channels, mode threshold interpolation, auto-bandwidth,
		// bandwidth clamping, decide_fec, mode fixup.
		requestedMode = e.autoModeAndBandwidthDecision(framePCM, frameSize, cbrMaxDataBytes, isSilence)
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
		e.bandwidth = e.autoClampBandwidth(e.bandwidth, requestedMode, equivRate, e.maxRateForFrame(frameSize, cbrMaxDataBytes))
		bw := e.bandwidth
		e.lbrrCoded = decideFEC(e.fecEnabled, e.packetLoss, e.lbrrCoded,
			requestedMode, &bw, equivRate)
		e.bandwidth = bw
		// libopus opus_encoder.c:1688-1695: only the restricted-SILK
		// application pins the bandwidth to WB; a plain forced-SILK request
		// with a wider bandwidth is promoted to Hybrid (and forced Hybrid at
		// <=WB drops to SILK), exactly like the auto path.
		if e.restrictedSilkApp && e.bandwidth > types.BandwidthWideband {
			e.bandwidth = types.BandwidthWideband
		}
		requestedMode = autoModeFixup(requestedMode, e.bandwidth)
	}
	actualMode, prevModeNext := e.applyCELTTransitionDelay(frameSize, requestedMode)
	transitionToCELT := requestedMode == ModeCELT && actualMode != ModeCELT

	dredExtraDelay := 0
	if !e.lowDelay {
		dredExtraDelay = sampleRate / 250
	}
	f20 := e.frame20ms()
	dredInSubframes := (actualMode == ModeCELT && frameSize > f20 && frameSize%f20 == 0) ||
		(actualMode == ModeHybrid && frameSize > f20 && frameSize%f20 == 0) ||
		(actualMode == ModeSILK && frameSize > 3*f20)
	if e.dredEncodingActive() && !dredInSubframes {
		e.processDREDLatentsForPacket(framePCM, frameSize, dredExtraDelay, actualMode)
	} else if !e.dredEncodingActive() {
		e.clearInactiveDREDHistory()
	}

	// DTX activity detection matches libopus opus_encoder.c:1246+1911-1930:
	// is_digital_silence() and compute_frame_energy() run on the original
	// unfiltered PCM (before hp_cutoff/dc_reject). The Opus-level VAD/peak-energy
	// activity is computed below (updateOpusVADRes / CELT noise-energy branch);
	// the decide_dtx_mode() counter update runs AFTER the frame is fully encoded
	// so the encoder state advances exactly as libopus does before discarding the
	// payload for a 1-byte DTX continuation packet (opus_encoder.c:2564-2572).
	// Multi-frame packets (>20ms CELT/Hybrid, >60ms SILK) compute the Opus-level
	// activity and peak-signal-energy per internal sub-frame inside their encode
	// loop, mirroring libopus opus_encode_native (opus_encoder.c:1769-1830) which
	// calls opus_encode_frame_native — and thus the activity/peak tracking and
	// decide_dtx_mode — once per sub-frame. Tracking it here on the full packet
	// would double-count peak energy and advance the DTX counter at the wrong
	// granularity, so it is skipped for those packets.
	multiFrame := e.isMultiFramePacket(actualMode, frameSize)
	if actualMode == ModeCELT && !multiFrame {
		e.updateCELTOnlyOpusVADRes(inputPCM, frameSize)
	}

	if !multiFrame && ((actualMode == ModeSILK && frameSize <= 3*f20) || (actualMode == ModeHybrid && frameSize <= f20)) {
		// Opus-level activity mirrors libopus opus_encoder.c:1888-1930. The
		// analysis-driven path (updateOpusVADRes) already reproduces the
		// VAD_NO_DECISION behaviour when the tonality analysis did not run
		// (lastAnalysisValid==false, e.g. restricted-silk application,
		// complexity<7, or out-of-range Fs): it leaves lastOpusVADValid false so
		// resolveDTXActivity() falls back to the SILK signal type just like
		// libopus resolves VAD_NO_DECISION from signalType at line 2235. Peak
		// signal energy is tracked there in every case, matching line 1312.
		e.updateOpusVADRes(vadPCM, frameSize)
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
	e.multiFrameDTXCount = 0
	e.multiFrameLastSubframeDTX = false
	switch actualMode {
	case ModeSILK:
		e.maybePrefillSILKOnModeTransition(actualMode, true, true)
		// SILK-only frames carry HB_gain = 1; the CELT-side fade has no consumer
		// here, but prev_HB_gain still resets to one.
		if e.hybridState != nil {
			e.hybridState.prevHBGain = 1
		}
		if frameSize > 3*f20 {
			packet, err = e.encodeSILKMultiFramePacket(framePCM, vadPCM, frameSize, int(e.bitrate), int(encodingBitrate), dredBitrate, dredExtraDelay, maxDataBytes)
		} else {
			originalBitrate := e.bitrate
			if encodingBitrate != originalBitrate {
				e.bitrate = encodingBitrate
			}
			dredNoDecision := e.dredEncodingActive() && !e.lastOpusVADValid
			frameData, err = e.encodeSILKFrameWithDRED(framePCM, frameSize, cbrMaxDataBytes, dredBitrate)
			if encodingBitrate != originalBitrate {
				e.bitrate = originalBitrate
			}
			if err == nil && dredNoDecision {
				e.backfillDREDActivityForFrame(frameSize, e.silkMode.SignalType != 0)
			}
		}
		e.updateDelayBuffer(framePCM, frameSize)
		e.applyStereoWidthReduction(ModeSILK, nil, frameSize)
	case ModeHybrid:
		if frameSize > f20 {
			delayState := e.ensureDelayState(len(e.delayBuffer))
			copy(delayState, e.delayBuffer)
			celtPCM := e.applyDelayCompensation(framePCM, frameSize)
			packet, err = e.encodeHybridMultiFramePacket(framePCM, celtPCM, vadPCM, delayState, frameSize, transitionToCELT, int(e.bitrate), int(encodingBitrate), dredBitrate, dredExtraDelay, maxDataBytes)
		} else {
			e.maybePrefillSILKOnModeTransition(actualMode, true, true)
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
			frameData, err = e.encodeHybridFrameWithMaxPacketAndTransition(framePCM, celtPCM, frameSize, maxPacketBytes, maxDataBytes, dredBitrate, false, true, transitionToCELT, false)
			if encodingBitrate != originalBitrate {
				e.bitrate = originalBitrate
			}
			if err == nil && dredNoDecision {
				e.backfillDREDActivityForFrame(frameSize, e.silkMode.SignalType != 0)
			}
		}
	case ModeCELT:
		celtPCM := e.prepareCELTPCM(framePCM, frameSize)
		// The transition prefill runs with the frame's CELT rate configuration
		// (opus_encode_frame_native configures CELT before prefilling).
		e.ensureCELTEncoder()
		e.configureCELTRate(ModeCELT, int(encodingBitrate))
		e.maybePrefillCELTOnModeTransition(actualMode)
		if frameSize > f20 {
			// Long CELT packets are encoded as multi-frame packets. The stereo
			// width fade is applied per 20 ms sub-frame inside the loop (matching
			// libopus' per-sub-frame opus_encode_native recursion), not here.
			packet, err = e.encodeCELTMultiFramePacket(framePCM, vadPCM, celtPCM, frameSize, int(e.bitrate), int(encodingBitrate), dredBitrate, dredExtraDelay, maxDataBytes)
		} else {
			// libopus runs gain_fade() and then stereo_fade() on pcm_buf after
			// the delay-buffer copy and the mode-transition prefill, before the
			// main celt_encode_with_ec.
			celtPCM = e.applyUnityHBGainFade(celtPCM)
			celtPCM = e.applyStereoWidthReduction(ModeCELT, celtPCM, frameSize)
			frameData, err = e.encodeCELTFrameWithBitrateMaxPayloadAndDRED(celtPCM, frameSize, int(encodingBitrate), e.celtNbComprBytes(cbrMaxDataBytes), dredBitrate)
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

	// DTX decision (libopus opus_encoder.c:2564-2572): runs decide_dtx_mode AFTER
	// the frame is fully encoded so the encoder state (SILK NSQ/LPC history, CELT
	// energy memory) is advanced exactly as libopus does. When DTX fires the
	// already-encoded payload is discarded and only the 1-byte TOC is emitted; the
	// decoder runs its own comfort-noise generation when it sees a TOC with no
	// frame data.
	//
	// For multi-frame packets the DTX decision was already made per sub-frame
	// inside the encode*MultiFramePacket loop (mirroring libopus, which runs
	// decide_dtx_mode once per sub-frame). When EVERY sub-frame was suppressed
	// libopus' repacketizer emits an unpadded TOC-only packet — pad is
	// !use_vbr && (dtx_count != nb_frames), so all-DTX => no padding
	// (opus_encoder.c:1831). gopus' multi-frame builder already produced exactly
	// that all-empty packet, so it is returned here before the CBR padding step.
	// A partial DTX (some sub-frames carry payload) keeps its mixed packet and
	// flows through the normal CBR-padding path below. The per-sub-frame path is
	// only taken when DRED is not active; DRED multi-frame packets keep the
	// whole-frame DTX decision (their own packet builder owns the DTX-refresh
	// interaction) and so fall through to the else branch.
	perSubframeDTX := multiFrame && !e.dredEncodingActive()
	if e.dtxEnabled && e.dtx != nil && perSubframeDTX {
		subframeCount := e.multiFrameSubframeCount(actualMode, frameSize)
		if subframeCount > 0 && e.multiFrameDTXCount == subframeCount {
			if isConcreteMode(actualMode) {
				e.prevPacketMode = actualMode
			}
			if isConcreteMode(prevModeNext) {
				e.prevMode = prevModeNext
				if e.mode == ModeAuto {
					e.prevAutoMode = prevModeNext
				}
			}
			e.intMode = actualMode
			e.intBandwidth = e.bandwidth
			e.first = false
			e.prevChannels = e.streamChannels
			e.finalRange = 0
			return packet, nil
		}
	} else if e.dtxEnabled && e.dtx != nil {
		activity := e.resolveDTXActivity()
		if e.decideDTXSuppress(activity, frameSize) {
			if isConcreteMode(actualMode) {
				e.prevPacketMode = actualMode
			}
			if isConcreteMode(prevModeNext) {
				e.prevMode = prevModeNext
				if e.mode == ModeAuto {
					e.prevAutoMode = prevModeNext
				}
			}
			// Track libopus st->mode / st->bandwidth so the next frame's low-rate
			// early-exit reads the same stale internal state libopus would.
			e.intMode = actualMode
			e.intBandwidth = e.bandwidth
			e.first = false
			e.prevChannels = e.streamChannels
			e.finalRange = 0
			return e.buildDTXPacketForMode(frameSize, actualMode)
		}
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
		// The TOC config table indexes by the 48 kHz-equivalent frame size
		// (libopus gen_toc derives the period from Fs/frame_size, which is the
		// same duration). For a sub-48 kHz API rate scale the API-rate frameSize
		// up to its 48 kHz core count.
		tocFrameSize := e.packetTOCFrameSize(frameSize)
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
				tocFrameSize,
				stereo,
				qextExtensionID,
				qextPayload,
				0,
				false,
			)
		} else if packet == nil {
			targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
			if e.bitrateMode == ModeCBR && targetSize >= 2+len(frameData) {
				// A CBR packet is padded to cbr_bytes, which exceeds the
				// single-frame 1276 bytes above 510 kb/s at 20 ms
				// (opus_encode_frame_native pads to max_data_bytes).
				e.ensurePacketScratch(targetSize)
				if targetSize == 2+len(frameData) {
					config := configFromParams(modeToTypes(actualMode), packetBW, tocFrameSize)
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
						tocFrameSize,
						stereo,
						nil,
						targetSize,
						true,
					)
				}
			} else {
				packetLen, pktErr = BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), packetBW, tocFrameSize, stereo)
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
	// Track libopus st->mode / st->bandwidth (the internal selected state) so the
	// next frame's low-rate early-exit reads the same stale values libopus would.
	e.intMode = actualMode
	e.intBandwidth = e.bandwidth
	switch e.bitrateMode {
	case ModeCBR:
		if dredPacketBuilt {
			break
		}
		targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
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
				e.packetTOCFrameSize(frameSize),
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
			packet = constrainSize(packet, e.targetBytesForBitrate(int(e.bitrate), frameSize), CVBRTolerance)
		}
	}
	e.prevChannels = e.streamChannels
	// C ref: opus_encode_native sets st->first = 0 here (line 2562), after a
	// frame is committed for every mode (auto, forced SILK/Hybrid/CELT). The
	// low-space and SILK nBytes==0 early returns happen earlier and leave
	// st->first = 1; gopus mirrors that by returning before this point for
	// those cases (emitLowSpacePacket / DTX-suppress set first explicitly).
	e.first = false
	switch {
	case multiFrame && e.multiFrameLastSubframeDTX:
		// libopus reports st->rangeFinal from the last opus_encode_frame_native
		// call in the repacketizer loop. When that final sub-frame DTXes it sets
		// rangeFinal = 0 (opus_encoder.c:2569), so a multi-frame packet whose last
		// internal sub-frame is suppressed has a final range of 0 even though its
		// earlier sub-frames carry payload.
		e.finalRange = 0
	default:
		e.finalRange = e.currentFinalRange(actualMode)
	}
	return packet, nil
}

// emitLowSpacePacket reproduces the libopus opus_encoder.c "too little space to
// do something useful" fast path (lines 1340-1406). When the per-frame byte
// budget or bitrate is too small to run a real encode, libopus emits a minimal
// TOC-only "PLC" packet (1 or 2 bytes), padding it to the CBR budget. The
// multistream encoder squeezes the trailing streams of a high-channel-count
// layout (e.g. third-order ambisonics) down to a 1-2 byte curr_max, which is
// exactly this path; without it gopus errored ("max_data_bytes <= 0") on streams
// that libopus emits as 1-byte minimal packets.
//
// effBitrate is st->bitrate_bps after the CBR cbr_bytes clamp and the DRED
// reservation; cbrMaxDataBytes is the CBR-clamped max_data_bytes (== outDataBytes
// for VBR). outDataBytes is the original caller budget (curr_max).
func (e *Encoder) emitLowSpacePacket(frameSize, outDataBytes, cbrMaxDataBytes, effBitrate int) ([]byte, error) {
	sampleRate := int(e.sampleRate)
	frameRate := sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 1
	}

	// tocmode = st->mode: the internal selected mode carried between frames, seeded
	// to MODE_HYBRID at init (opus_encoder.c:319). libopus maps an unset st->mode
	// (==0) to MODE_SILK_ONLY (opus_encoder.c:1349); intMode is always concrete here,
	// so that fallback is only defensive.
	tocmode := e.intMode
	if !isConcreteMode(tocmode) {
		tocmode = ModeSILK
	}
	if frameRate > 100 {
		tocmode = ModeCELT
	}

	// bw = st->bandwidth==0 ? NB : st->bandwidth (opus_encoder.c:1345). intBandwidth
	// mirrors st->bandwidth: seeded to FULLBAND at init (opus_encoder.c:320) and
	// updated only after a full encode, so the early-exit reads the same stale value
	// libopus would (OPUS_SET_BANDWIDTH writes user_bandwidth, not st->bandwidth).
	bw := max(e.intBandwidth, types.BandwidthNarrowband)

	packetCode := 0
	numMultiframes := 0

	// 40 ms -> 2 x 20 ms if in CELT_ONLY or HYBRID mode.
	if frameRate == 25 && tocmode != ModeSILK {
		frameRate = 50
		packetCode = 1
	}
	// >= 60 ms frames.
	if frameRate <= 16 {
		if outDataBytes == 1 || (tocmode == ModeSILK && frameRate != 10) {
			tocmode = ModeSILK
			if frameRate <= 12 {
				packetCode = 1
			} else {
				packetCode = 0
			}
			if frameRate == 12 {
				frameRate = 25
			} else {
				frameRate = 16
			}
		} else {
			numMultiframes = 50 / frameRate
			frameRate = 50
			packetCode = 3
		}
	}

	// Per-mode bandwidth clamps (libopus lines 1379-1384).
	switch {
	case tocmode == ModeSILK && bw > types.BandwidthWideband:
		bw = types.BandwidthWideband
	case tocmode == ModeCELT && bw == types.BandwidthMediumband:
		bw = types.BandwidthNarrowband
	case tocmode == ModeHybrid && bw <= types.BandwidthSuperwideband:
		bw = types.BandwidthSuperwideband
	}

	stereo := e.packetStereoForMode(tocmode)
	tocByte := lowSpaceTOC(tocmode, frameRate, bw, stereo)
	tocByte |= byte(packetCode)

	ret := 1
	if packetCode > 1 {
		ret = 2
	}
	// libopus pads to IMAX(cbr_max_data_bytes, ret) for CBR.
	padTarget := max(cbrMaxDataBytes, ret)

	pkt := make([]byte, ret)
	pkt[0] = tocByte
	if packetCode == 3 {
		pkt[1] = byte(numMultiframes)
	}

	if e.bitrateMode != ModeCBR {
		return pkt, nil
	}
	// CBR: pad to the budget (opus_packet_pad).
	if padTarget <= ret {
		return pkt, nil
	}
	padded := padToSize(pkt, padTarget)
	return padded, nil
}

// lowSpaceTOC reproduces libopus gen_toc(mode, framerate, bandwidth, channels).
func lowSpaceTOC(mode Mode, framerate int, bw types.Bandwidth, stereo bool) byte {
	period := 0
	for framerate < 400 {
		framerate <<= 1
		period++
	}
	var toc byte
	switch mode {
	case ModeSILK:
		toc = byte((int(bw)-int(types.BandwidthNarrowband))<<5) | byte((period-2)<<3)
	case ModeCELT:
		tmp := max(int(bw)-int(types.BandwidthMediumband), 0)
		toc = 0x80 | byte(tmp<<5) | byte(period<<3)
	default: // Hybrid
		toc = 0x60 | byte((int(bw)-int(types.BandwidthSuperwideband))<<4) | byte((period-2)<<3)
	}
	if stereo {
		toc |= 1 << 2
	}
	return toc
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

// buildDTXPacketForMode assembles the minimal TOC-only packet emitted when DTX
// fires, using the supplied actualMode so the TOC config matches the mode the
// frame would otherwise have used. For SILK it is a single code-0 frame; for
// CELT/Hybrid frames longer than 20ms it builds N zero-length 20ms sub-frames so
// the repacketizer collapses them exactly as libopus does (code 1 for two
// sub-frames, code 3 for three).
func (e *Encoder) buildDTXPacketForMode(frameSize int, actualMode Mode) ([]byte, error) {
	packetBW := e.effectiveBandwidth()
	if actualMode == ModeSILK && packetBW > types.BandwidthWideband {
		packetBW = types.BandwidthWideband
	}
	stereo := e.packetStereoForMode(actualMode)
	mode := modeToTypes(actualMode)

	// CELT and Hybrid have no single-frame TOC config beyond 20 ms, so a 40/60 ms
	// (>20 ms) packet in those modes is assembled as N=frameSize/960 internal 20 ms
	// frames (encodeCELTMultiFramePacket / encodeHybridMultiFramePacket). When DTX
	// fires for such a frame, libopus' per-sub-frame encode returns tmp_len==1 for
	// every sub-frame and the repacketizer collapses them to a TOC-only packet:
	// code 1 for 2 sub-frames (1 byte) or code 3 with a frame-count byte for 3
	// sub-frames (2 bytes). Mirror that with a multi-frame TOC-only packet built
	// from N zero-length sub-frames at the 20 ms sub-frame config. SILK keeps the
	// single-frame path below because it has native 40/60 ms configs (code 0).
	f20 := e.frame20ms()
	if (mode == types.ModeCELT || mode == types.ModeHybrid) && frameSize > f20 && frameSize%f20 == 0 {
		frameCount := frameSize / f20
		e.resetPacketFrameScratch()
		frames := e.scratchFrameSlots[:0]
		for range frameCount {
			frames = append(frames, e.keepFrame(nil))
		}
		n, err := buildMultiFramePacketInto(e.scratchPacket, frames, mode, packetBW, 960, stereo, false)
		if err != nil {
			return nil, err
		}
		return e.scratchPacket[:n], nil
	}

	// Build TOC-only packet (no frame data) into scratch buffer.
	n, err := BuildPacketInto(e.scratchPacket, nil, mode, packetBW, e.packetTOCFrameSize(frameSize), stereo)
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
	if e.silk != nil && e.mode != ModeCELT {
		hpFreqSmth1 = e.silk.VariableHPSmth1Q15()
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

	// silk_biquad_res, float path (Direct Form II Transposed). The src32 branch is
	// hoisted out of the inner loop, and stereo runs both channels' independent
	// recurrences in one interleaved pass so the OoO engine overlaps the two
	// latency-bound filter chains. Per-sample arithmetic is byte-identical to the
	// per-channel form (each channel's state depends only on its own history).
	if channels == 1 {
		s0 := e.hpMem[0]
		s1 := e.hpMem[1]
		if src32 != nil {
			for i := range frameSize {
				inval := src32[i]
				vout := s0 + b[0]*inval
				s0 = s1 - vout*a[0] + b[1]*inval
				s1 = -vout*a[1] + b[2]*inval + verySmall
				out[i] = opusRes(vout)
			}
		} else {
			for i := range frameSize {
				inval := float32(in[i])
				vout := s0 + b[0]*inval
				s0 = s1 - vout*a[0] + b[1]*inval
				s1 = -vout*a[1] + b[2]*inval + verySmall
				out[i] = opusRes(vout)
			}
		}
		e.hpMem[0] = s0
		e.hpMem[1] = s1
		return out
	}

	s0L := e.hpMem[0]
	s1L := e.hpMem[1]
	s0R := e.hpMem[2]
	s1R := e.hpMem[3]
	if src32 != nil {
		for i := range frameSize {
			l := src32[2*i]
			voutL := s0L + b[0]*l
			s0L = s1L - voutL*a[0] + b[1]*l
			s1L = -voutL*a[1] + b[2]*l + verySmall
			out[2*i] = opusRes(voutL)
			r := src32[2*i+1]
			voutR := s0R + b[0]*r
			s0R = s1R - voutR*a[0] + b[1]*r
			s1R = -voutR*a[1] + b[2]*r + verySmall
			out[2*i+1] = opusRes(voutR)
		}
	} else {
		for i := range frameSize {
			l := float32(in[2*i])
			voutL := s0L + b[0]*l
			s0L = s1L - voutL*a[0] + b[1]*l
			s1L = -voutL*a[1] + b[2]*l + verySmall
			out[2*i] = opusRes(voutL)
			r := float32(in[2*i+1])
			voutR := s0R + b[0]*r
			s0R = s1R - voutR*a[0] + b[1]*r
			s1R = -voutR*a[1] + b[2]*r + verySmall
			out[2*i+1] = opusRes(voutR)
		}
	}
	e.hpMem[0] = s0L
	e.hpMem[1] = s1L
	e.hpMem[2] = s0R
	e.hpMem[3] = s1R
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
			for i := range frameSize {
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
			for i := range frameSize {
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
			for i := range n {
				x := src32[i]
				y := x - m0
				m0 = coef*x + verySmall + coef2*m0
				out[i] = y
			}
		} else {
			for i := range n {
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
	for i := range samples {
		sum := float32ToInt16Libopus(interleaved[2*i] + interleaved[2*i+1])
		dst[i] = float32(silkRShiftRound1(sum)) * invScale
	}
}

func averageSilkResamplerOutputsLibopus(dst, right []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := range samples {
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
	channels := max(int(e.channels), 1)
	frameSamples := min(len(pcm), frameSize*channels)
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

func (e *Encoder) maybePrefillCELTOnModeTransition(actualMode Mode) {
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
	prefillInput := e.celtTransitionPrefillSource(prefillFrameSize * channels)
	e.hasCELTPrefill = false
	if prefillInput == nil {
		return
	}

	e.ensureCELTEncoder()
	// OPUS_RESET_STATE clears the SILK info the Opus layer handed CELT for this
	// frame, so the prefill and the transition frame run without it
	// (src/opus_encoder.c:2477-2486).
	e.celtEncoder.Reset()
	e.celtEncoder.SetHybrid(actualMode == ModeHybrid)
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(actualMode))
	e.celtEncoder.SetTopLevelDelayCompensatedInput(true)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	// The transition prefill re-reads the current top-level analysis snapshot.
	e.syncCELTAnalysisToCELT()
	// Match libopus mode-transition cadence: prefill uses normal prediction,
	// then the next real frame is forced intra. The prefill encodes with the
	// frame's CELT rate configuration (configureCELTRate), so its VBR block
	// advances the VBR state from reset exactly like libopus.
	e.celtEncoder.SetPrediction(e.celtPredictionMode())
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))

	e.celtEncoder.SetMaxPayloadBytes(2)
	e.celtEncoder.EncodeFrame(prefillInput, prefillFrameSize)
	e.celtEncoder.SetMaxPayloadBytes(0)
	// Match libopus mode-switch behavior: the next real CELT frame is forced intra.
	e.celtForceIntra = true
}

// celtTransitionPrefillSource returns libopus tmp_prefill: the Fs/400 samples of
// delay history that precede this frame's CELT input,
// delay_buffer[encoder_buffer-total_buffer-Fs/400 : +Fs/400], copied before the
// delay buffer shifts (src/opus_encoder.c:2297-2301). When this frame ran a SILK
// transition prefill, that prefill's onset ramp has rewritten the same window.
func (e *Encoder) celtTransitionPrefillSource(prefillSamples int) []opusRes {
	src := e.scratchTransitionPrefill
	if e.hasCELTPrefill {
		src = e.scratchCELTPrefill
	}
	if prefillSamples <= 0 || len(src) < prefillSamples {
		return nil
	}
	return src[:prefillSamples]
}

// maybePrefillSILKOnModeTransition stages the SILK prefill of a switch from
// CELT to SILK or Hybrid (src/opus_encoder.c:1576-1581 and 2191-2209). On the
// first frame of the packet (initSILK) the SILK encoder is re-initialized
// (silk_InitEncoder). The 10 ms of delay history, with the onset ramp that
// avoids coding a discontinuity, is kept for the silk_Encode prefill call that
// runs once the frame's SILK controls are set; captureCELTPrefill also records
// the part of it the CELT transition prefill reads.
func (e *Encoder) maybePrefillSILKOnModeTransition(actualMode Mode, initSILK, captureCELTPrefill bool) {
	if !e.shouldPrefillSILKOnModeTransition(actualMode) {
		return
	}
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// libopus prefill uses encoder_buffer = Fs/100 samples of delay history.
	prefillFrameSize := sampleRate / 100
	prefillSamples := prefillFrameSize * channels
	prefill := e.ensureSilkPrefill(prefillSamples)
	clear(prefill)
	if len(e.delayBuffer) >= prefillSamples {
		copy(prefill, e.delayBuffer[:prefillSamples])
	} else if len(e.delayBuffer) > 0 {
		copy(prefill[prefillSamples-len(e.delayBuffer):], e.delayBuffer)
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
	if initSILK {
		e.silk.Init()
	}
	e.silkPrefillPending = true
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

// runPendingSILKPrefill runs the staged CELT->SILK/Hybrid prefill through
// silk_Encode with the frame's controls (prefillFlag 1, no range coder).
func (e *Encoder) runPendingSILKPrefill(activity int) error {
	if !e.silkPrefillPending {
		return nil
	}
	e.silkPrefillPending = false
	_, err := e.silk.Encode(&e.silkMode, e.scratchSilkPrefill, int(e.sampleRate)/100, nil, 1, activity)
	return err
}

func (e *Encoder) applySilkTransitionPrefillRamp(prefill []opusRes, prefillFrameSize int) {
	if len(prefill) == 0 || prefillFrameSize <= 0 {
		return
	}
	channels := max(int(e.channels), 1)
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	prefillLen := sampleRate / 400
	start := min(max(prefillFrameSize-delayComp-prefillLen, 0), prefillFrameSize)

	prefix := min(start*channels, len(prefill))
	for i := range prefix {
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

	inc := max(48000/sampleRate, 1)
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
	channels := max(int(e.channels), 1)
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	frameSamples := min(len(pcm), frameSize*channels)
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
	channels := max(int(e.channels), 1)
	frameSamples := min(len(framePCM), frameSize*channels)
	if e.lowDelay {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, framePCM[:frameSamples])
		return out
	}
	return e.applyDelayCompensation(framePCM, frameSize)
}

// applyStereoWidthReduction runs the stereo width block of
// opus_encode_frame_native (src/opus_encoder.c:2320-2348) for a frame of a
// stereo stream. SILK-only and CELT-only frames, and hybrid frames coded as
// one stream channel, take silk_mode.stereoWidth_Q14 from equiv_rate; stereo
// hybrid frames keep the smoothed width silk_Encode reported. Without an energy
// mask, when the previously applied width or the new one is below full width,
// it runs stereo_fade() on pcm (the delay-compensated CELT input, modified in
// place) and records the width in hybrid_stereo_width_Q14. A SILK-only frame has
// no CELT input to fade, so it passes nil pcm and only advances the width state.
// frameSize drives the equiv_rate frame rate; libopus passes the whole packet's
// equiv_rate to every 20 ms sub-frame, and compute_equiv_rate only adjusts frame
// rates above 50 Hz, so a sub-frame size gives the same rate.
func (e *Encoder) applyStereoWidthReduction(mode Mode, pcm []opusRes, frameSize int) []opusRes {
	if e.channels != 2 {
		return pcm
	}
	if mode != ModeHybrid || e.streamChannels == 1 {
		frameRate := int32(int(e.sampleRate) / frameSize)
		equivRate := e.computeEquivRate(e.bitrate, int32(e.streamChannels), frameRate, e.bitrateMode != ModeCBR, mode, int32(e.complexity), int32(e.packetLoss))
		switch {
		case equivRate > 32000:
			e.silkMode.StereoWidthQ14 = 16384
		case equivRate < 16000:
			e.silkMode.StereoWidthQ14 = 0
		default:
			e.silkMode.StereoWidthQ14 = 16384 - 2048*(32000-equivRate)/(equivRate-14000)
		}
	}
	if len(e.celtEnergyMask) > 0 {
		return pcm
	}

	if e.hybridState == nil {
		e.hybridState = &HybridState{
			prevHBGain:     1.0,
			stereoWidthQ14: 16384,
		}
	}
	widthQ14 := int16(e.silkMode.StereoWidthQ14)
	if e.hybridState.stereoWidthQ14 < (1<<14) || widthQ14 < (1<<14) {
		if pcm != nil {
			pcm = e.applyStereoWidthFade(pcm, e.hybridState.stereoWidthQ14, widthQ14)
		}
		e.hybridState.stereoWidthQ14 = widthQ14
	}
	return pcm
}

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	if e.restrictedSilkApp {
		return ModeSILK
	}
	if e.lowDelay {
		return ModeCELT
	}
	if frameSize > e.frame20ms() {
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
	switch prev {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
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
	switch prev {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
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
		f20 := e.frame20ms()
		runAnalyzer := frameSize > f20
		if !runAnalyzer && e.mode == ModeAuto && frameSize == f20 && e.effectiveBandwidth() == types.BandwidthSuperwideband {
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
	if frameSize > e.frame20ms() && e.lastAnalysisValid {
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
	switch prev {
	case ModeCELT:
		prob = e.lastAnalysisInfo.MusicProbMax
	case ModeSILK, ModeHybrid:
		prob = e.lastAnalysisInfo.MusicProbMin
	}
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}
	voiceRatio := int(opusmath.FloorHalfPlusF32ToInt32(float32(100) * (float32(1) - prob)))
	voiceEst = min(
		// OPUS_APPLICATION_AUDIO clamp.
		(voiceRatio*327)>>8, 115)
	return voiceEst
}

// effectiveBandwidth returns the resolved bandwidth for encoder submodules.
func (e *Encoder) effectiveBandwidth() types.Bandwidth {
	if e.lfe {
		return types.BandwidthNarrowband
	}
	return e.bandwidth
}

// packetTOCFrameSize maps the native-Fs per-frame size to the 48 kHz-equivalent
// count used to index the TOC config table. libopus gen_toc derives the TOC
// period from Fs/frame_size, so the duration (and therefore the period) is
// identical to the 48 kHz frame size frameSize*48000/Fs. At 48 kHz it is the
// identity; at sub-48 kHz native rates it scales the native frame size up to its
// 48 kHz-equivalent duration so the on-wire TOC config is unchanged.
func (e *Encoder) packetTOCFrameSize(frameSize int) int {
	fs := int(e.sampleRate)
	if fs <= 0 || fs == 48000 {
		return frameSize
	}
	return frameSize * 48000 / fs
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

// encodeSILKFrameWithDRED runs the SILK leg of opus_encode_frame_native for one
// SILK-only frame (src/opus_encoder.c:2043-2212): it sets silk_mode for the
// frame, runs any pending transition prefill and codes pcm with silk_Encode into
// the frame's own range coder. maxDataBytes is the frame's orig_max_data_bytes
// (the CBR byte count, the caller budget, or a multi-frame sub-frame's
// curr_max), which bounds both the SILK rate and its maxBits; e.bitrate is
// st->bitrate_bps after any DRED reservation, and dredBitrate is that
// reservation. It returns the frame payload after ec_enc_done: the
// (ec_tell+7)>>3 coded bytes without trailing zero bytes, or a single zero byte
// that makes the decoder run the PLC when SILK busted the frame budget
// (src/opus_encoder.c:2578-2597).
func (e *Encoder) encodeSILKFrameWithDRED(pcm []opusRes, frameSize, maxDataBytes, dredBitrate int) ([]byte, error) {
	e.ensureSILKEncoder()
	silkRate := e.silkTotalBitrate(frameSize, maxDataBytes, 0)
	useCBR := e.bitrateMode == ModeCBR && dredBitrate == 0
	e.configureSILKMode(frameSize, silkRate, e.silkOnlyMaxBits(frameSize, maxDataBytes, silkRate, dredBitrate), useCBR)
	activity := e.silkActivity()
	if err := e.runPendingSILKPrefill(activity); err != nil {
		return nil, err
	}

	payload := e.ensureSILKPayload(max(maxDataBytes-1, 1))
	re := &e.silkRangeEncoder
	re.Init(payload)
	if _, err := e.silk.Encode(&e.silkMode, pcm, frameSize, re, 0, activity); err != nil {
		return nil, err
	}
	e.silkFinalRange = re.Range()
	tell := re.Tell()
	re.Done()
	if tell > (min(maxDataBytes, libopusMaxDataBytesCap)-1)*8 {
		e.silkFinalRange = 0
		payload[0] = 0
		return payload[:1], nil
	}
	return trimSilkTrailingZeros(payload[:(tell+7)>>3]), nil
}

// configureSILKMode sets the silk_mode controls opus_encode_frame_native hands
// silk_Encode for a frame (src/opus_encoder.c:2050-2189): the SILK rate, the
// packet duration and channel layout, the bit cap and CBR flag, and the
// FEC/complexity/loss/dependency settings.
func (e *Encoder) configureSILKMode(frameSize, bitRate, maxBits int, useCBR bool) {
	m := &e.silkMode
	m.NChannelsAPI = e.channels
	m.NChannelsInternal = int32(e.silkInternalChannels())
	m.APISampleRate = e.sampleRate
	m.PayloadSizeMs = int32(1000 * frameSize / int(e.sampleRate))
	m.BitRate = int32(bitRate)
	m.PacketLossPercentage = e.packetLoss
	m.Complexity = e.complexity
	m.LBRRCoded = e.lbrrCoded
	m.UseCBR = useCBR
	m.MaxBits = int32(maxBits)
	m.ToMono = e.toMono != 0
	m.ReducedDependency = e.predictionDisabled
}

// silkActivity returns the Opus-level voice activity decision handed to
// silk_Encode (src/opus_encoder.c:1888-1930): VAD_NO_DECISION when the analysis
// made no decision, otherwise whether the frame is active.
func (e *Encoder) silkActivity() int {
	switch {
	case !e.lastOpusVADValid:
		return silk.VADNoDecision
	case e.lastOpusVADActive:
		return 1
	default:
		return silk.VADNoActivity
	}
}

func (e *Encoder) ensureSILKPayload(n int) []byte {
	if cap(e.silkPayload) < n {
		e.silkPayload = make([]byte, n)
	}
	return e.silkPayload[:n]
}

// silkOnlyMaxBits mirrors silk_mode.maxBits for a SILK-only frame
// (src/opus_encoder.c:2154-2177): the payload bits after the TOC byte of
// max_data_bytes (capped at 1276). A CBR stream carrying DRED codes SILK as VBR
// capped so SILK takes at most a quarter of the bits beyond its own rate, and
// DRED absorbs the rest.
func (e *Encoder) silkOnlyMaxBits(frameSize, maxDataBytes, silkBitrate, dredBitrate int) int {
	maxBits := (min(maxDataBytes, libopusMaxDataBytesCap) - 1) * 8
	if e.bitrateMode == ModeCBR && dredBitrate > 0 {
		otherBits := max(0, maxBits-silkBitrate*frameSize/int(e.sampleRate))
		maxBits = max(0, maxBits-otherBits*3/4)
	}
	return maxBits
}

// celtNbComprBytes returns the CELT-only payload budget opus_encode_frame_native
// hands celt_encode_with_ec: nb_compr_bytes = min(max_data_bytes, 1276)-1
// (src/opus_encoder.c:1893 and :2392; CELT-only frames carry no redundancy).
// With QEXT enabled the budget is max_data_bytes-1 without the 1276-byte cap
// (src/opus_encoder.c:2393-2397).
func (e *Encoder) celtNbComprBytes(maxDataBytes int) int {
	if extsupport.QEXT && e.qextActive() {
		return maxDataBytes - 1
	}
	return min(maxDataBytes, libopusMaxDataBytesCap) - 1
}

// celtDREDPayloadCap caps the CELT payload budget so an attached DRED payload
// keeps a quarter of its bytes (src/opus_encoder.c:2399-2411): CELT may take at
// most nb_compr_bytes-dred_bytes*3/4, but keeps at least 5 bytes past the
// already-coded (empty) range-coder prefix.
func (e *Encoder) celtDREDPayloadCap(nbComprBytes, dredBitrate, frameSize int) int {
	if nbComprBytes <= 0 || dredBitrate <= 0 || frameSize <= 0 {
		return nbComprBytes
	}
	dredBytes := bitrateToBitsFs(dredBitrate, int(e.sampleRate), frameSize) / 8
	const emptyCoderBytes = 1 // (ec_tell(&enc)+7)/8 with nothing coded yet
	maxCELTBytes := max(nbComprBytes-dredBytes*3/4, emptyCoderBytes+5)
	return min(nbComprBytes, maxCELTBytes)
}

// configureCELTRate mirrors the per-frame CELT rate setup of
// opus_encode_frame_native (src/opus_encoder.c:2286 and :2447-2476). Every frame
// first resets CELT to OPUS_BITRATE_MAX, so CBR frames fill the nb_compr_bytes
// budget; VBR frames then hand CELT the frame bitrate, constrained per
// OPUS_SET_VBR_CONSTRAINT for CELT-only frames and always unconstrained for
// hybrid frames. A CBR stream carrying DRED codes its CELT part as
// unconstrained VBR so the DRED payload absorbs the slack. celtBitrate is the
// bitrate the CELT part of the frame targets.
func (e *Encoder) configureCELTRate(mode Mode, celtBitrate int) {
	e.celtEncoder.SetBitrate(celt.BitrateMax)
	switch {
	case e.bitrateMode != ModeCBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(mode != ModeHybrid && e.bitrateMode == ModeCVBR)
		e.setCELTBitrate(celtBitrate)
	case e.dredEncodingActive():
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false)
		e.setCELTBitrate(celtBitrate)
	default:
		e.celtEncoder.SetVBR(false)
	}
}

// setCELTBitrate applies OPUS_SET_BITRATE to the CELT encoder with the checks
// of celt_encoder_ctl (celt/celt_encoder.c:3001-3009): a rate of 500 b/s or less
// is rejected and leaves the current rate in place, and the rate is capped at
// 750 kb/s per channel.
func (e *Encoder) setCELTBitrate(bitrate int) {
	if bitrate <= 500 && bitrate != celt.BitrateMax {
		return
	}
	e.celtEncoder.SetBitrate(min(bitrate, 750000*int(e.channels)))
}

// encodeCELTFrameWithBitrateMaxPayloadAndDRED runs the CELT sub-encoder for one
// CELT-only frame and is the CELT leg of the SILK/CELT/Hybrid bridge (libopus
// celt_encode_with_ec). bitrate is the frame's st->bitrate_bps (after any DRED
// reservation), nbComprBytes the CELT payload budget, and dredBitrate the DRED
// bitrate whose payload further caps that budget. It configures the native-Fs
// upsample factor before encoding and returns the raw CELT frame bytes.
func (e *Encoder) encodeCELTFrameWithBitrateMaxPayloadAndDRED(pcm []opusRes, frameSize int, bitrate int, nbComprBytes int, dredBitrate int) ([]byte, error) {
	e.ensureCELTEncoder()
	// CELT-only consumes native-Fs frame sizes; the float CELT encoder upsamples
	// to the 48 kHz core (libopus celt_encode_with_ec frame_size *= st->upsample).
	e.celtEncoder.SetUpsample(e.celtUpsampleFactor())
	e.syncQEXTToCELT()
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(ModeCELT))
	e.configureCELTRate(ModeCELT, bitrate)
	nbComprBytes = e.celtDREDPayloadCap(nbComprBytes, dredBitrate, frameSize)
	e.celtEncoder.SetMaxPayloadBytes(nbComprBytes)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetTopLevelDelayCompensatedInput(true)
	e.celtEncoder.SetPrediction(e.celtPredictionModeForFrame())
	e.celtEncoder.SetDCRejectEnabled(false)
	e.celtEncoder.SetPacketLoss(int(e.packetLoss))
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))
	defer e.celtEncoder.SetMaxPayloadBytes(0)
	if out, ok, err := e.encodeCELTFrameFixed(pcm, frameSize, e.celtEncoder.Bitrate(), nbComprBytes); ok || err != nil {
		return out, err
	}
	return e.celtEncoder.EncodeFrame(pcm, frameSize)
}

// maxLongPacketFrameBytes bounds the combined size of all subframe payloads
// kept by keepFrame within a single long packet. Each of the <=6 internal
// subframes is independently capped by at most a full Opus packet, so this
// covers any realisable sum while letting resetPacketFrameScratch pre-grow the
// backing buffer once, keeping earlier keepFrame subslices stable.
const maxLongPacketFrameBytes = 6 * maxSilkPacketBytes

// resetPacketFrameScratch prepares the reusable long-packet assembly scratch at
// the top of each long-packet encode. It pre-grows scratchFrameBytes so that the
// per-frame keepFrame appends never reallocate (which would invalidate earlier
// returned subslices).
func (e *Encoder) resetPacketFrameScratch() {
	if cap(e.scratchFrameBytes) < maxLongPacketFrameBytes {
		e.scratchFrameBytes = make([]byte, 0, maxLongPacketFrameBytes)
	}
	e.scratchFrameBytes = e.scratchFrameBytes[:0]
	if cap(e.scratchQEXTPayloadBytes) < maxLongPacketFrameBytes {
		e.scratchQEXTPayloadBytes = make([]byte, 0, maxLongPacketFrameBytes)
	}
	e.scratchQEXTPayloadBytes = e.scratchQEXTPayloadBytes[:0]
}

// ensurePacketScratch grows the assembled-packet buffer so a multi-frame packet
// up to n bytes fits. The default buffer holds a single 1275-byte Opus packet,
// but a long CELT/SILK/Hybrid packet at a high bitrate (e.g. 120 ms at 128 kb/s,
// ~1920 bytes) needs the caller's larger out_data_bytes budget, exactly as
// libopus assembles into the caller's buffer.
func (e *Encoder) ensurePacketScratch(n int) {
	if cap(e.scratchPacket) >= n {
		e.scratchPacket = e.scratchPacket[:cap(e.scratchPacket)]
		return
	}
	e.scratchPacket = make([]byte, n)
}

// keepFrame copies frame into the reusable scratchFrameBytes backing buffer and
// returns a length-capped subslice. The range coder output buffer is reused
// across subframes, so the copy gives each kept frame stable storage. The
// returned subslice is 3-index sliced so a stray append cannot reach into the
// next frame's bytes.
func (e *Encoder) keepFrame(frame []byte) []byte {
	start := len(e.scratchFrameBytes)
	e.scratchFrameBytes = append(e.scratchFrameBytes, frame...)
	end := len(e.scratchFrameBytes)
	return e.scratchFrameBytes[start:end:end]
}

// keepQEXTPayload copies a QEXT extension payload into the reusable
// scratchQEXTPayloadBytes backing buffer, giving it stable storage for the
// duration of packet assembly without a per-frame heap allocation.
func (e *Encoder) keepQEXTPayload(payload []byte) []byte {
	start := len(e.scratchQEXTPayloadBytes)
	e.scratchQEXTPayloadBytes = append(e.scratchQEXTPayloadBytes, payload...)
	end := len(e.scratchQEXTPayloadBytes)
	return e.scratchQEXTPayloadBytes[start:end:end]
}

// encodeCELTMultiFramePacket encodes long CELT packets by splitting into
// 20ms CELT frames and packing them with Opus multi-frame framing.
func (e *Encoder) encodeCELTMultiFramePacket(framePCM []opusRes, vadPCM []opusRes, celtPCM []opusRes, frameSize, originalBitrate, encodingBitrate, dredBitrate, dredExtraDelay, outDataBytes int) ([]byte, error) {
	f20 := e.frame20ms()
	if frameSize <= f20 || frameSize%f20 != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / f20
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

	e.resetPacketFrameScratch()
	frameStride := f20 * channels
	frames := e.scratchFrameSlots[:frameCount]
	sameSize := true
	prevSize := -1
	// libopus opus_encoder.c: VBR (and bitrate==MAX) sizes the repacketizer by the
	// full output buffer; CBR caps it to IMIN(cbr_bytes, out_data_bytes). Using the
	// bitrate-derived size for VBR would shrink each sub-frame's curr_max ceiling by
	// ~1 byte and desync the per-frame CELT VBR target. (bitrate==MAX resolves to a
	// rate whose cbr_bytes exceeds out_data_bytes, so the IMIN below still yields the
	// full buffer for that case.)
	repacketizeLen := outDataBytes
	if e.bitrateMode == ModeCBR {
		cbrBytes := min(e.targetBytesForBitrate(originalBitrate, frameSize), outDataBytes)
		repacketizeLen = cbrBytes
	}
	if repacketizeLen < 1 {
		repacketizeLen = 1
	}
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	if extsupport.QEXT && e.qextActive() {
		maxHeaderBytes += frameCount
	}
	maxLenSum := max(frameCount+repacketizeLen-maxHeaderBytes, frameCount)
	// The assembled packet (header + sum of sub-frame payloads) is bounded by
	// maxLenSum+maxHeaderBytes; grow the output buffer so a high-bitrate long
	// packet that exceeds the default single-packet size still fits.
	e.ensurePacketScratch(maxLenSum + maxHeaderBytes)
	subframeBitrate := int(e.bitrate)
	if encodingBitrate > 0 {
		subframeBitrate = encodingBitrate
	}
	currMaxByRate := max(subframeBitrate*f20/int(e.sampleRate)/8, 2)
	dredBytes := 0
	if dredBitrate > 0 {
		dredBytes = e.bitrateToBits(dredBitrate, frameSize) / 8
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
	for i := range frameCount {
		e.primeSubframeAnalysis(f20)
		start := i * frameStride
		end := start + frameStride
		subFramePCM := framePCM[start:end]
		subVADPCM := vadPCM[start:end]
		if dredActive {
			e.updateOpusVADRes(subVADPCM, f20)
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
			dredCap := max((maxLenSum-dredBytes)/frameCount, 2)
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
		// libopus recurses opus_encode_native per 20 ms sub-frame, so gain_fade
		// and stereo_fade run (and their state evolves) once per sub-frame on
		// that sub-frame's CELT input. Apply them here on the sub-frame slice,
		// mirroring the single-frame path.
		subCeltPCM := e.applyUnityHBGainFade(celtPCM[start:end])
		subCeltPCM = e.applyStereoWidthReduction(ModeCELT, subCeltPCM, f20)
		frameData, err := e.encodeCELTFrameWithBitrateMaxPayloadAndDRED(subCeltPCM, f20, int(e.bitrate), e.celtNbComprBytes(currMax), dredBitrate)
		if err != nil {
			e.bitrate = savedBitrate
			return nil, err
		}
		// Keep a stable copy because the range coder output buffer is reused.
		frameCopy := e.keepFrame(frameData)
		// Per-sub-frame DTX decision (libopus opus_encode_frame_native
		// decide_dtx_mode, called once per 20ms sub-frame). When it fires the
		// just-encoded payload is discarded and the sub-frame becomes a length-0
		// frame in the repacketized packet — exactly as libopus turns tmp_len==1
		// into a zero-length repacketizer entry. The encode call above already
		// advanced the encoder state, so suppression only drops the bytes.
		suppressed := !dredActive && e.subframeDTXSuppress(ModeCELT, subVADPCM, f20, false)
		e.multiFrameLastSubframeDTX = suppressed
		if suppressed {
			frameCopy = frameCopy[:0]
			e.multiFrameDTXCount++
			totSize++ // libopus tot_size += tmp_len (==1) for a DTX sub-frame
		} else {
			totSize += len(frameData) + 1
		}
		frames[i] = frameCopy
		if !suppressed && extsupport.QEXT && e.celtEncoder != nil {
			qextPayload := e.lastQEXTPayload()
			if len(qextPayload) > 0 {
				qextExtensions[qextExtensionCount] = packetExtension{
					ID:    qextExtensionID,
					Data:  e.keepQEXTPayload(qextPayload),
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
	packetLen, err := buildMultiFramePacketInto(
		e.scratchPacket,
		frames,
		types.ModeCELT,
		e.effectiveBandwidth(),
		960,
		e.packetStereoForMode(ModeCELT),
		!sameSize,
	)
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:packetLen], nil
}

// encodeHybridMultiFramePacket encodes long hybrid packets by splitting into
// 20ms hybrid frames and packing them with Opus multi-frame framing.
func (e *Encoder) encodeHybridMultiFramePacket(pcm []opusRes, celtPCM []opusRes, vadPCM []opusRes, delayState []opusRes, frameSize int, transitionToCELT bool, originalBitrate, encodingBitrate, dredBitrate, dredExtraDelay, outDataBytes int) ([]byte, error) {
	f20 := e.frame20ms()
	if frameSize <= f20 || frameSize%f20 != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / f20
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

	e.resetPacketFrameScratch()
	frameStride := f20 * channels
	frames := e.scratchFrameSlots[:frameCount]
	sameSize := true
	prevSize := -1
	// libopus opus_encode_native sizes the repacketizer by the full output
	// buffer in VBR and by IMIN(cbr_bytes, out_data_bytes) in CBR; each 20 ms
	// sub-frame is then capped at its bitrate share of that sum.
	repacketizeLen := outDataBytes
	if e.bitrateMode == ModeCBR {
		repacketizeLen = min(e.targetBytesForBitrate(originalBitrate, frameSize), outDataBytes)
	}
	repacketizeLen = max(repacketizeLen, 1)
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	maxLenSum := max(frameCount+repacketizeLen-maxHeaderBytes, frameCount)
	// A long high-bitrate packet exceeds the default single-packet buffer; the
	// assembled packet is bounded by maxLenSum+maxHeaderBytes.
	e.ensurePacketScratch(maxLenSum + maxHeaderBytes)
	subframeBitrate := int(e.bitrate)
	if encodingBitrate > 0 {
		subframeBitrate = encodingBitrate
	}
	currMaxByRate := max(subframeBitrate*f20/int(e.sampleRate)/8, 2)
	dredBytes := 0
	if dredBitrate > 0 {
		dredBytes = e.bitrateToBits(dredBitrate, frameSize) / 8
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
	// Every sub-frame is coded with silk_mode.toMono = 0; the packet's value
	// comes back afterwards (src/opus_encoder.c:1763-1836).
	bakToMono := e.toMono
	e.toMono = 0
	defer func() { e.toMono = bakToMono }()
	for i := range frameCount {
		e.primeSubframeAnalysis(f20)
		start := i * frameStride
		end := start + frameStride
		subPCM := pcm[start:end]
		subCELTPCM := celtPCM[start:end]
		subVADPCM := vadPCM[start:end]

		// Match libopus long-packet cadence: compute DRED activity from the
		// same per-subframe analysis snapshot used by the primary frame.
		e.updateOpusVADRes(subVADPCM, f20)
		dredNoDecision := !e.lastOpusVADValid
		if dredActive {
			e.processDREDLatentsWithActivity(subPCM, dredExtraDelay, e.lastOpusVADActive)
		}

		if packetPrefillFromCELT {
			// libopus keeps the packet-level CELT->SILK/HYBRID prefill active for
			// each 20 ms internal frame of long packets. The first subframe also
			// snapshots CELT's transition-prefill window; later ones only re-prime
			// the SILK state from the rolling delay history.
			e.maybePrefillSILKOnModeTransition(ModeHybrid, i == 0, i == 0)
		}

		currMax := currMaxByRate
		capPerFrame := maxLenSum / frameCount
		if currMax > capPerFrame {
			currMax = capPerFrame
		}
		if dredBytes > 0 {
			dredCap := max((maxLenSum-dredBytes)/frameCount, 2)
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
		frameData, err := e.encodeHybridFrameWithMaxPacketAndTransition(subPCM, subCELTPCM, f20, currMax, 0, dredBitrate, true, allowTransitionRedundancy, subframeToCELT, runCELTTransitionPrefill)
		if err != nil {
			e.bitrate = savedBitrate
			return nil, err
		}
		if dredActive && dredNoDecision {
			e.backfillDREDActivityForFrame(f20, e.silkMode.SignalType != 0)
		}
		if dredActive && i == 0 {
			e.snapshotDREDPacketState()
		}
		// Keep a stable copy because encoder scratch buffers are reused.
		frameCopy := e.keepFrame(frameData)
		// Per-sub-frame DTX decision (libopus opus_encode_frame_native
		// decide_dtx_mode). The Opus-level activity (lastOpusVAD*) was already
		// computed for this sub-frame by updateOpusVADRes above, so pass
		// vadAlreadyComputed=true to avoid re-tracking peak_signal_energy. A
		// suppressed sub-frame becomes a length-0 frame in the packet; the encode
		// above already advanced the encoder state.
		suppressed := !dredActive && e.subframeDTXSuppress(ModeHybrid, subVADPCM, f20, true)
		e.multiFrameLastSubframeDTX = suppressed
		if suppressed {
			frameCopy = frameCopy[:0]
			e.multiFrameDTXCount++
			totSize++
		} else {
			totSize += len(frameCopy) + 1
		}
		frames[i] = frameCopy
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
	packetLen, err := buildMultiFramePacketInto(e.scratchPacket, frames, types.ModeHybrid, packetBW, 960, e.packetStereoForMode(ModeHybrid), !sameSize)
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:packetLen], nil
}

// encodeSILKMultiFramePacket encodes 80/100/120ms SILK packets by splitting
// them into libopus-compatible 20/40/60ms SILK frames and repacketizing them.
func (e *Encoder) encodeSILKMultiFramePacket(pcm []opusRes, vadPCM []opusRes, frameSize int, originalBitrate, encodingBitrate, dredBitrate, dredExtraDelay, outDataBytes int) ([]byte, error) {
	channels := int(e.channels)
	if len(pcm) != frameSize*channels || len(vadPCM) != frameSize*channels {
		return nil, ErrInvalidFrameSize
	}

	// libopus opus_encode_native (lines 1715-1723) splits long SILK packets by
	// duration: 80 ms -> 2x40 ms, 120 ms -> 2x60 ms, otherwise N x 20 ms. The
	// sub-frame encode size is native-Fs; the TOC config it maps to is the
	// 48 kHz-equivalent (encFrameSize48k) so the on-wire framing is unchanged.
	f20 := e.frame20ms()
	var encFrameSize int
	switch frameSize {
	case 4 * f20: // 80 ms -> 2x40 ms
		encFrameSize = 2 * f20
	case 5 * f20: // 100 ms -> 5x20 ms
		encFrameSize = f20
	case 6 * f20: // 120 ms -> 2x60 ms
		encFrameSize = 3 * f20
	default:
		return nil, ErrInvalidFrameSize
	}
	encFrameSize48k := encFrameSize * 48000 / int(e.sampleRate)

	frameCount := frameSize / encFrameSize
	if frameCount < 1 || frameCount > 6 {
		return nil, ErrInvalidFrameSize
	}
	e.resetPacketFrameScratch()
	frames := e.scratchFrameSlots[:frameCount]
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
	// opus_encode_native sizes the repacketizer by the whole output buffer in
	// VBR and by IMIN(cbr_bytes, out_data_bytes) in CBR (src/opus_encoder.c).
	repacketizeLen := outDataBytes
	if e.bitrateMode == ModeCBR {
		repacketizeLen = min(e.targetBytesForBitrate(originalBitrate, frameSize), outDataBytes)
	}
	repacketizeLen = max(repacketizeLen, 1)
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	maxLenSum := max(frameCount+repacketizeLen-maxHeaderBytes, frameCount)
	e.ensurePacketScratch(maxLenSum + maxHeaderBytes)
	currMaxByRate := max(subframeBitrate*encFrameSize/int(e.sampleRate)/8, 2)
	dredBytes := 0
	if dredBitrate > 0 {
		dredBytes = e.bitrateToBits(dredBitrate, frameSize) / 8
	}
	dredActive := e.dredEncodingActive()
	if dredActive {
		e.clearDREDPacketSnapshot()
	}
	totSize := 0
	firstFrameMaxBytes := 0
	savedBitrate := e.bitrate
	e.bitrate = int32(subframeBitrate)
	// Every sub-frame is coded with silk_mode.toMono = 0; the packet's value
	// comes back afterwards (src/opus_encoder.c:1763-1836).
	bakToMono := e.toMono
	e.toMono = 0
	defer func() { e.toMono = bakToMono }()
	// After CELT, opus_encode_native keeps prefill set for every sub-frame, so
	// each opus_encode_frame_native reruns the SILK prefill from the delay
	// history the previous sub-frame left (src/opus_encoder.c:2191-2209). The
	// caller staged the first one; the sub-frames advance a copy of the history.
	packetPrefillFromCELT := e.shouldPrefillSILKOnModeTransition(ModeSILK)
	if packetPrefillFromCELT && len(e.delayBuffer) > 0 {
		savedDelayBuffer := e.delayBuffer
		delayState := e.ensureDelayState(len(savedDelayBuffer))
		copy(delayState, savedDelayBuffer)
		e.delayBuffer = delayState
		defer func() { e.delayBuffer = savedDelayBuffer }()
	}

	for i := range frameCount {
		e.primeSubframeAnalysis(encFrameSize)
		start := i * frameStride
		end := start + frameStride
		subPCM := pcm[start:end]
		subVADPCM := vadPCM[start:end]
		if packetPrefillFromCELT && i > 0 {
			e.maybePrefillSILKOnModeTransition(ModeSILK, false, false)
		}

		e.updateOpusVADRes(subVADPCM, encFrameSize)
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
			dredCap := max((maxLenSum-dredBytes)/frameCount, 2)
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
		frameData, err := e.encodeSILKFrameWithDRED(subPCM, encFrameSize, currMax, dredBitrate)
		if err != nil {
			e.bitrate = savedBitrate
			return nil, err
		}
		if dredActive && dredNoDecision {
			e.backfillDREDActivityForFrame(encFrameSize, e.silkMode.SignalType != 0)
		}
		if dredActive && i == 0 {
			e.snapshotDREDPacketState()
		}
		frameCopy := e.keepFrame(frameData)
		// Per-sub-frame DTX decision (libopus opus_encode_frame_native
		// decide_dtx_mode). For the default AUDIO application SILK-internal DTX is
		// off (silk_mode.useDTX = use_dtx && !(analysis_info.valid || is_silence)
		// == 0 when analysis is valid, opus_encoder.c:1461), so the Opus-level
		// decision drives suppression here. The Opus-level activity (lastOpusVAD*)
		// was already computed for this sub-frame by updateOpusVADRes above.
		suppressed := !dredActive && e.subframeDTXSuppress(ModeSILK, subVADPCM, encFrameSize, true)
		e.multiFrameLastSubframeDTX = suppressed
		if suppressed {
			frameCopy = frameCopy[:0]
			e.multiFrameDTXCount++
			totSize++
		} else {
			totSize += len(frameCopy) + 1
		}
		frames[i] = frameCopy
		if packetPrefillFromCELT && len(e.delayBuffer) > 0 {
			e.updateDelayBufferInternal(subPCM, len(subPCM), len(e.delayBuffer))
		}
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}
	e.bitrate = savedBitrate
	e.analysisReadBakSet = false

	packetBW := min(e.effectiveBandwidth(), types.BandwidthWideband)
	if e.dredEncodingActive() {
		if dredPacket, ok, err := e.maybeBuildMultiFrameDREDPacket(frames, ModeSILK, packetBW, frameSize, encFrameSize48k, firstFrameMaxBytes, e.packetStereoForMode(ModeSILK), !sameSize, nil); err != nil {
			return nil, err
		} else if ok {
			return dredPacket, nil
		}
	}
	packetLen, err := buildMultiFramePacketInto(e.scratchPacket, frames, types.ModeSILK, packetBW, encFrameSize48k, e.packetStereoForMode(ModeSILK), !sameSize)
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:packetLen], nil
}

// ensureSILKEncoder creates the SILK encoder on first use and selects the SILK
// bandwidth of the current Opus bandwidth.
func (e *Encoder) ensureSILKEncoder() {
	bw := e.silkBandwidth()
	if e.silk == nil {
		e.silk = silk.NewPacketEncoder(int(e.sampleRate), int(e.channels), bw)
		return
	}
	e.silk.SetBandwidth(bw)
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

// updateCELTOnlyOpusVADRes computes the Opus-level activity for CELT-only frames,
// matching libopus opus_encoder.c:1888-1930. When the tonality analysis is valid
// it uses the analysis_info.activity_probability path (with the pseudo-SNR safety
// net); otherwise it uses the CELT-only noise-energy branch (line 1927):
//
//	activity = st->peak_signal_energy < (PSEUDO_SNR_THRESHOLD * HALF32(noise_energy))
//
// Peak signal energy tracking mirrors line 1312-1318.
func (e *Encoder) updateCELTOnlyOpusVADRes(pcm []opusRes, frameSize int) {
	if frameSize <= 0 || len(pcm) == 0 {
		e.clearOpusVADDecision()
		return
	}
	isSilence := isDigitalSilenceRes(pcm, e.lsbDepth)
	if isSilence {
		e.lastOpusVADProb = 0
		e.lastOpusVADValid = true
		e.lastOpusVADActive = false
		return
	}

	analysisValid := false
	analysisProb := float32(1.0)
	if e.lastAnalysisFresh {
		e.lastAnalysisFresh = false
		analysisValid = e.lastAnalysisValid
		analysisProb = e.lastAnalysisInfo.VADProb
	} else if e.lastAnalysisValid {
		analysisValid = true
		analysisProb = e.lastAnalysisInfo.VADProb
	}

	// Peak signal energy tracking (opus_encoder.c:1312-1318): update when analysis
	// is invalid or clearly active (> threshold), skipping digital silence.
	if e.dtx != nil && (!analysisValid || analysisProb > DTXActivityThreshold) {
		frameEnergy := computeFrameEnergyRes(pcm)
		e.dtx.peakSignalEnergy = maxf(0.999*e.dtx.peakSignalEnergy, frameEnergy)
	}

	e.lastOpusVADProb = analysisProb
	e.lastOpusVADValid = true
	if analysisValid {
		active := analysisProb >= DTXActivityThreshold
		if !active {
			frameEnergy := computeFrameEnergyRes(pcm)
			peak := opusVal32(0)
			if e.dtx != nil {
				peak = e.dtx.peakSignalEnergy
			}
			active = peak < pseudoSNRThreshold*frameEnergy
		}
		e.lastOpusVADActive = active
		return
	}

	// CELT-only noise-energy branch (opus_encoder.c:1927-1929):
	// activity = peak_signal_energy < (PSEUDO_SNR_THRESHOLD * HALF32(noise_energy)).
	frameEnergy := computeFrameEnergyRes(pcm)
	peak := opusVal32(0)
	if e.dtx != nil {
		peak = e.dtx.peakSignalEnergy
	}
	e.lastOpusVADActive = peak < pseudoSNRThreshold*(frameEnergy*0.5)
}

// resolveDTXActivity resolves the libopus opus_int activity for the just-encoded
// frame (opus_encoder.c:2235). When the Opus-level VAD made no decision
// (VAD_NO_DECISION, lastOpusVADValid==false) libopus resolves activity from the
// SILK signal type: activity = (signalType != TYPE_NO_VOICE_ACTIVITY).
func (e *Encoder) resolveDTXActivity() bool {
	if e.lastOpusVADValid {
		return e.lastOpusVADActive
	}
	if e.silk != nil {
		return e.silkMode.SignalType != 0
	}
	// VAD_NO_DECISION with no SILK result resolves to active (true), matching the
	// libopus default where activity stays VAD_NO_DECISION (-1, truthy) and
	// decide_dtx_mode treats !activity as false.
	return true
}

// ensureCELTEncoder creates the CELT encoder if it doesn't exist.
// celtUpsampleFactor mirrors libopus resampling_factor(Fs): the CELT input
// upsample factor for the native API rate (1 at 48 kHz, 2/3/4/6 at
// 24/16/12/8 kHz). The float CELT encoder consumes native-Fs frame sizes and
// upsamples to the 48 kHz core internally.
func (e *Encoder) celtUpsampleFactor() int {
	switch e.sampleRate {
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	}
	return 1
}

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
	// Default the CELT input upsample to the API rate's resampling_factor so the
	// CELT prefill (Fs/400 native) and CELT-only frames consume native-Fs sizes.
	// The redundancy CELT path overrides this to 1 (fixed 48 kHz block).
	e.celtEncoder.SetUpsample(e.celtUpsampleFactor())
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
func (e *Encoder) CELTSurroundTrim() OpusVal32 {
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
	n := min(len(e.celtEnergyMask), celt.MaxBands*2)
	e.celtEncoder.SetEnergyMask(e.celtEnergyMask[:n])
}
