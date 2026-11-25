// Package encoder implements the stateful Opus encoder described by RFC 6716.
// It coordinates SILK, CELT, and Hybrid coding, selects a mode and bandwidth
// for each frame, applies bitrate control, and assembles the packet.
//
// A frame uses SILK for predictive speech coding, CELT for transform coding, or
// both in Hybrid mode. [ModeAuto] selects among them from the configured rate
// and the encoder's signal analysis. The frame path follows libopus 1.6.1's
// `src/opus_encoder.c` and `src/analysis.c`.
//
// [Encoder] retains analysis, sub-encoder, and transition history across calls.
// Keep one encoder per stream and serialize access. [Encoder.FinalRange] exposes the
// entropy coder's final range for paired comparisons; packet and range claims
// apply to the matching reference configuration and tested cases. See the
// coverage summary in `reports/validation.md#coverage`.
//
// Most applications should use the top-level gopus API, which owns this
// implementation state and packet framing.
package encoder

import (
	"errors"
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
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

	// ErrBufferTooSmall indicates the output budget cannot represent the frame.
	ErrBufferTooSmall = errors.New("encoder: output buffer too small")

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
	// Pointer-bearing fields are grouped first so Go's GC stops scanning at
	// ptrdata, leaving scalar state and fixed-size value arrays in the noscan
	// tail.

	// Sub-encoders (created lazily)
	silk        *silk.PacketEncoder
	celtEncoder *celt.Encoder

	// frameRangeEncoder and framePayload are the range coder of a frame and
	// its buffer (opus_encode_frame_native's ec_enc over data+1).
	frameRangeEncoder rangecoding.Encoder
	framePayload      []byte

	fec *fecState
	dtx *dtxState

	// celtEnergyMask carries per-band surround masking into CELT dynalloc control.
	celtEnergyMask []float32

	encoderQEXTFields
	encoderFixedCELTFields
	encoderFixedOuterQ8Fields

	// dnnBlob retains the validated model blob for encoder neural features,
	// matching the lifetime of libopus OPUS_SET_DNN_BLOB.
	dnnBlob *dnnblob.Blob
	encoderDREDFields

	// Tonality and speech/music analysis.
	analyzer *TonalityAnalysisState

	inputBuffer []opusRes
	delayBuffer []opusRes

	// pcmBump backs the two frameSize-sized input-domain PCM scratch buffers.
	pcmBump arena.Bump[opusRes]

	// Scratch buffers for zero-allocation encoding
	scratchDCPCM             []opusRes // DC rejected PCM buffer
	scratchInputPCM          []opusRes // Public PCM rounded into the libopus opus_res domain
	scratchPacket            []byte    // Output packet buffer
	scratchFrameSlots        [6][]byte // Per-subframe slice headers for long packets
	scratchFrameBytes        []byte    // Backing storage for kept subframe payloads
	scratchQEXTPayloadBytes  []byte    // Backing storage for kept QEXT payloads
	scratchDelayedPCM        []opusRes // Delay-compensated CELT input
	scratchTransitionPrefill []opusRes // Delay-history CELT transition prefill window (Fs/400)
	scratchSilkPrefill       []opusRes
	scratchCELTPrefill       []opusRes // CELT transition prefill source (Fs/400 * channels)
	floatInputFrame          []float32 // Current public float32 frame view, if available

	// Embedded FEC, DTX, and tonality analyzer state values. analyzerVal sits
	// at the pointer/noscan boundary so its ~26 KB noscan tail and all scalar
	// fields below remain excluded from GC scanning.
	fecVal      fecState
	dtxVal      dtxState
	analyzerVal TonalityAnalysisState

	// --- Pointer-free (noscan) tail ---

	// silkMode mirrors libopus st->silk_mode.
	silkMode             silk.EncControl
	silkPrefillPending   bool
	silkBWSwitch         bool
	nonfinalFrame        bool
	redundancyScratch    [257]byte
	frameFinalRange      uint32
	prevHBGain           opusVal16
	hybridStereoWidthQ14 int16

	// Configuration
	mode              Mode
	bandwidth         types.Bandwidth
	sampleRate        int32
	channels          int32
	frameSize         int32 // Per-channel samples at the configured sample rate.
	lowDelay          bool
	voipApp           bool
	restrictedSilkApp bool

	// Bitrate controls
	bitrateMode   BitrateMode
	useVBR        bool
	vbrConstraint bool
	bitrate       int32 // Target bits per second

	// FEC / VAD / multi-frame DTX controls
	fecEnabled                  bool
	packetLoss                  int32 // Expected packet loss percentage (0-100)
	lastOpusVADActive           bool
	lastOpusVADValid            bool
	lastOpusVADProb             float32
	lastOpusVADActivityObserved bool
	multiFrameDTXCount          int
	multiFrameLastSubframeDTX   bool
	multiFrameCommitted         bool
	multiFrameLastCommitted     bool

	// DTX (Discontinuous Transmission) controls
	dtxEnabled bool
	rng        uint32 // RNG for comfort noise
	finalRange uint32

	// Complexity and mode controls
	complexity             int32
	signalType             types.Signal
	maxBandwidth           types.Bandwidth
	forceChannels          int32
	lfe                    bool
	lsbDepth               int32
	predictionDisabled     bool
	phaseInversionDisabled bool

	// DC rejection / variable-cutoff HP filter state.
	hpMem                 [4]float32
	variableHPSmth2Q15    int32
	variableHPSmth2Inited bool

	// Last frame analysis info from RunAnalysis(), used by mode heuristics.
	lastAnalysisInfo    AnalysisInfo
	lastAnalysisValid   bool
	lastAnalysisFresh   bool
	analysisReadPosBak  int32
	analysisSubframeBak int32
	analysisReadBakSet  bool
	prevMode            Mode
	prevPacketMode      Mode
	prevAutoMode        Mode
	intMode             Mode
	intBandwidth        types.Bandwidth

	// Auto-mode state (matching libopus OpusEncoder fields)
	voiceRatio             int32           // Persistent voice ratio (-1 = unset, 0-100)
	detectedBandwidth      types.Bandwidth // Analysis-detected bandwidth; narrowband is enum value 0.
	detectedBandwidthValid bool            // Distinguishes a valid narrowband result from no analysis result.
	streamChannels         int32           // Actual encoding channels (1 or 2)
	prevChannels           int32           // Previous frame's streamChannels
	autoBandwidth          types.Bandwidth // Last auto-selected bandwidth (for hysteresis)
	first                  bool            // First frame flag
	lbrrCoded              bool            // Previous frame FEC coding decision
	userBandwidth          types.Bandwidth // User-set bandwidth value
	userBandwidthSet       bool            // Whether userBandwidth is explicitly set
	widthMem               StereoWidthMem  // Stateful stereo width computation memory
	toMono                 int32           // Stereo->mono transition countdown (0=inactive)
	fecConfig              int32           // FEC config: 0=disabled, 1=enabled, 2=music-safe
	hasCELTPrefill         bool
	floatInputExact        bool // True when pcm originated from float32 samples
	scratchPacketBuf       [defaultScratchPacketBytes]byte
}

