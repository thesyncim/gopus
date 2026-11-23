package celt

import (
	"errors"

	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	frameNone = iota
	frameNormal
	framePLCNoise
	framePLCPeriodic
	framePLCNeural
	frameDRED
)

// Decoding errors
var (
	// ErrInvalidFrame indicates the frame data is invalid or corrupted.
	ErrInvalidFrame = errors.New("celt: invalid frame data")

	// ErrInvalidFrameSize indicates an invalid frame size.
	ErrInvalidFrameSize = errors.New("celt: invalid frame size")

	// ErrInvalidSampleRate indicates a sample rate outside the Opus API set.
	ErrInvalidSampleRate = errors.New("celt: invalid sample rate")

	// ErrOutputTooSmall indicates the caller-provided PCM buffer is too small.
	ErrOutputTooSmall = errors.New("celt: output buffer too small")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("celt: nil range decoder")

	// ErrInvalidComplexity indicates the decoder complexity is out of range.
	ErrInvalidComplexity = errors.New("celt: invalid complexity (must be 0-10)")
)

// Decoder decodes CELT frame data and synthesizes PCM. It retains band-energy
// prediction, overlap, de-emphasis, postfilter, and loss history across frames.
// Its scratch and history are mutable, so use one decoder per stream and
// serialize access. See RFC 6716 Section 4.3.
type Decoder struct {
	// Pointer-bearing fields are grouped first so Go's GC stops scanning at
	// ptrdata, leaving scalar state and fixed-size value arrays in the noscan
	// tail.

	// Range decoder (set per frame)
	rangeDecoder        *rangecoding.Decoder
	rangeDecoderScratch rangecoding.Decoder

	// perMode carries band edges, widths, logN, allocation vectors, and pulse
	// cache for an Opus Custom mode whose layout differs from the static CELT
	// tables.
	perMode *perModeTables

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State

	// Optional test-only stage traces (nil in production).
	synthTrace    *synthesisStageTrace
	plcStageTrace *plcStageTrace

	decoderQEXTFields
	decoderDREDState

	// Energy state (persists across frames for inter-frame prediction)
	prevEnergy       []celtGLog // Previous frame band energies [MaxBands * channels]
	prevLogE         []celtGLog // Previous log energies (for anti-collapse history)
	prevLogE2        []celtGLog // Two frames ago log energies (for anti-collapse history)
	backgroundEnergy []celtGLog // Slow background floor estimate (libopus backgroundLogE cadence)

	// decodeMem is libopus CELTDecoder._decode_mem. Each channel reserves
	// 2*decodeMemLineLen() samples; its line is the decodeMemLineLen() window at
	// decodeMemOff within that reservation.
	decodeMem    []celtSig
	preemphState []celtSig // De-emphasis filter state [channels]
	plcLPC       []float32 // Stored LPC coefficients per channel for periodic PLC continuation.

	redundancyBytes []byte
	directOutPCM    []float32

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	scratchPrevEnergyGLog   []celtGLog
	scratchEnergies         []celtGLog
	scratchStereoEnergies   []celtGLog
	scratchTFRes            []int32
	scratchOffsets          []int32
	scratchPulses           []int32
	scratchFineQuant        []int32
	scratchFinePriority     []int32
	scratchPrevBandEnergy   []float32
	scratchCaps             []int32
	scratchAllocWork        []int32
	scratchBands            bandDecodeScratch
	scratchIMDCTF32         imdctScratchF32
	scratchSpecRF32         []float32
	scratchStereoF32        []float32
	scratchShortCoeffsF32   []float32
	scratchPCM              []float32
	scratchSynthF32         []float32
	scratchSynthRF32        []float32
	scratchPLCPitchHist     []celtSig
	scratchMonoToStereoRF32 []float32
	scratchMonoMixF32       []float32
	postfilterWindowSqF32   []float32
	postfilterWindowSqOf    *float32  // first element of the window postfilterWindowSqF32 squares
	scratchPLC              []float32 // Scratch buffer for PLC concealment samples
	scratchPLCPitchLP       []float32
	scratchPLCPitchSearch   plcPitchSearchScratch
	scratchPLCFIRTmp        []celtSig
	scratchPLCWindowed      []celtSig
	scratchPLCIIRY          []float32
	scratchPLCBuf           []celtSig
	scratchPLCExc           []celtSig
	scratchPLCFoldDst       []celtSig
	scratchPLCHybridNormL   []celtNorm
	scratchPLCHybridNormR   []celtNorm

	// --- Pointer-free (noscan) tail ---

	// Configuration
	channels        int32 // libopus CELTDecoder.channels
	sampleRate      int32 // Output sample rate (typically 48000)
	downsample      int32 // libopus CELTDecoder.downsample, 1 at 48 kHz
	decodeMemOff    int
	synthOverlap    int
	deemphCoef      float32
	deemphCoef1     float32
	deemphCoef3     float32
	customScaleBase int
	customEffBands  int
	customFrameSize int

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod    int32   // libopus CELTDecoder.postfilter_period
	postfilterGain      float32 // Comb filter gain
	postfilterTapset    int32   // libopus CELTDecoder.postfilter_tapset
	postfilterPeriodOld int32
	postfilterGainOld   float32
	postfilterTapsetOld int32

	// Error recovery / deterministic randomness
	rng                        uint32 // RNG state for PLC and folding
	plcLossDuration            int32  // CELT loss duration in libopus LM units (saturates at 10000)
	plcDuration                int32  // Mirrors libopus st->plc_duration for periodic/noise/DRED gating
	plcLastFrameType           int32  // Mirrors libopus st->last_frame_type
	plcLastPitchPeriod         int32
	plcSkip                    bool // Mirrors libopus st->skip_plc two-good-packets gate
	plcPrevLossWasPeriodic     bool
	plcPrefilterAndFoldPending bool

	// Band processing and mode state
	collapseMask           uint32 // Tracks which bands received pulses (for anti-collapse)
	bandwidth              CELTBandwidth
	customEndBand          int32
	complexity             int32
	redundancyRange        uint32
	redundancyFrameSize    int
	prevStreamChannels     int32 // libopus CELTDecoder.stream_channels mirror (0 = uninitialized)
	phaseInversionDisabled bool
	redundancyActive       bool
	directOutAccum         bool

	plcStateVal plc.State
	stdAlloc    stdAllocState
}