// NewEncoder creates an Opus encoder for sampleRate and channels. Unsupported
// sample rates fall back to 48 kHz; 96 kHz is retained when QEXT is enabled.
// Channel counts below one select mono, and counts above two select stereo.
func NewEncoder(sampleRate, channels int) *Encoder {
	e := &Encoder{}
	e.Init(sampleRate, channels)
	return e
}

// Init initializes e in place for sampleRate and channels so callers can embed
// encoder.Encoder inline without a separate heap allocation.
func (e *Encoder) Init(sampleRate, channels int) {
	switch sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
	case 96000:
		if !extsupport.QEXT {
			sampleRate = 48000
		}
	default:
		sampleRate = 48000
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	e.mode = ModeAuto
	e.bandwidth = types.BandwidthFullband
	e.sampleRate = int32(sampleRate)
	e.channels = int32(channels)
	e.frameSize = int32(sampleRate / 50)
	e.lowDelay = false
	e.bitrateMode = ModeCVBR
	e.useVBR = true
	e.vbrConstraint = true
	e.bitrate = 64000
	e.fecEnabled = false
	e.packetLoss = 0
	e.dtxEnabled = false
	e.rng = 22222
	e.complexity = 9
	e.signalType = types.SignalAuto
	e.maxBandwidth = types.BandwidthFullband
	e.forceChannels = -1
	e.lsbDepth = 24
	e.predictionDisabled = false
	e.phaseInversionDisabled = false
	e.analyzerVal.Init(sampleRate)
	e.analyzer = &e.analyzerVal
	e.scratchPacket = e.scratchPacketBuf[:]
	e.prevMode = ModeAuto
	e.prevPacketMode = ModeAuto
	e.prevAutoMode = ModeAuto
	e.intMode = ModeHybrid
	e.intBandwidth = types.BandwidthFullband
	e.voiceRatio = -1
	e.streamChannels = int32(channels)
	// opus_encoder_init zeroes the state before setting stream_channels;
	// prev_channels stays zero until the first encoded frame.
	e.prevChannels = 0
	e.autoBandwidth = types.BandwidthFullband
	e.first = true
	e.prevHBGain = 1
	e.hybridStereoWidthQ14 = 1 << 14
	e.encoderFixedOuterQ8Fields = newFixedOuterQ8Fields()
	e.fec = &e.fecVal
	e.dtxVal.init()
	e.dtx = &e.dtxVal
}

// Reset clears the encoder state for a new stream.
func (e *Encoder) Reset() {
	// hp_mem is inside OPUS_ENCODER_RESET_START and is cleared by
	// OPUS_RESET_STATE (opus_encoder.c:112-121, 3254).
	e.hpMem = [4]float32{}
	e.variableHPSmth2Q15 = 0
	e.variableHPSmth2Inited = false
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
	e.silkBWSwitch = false
	e.nonfinalFrame = false
	e.frameFinalRange = 0
	e.prevHBGain = 1
	e.hybridStereoWidthQ14 = 1 << 14
	e.resetFixedOuterQ8()
	if e.celtEncoder != nil {
		e.celtEncoder.Reset()
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
	e.lastOpusVADActivityObserved = false
	e.prevMode = ModeAuto
	e.prevPacketMode = ModeAuto
	e.prevAutoMode = ModeAuto
	e.intMode = ModeHybrid
	e.intBandwidth = types.BandwidthFullband
	e.detectedBandwidth = 0
	e.detectedBandwidthValid = false
	// C ref: opus_encoder.c OPUS_RESET_STATE sets st->bandwidth =
	// OPUS_BANDWIDTH_FULLBAND. st->bandwidth (the decided bandwidth reported by
	// OPUS_GET_BANDWIDTH) sits after OPUS_ENCODER_RESET_START, so the reset
	// region clears it and the handler re-seeds it to FULLBAND. The user
	// bandwidth request (userBandwidth/userBandwidthSet) is before the reset
	// start and is preserved.
	e.bandwidth = types.BandwidthFullband
	e.streamChannels = int32(e.channels)
	// OPUS_RESET_STATE clears prev_channels along with the other frame state;
	// only stream_channels is then re-seeded (opus_encoder.c:3249-3265).
	e.prevChannels = 0
	e.autoBandwidth = types.BandwidthFullband
	e.first = true
	e.widthMem = StereoWidthMem{}
	e.toMono = 0
	if extsupport.DREDRuntime {
		e.resetDREDControls()
	}
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
	inputPCM := e.prepareOpusResInput(pcm)
	e.prepareFixedInputRes(pcm)
	defer e.clearFixedInputRes()
	e.SetFloatInputFrame(pcm)
	defer e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}

// EncodeShortMixedWithAnalysisMaxBytes encodes the opus_res samples produced by
// libopus's short input callback. analysisPCM contains the original short input
// channels, converted exactly to float32 by division by 32768 before routing;
// it is separate from any projection-mixed coding samples.
func (e *Encoder) EncodeShortMixedWithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	if len(pcm) != expectedLen || frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%channels != 0 {
		return nil, ErrInvalidFrameSize
	}
	inputPCM := e.prepareOpusResInput(pcm)
	e.prepareFixedInputRes(pcm)
	defer e.clearFixedInputRes()
	// opus_encode_native uses min(16, st->lsb_depth) for this call while the
	// configured control survives the call.
	configuredDepth := e.lsbDepth
	if e.lsbDepth > 16 {
		e.lsbDepth = 16
	}
	if e.analyzer != nil {
		e.analyzer.SetLSBDepth(int(e.lsbDepth))
	}
	defer func() {
		e.lsbDepth = configuredDepth
		if e.analyzer != nil {
			e.analyzer.SetLSBDepth(int(configuredDepth))
		}
	}()
	e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}

func (e *Encoder) prepareOpusResInput(pcm []float32) []opusRes {
	// Back all frame-sized DC/delay scratch buffers with one reusable arena.
	// pcm is already in the opusRes (float32) domain and is never mutated by
	// encodeOpusResWithAnalysisMaxBytes, so return it directly without copying.
	if len(pcm) > 0 {
		ch := max(int(e.channels), 1)
		sr := int(e.sampleRate)
		encBufLen := (sr / 100) * ch
		prefillLen := (sr / 400) * ch
		need := 2*len(pcm) + prefillLen
		carveDelay := len(e.delayBuffer) == 0 && encBufLen > 0
		if carveDelay {
			need += encBufLen
		}
		if e.pcmBump.Cap() < need {
			e.pcmBump.Ensure(need)
			e.scratchDCPCM = e.pcmBump.AllocN(len(pcm))
			e.scratchDelayedPCM = e.pcmBump.AllocN(len(pcm))
			if prefillLen > 0 {
				e.scratchTransitionPrefill = e.pcmBump.AllocN(prefillLen)
			}
			if carveDelay {
				e.delayBuffer = e.pcmBump.AllocN(encBufLen)
			}
		}
	}
	return pcm
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
