package celt

import (
	"errors"

	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/rangecoding"
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

	// ErrInvalidFrameSize indicates an unsupported frame size.
	ErrInvalidFrameSize = errors.New("celt: invalid frame size")

	// ErrOutputTooSmall indicates the caller-provided PCM buffer is too small.
	ErrOutputTooSmall = errors.New("celt: output buffer too small")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("celt: nil range decoder")
)

// Decoder decodes CELT frames from an Opus packet.
// It maintains state across frames for proper audio continuity via overlap-add
// synthesis and energy prediction.
//
// CELT is the transform-based layer of Opus, using the Modified Discrete Cosine
// Transform (MDCT) for music and general audio. The decoder reconstructs audio by:
// 1. Decoding energy envelope (coarse + fine quantization)
// 2. Decoding normalized band shapes via PVQ
// 3. Applying denormalization (scaling by energy)
// 4. Performing IMDCT synthesis with overlap-add
// 5. Applying de-emphasis filter
//
// Reference: RFC 6716 Section 4.3
type Decoder struct {
	// Configuration
	channels   int // 1 or 2
	sampleRate int // Output sample rate (typically 48000)

	// Range decoder (set per frame)
	rangeDecoder *rangecoding.Decoder
	// rangeDecoderScratch holds a reusable decoder to avoid per-frame allocations.
	rangeDecoderScratch rangecoding.Decoder

	// Energy state (persists across frames for inter-frame prediction)
	prevEnergy  []float64 // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []float64 // Two frames ago energies (for anti-collapse)
	prevLogE    []float64 // Previous log energies (for anti-collapse history)
	prevLogE2   []float64 // Two frames ago log energies (for anti-collapse history)
	// Slow background floor estimate (libopus backgroundLogE cadence).
	backgroundEnergy []float64

	// Synthesis state (persists for overlap-add)
	overlapBuffer []float64 // Previous frame overlap tail [Overlap * channels]
	preemphState  []float64 // De-emphasis filter state [channels]

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int     // Pitch period for comb filter
	postfilterGain   float64 // Comb filter gain
	postfilterTapset int     // Filter tap configuration (0, 1, or 2)
	// Previous postfilter state for overlap cross-fade
	postfilterPeriodOld int
	postfilterGainOld   float64
	postfilterTapsetOld int
	// Postfilter history buffer (per-channel)
	postfilterMem []float64
	// PLC decode history buffer (per-channel), sized to match libopus
	// DECODE_BUFFER_SIZE cadence used by celt_plc_pitch_search().
	plcDecodeMem []float64

	// Error recovery / deterministic randomness
	rng uint32 // RNG state for PLC and folding

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State
	// CELT loss duration in libopus LM units (saturates at 10000).
	plcLossDuration int
	// Mirrors libopus st->plc_duration for periodic/noise/DRED gating.
	plcDuration int
	// Mirrors libopus st->last_frame_type.
	plcLastFrameType int
	// Mirrors libopus st->skip_plc two-good-packets gate.
	plcSkip bool
	// Periodic PLC cadence state (mirrors libopus decode_lost() behavior).
	plcLastPitchPeriod     int
	plcPrevLossWasPeriodic bool
	// Mirrors libopus prefilter_and_fold cadence after periodic PLC.
	plcPrefilterAndFoldPending bool
	// Stored LPC coefficients per channel for periodic PLC continuation.
	plcLPC []float64

	// Band processing state
	collapseMask uint32 // Tracks which bands received pulses (for anti-collapse)

	// Bandwidth (Opus TOC-derived)
	bandwidth CELTBandwidth

	// Channel transition tracking (for mono-to-stereo overlap buffer clearing)
	prevStreamChannels int // Previous packet's channel count (0 = uninitialized)
	directOutPCM       []float32
	pendingQEXTPayload []byte
	qextOldBandE       []float64

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	scratchPrevEnergy     []float64
	scratchPrevLogE       []float64
	scratchPrevLogE2      []float64
	scratchEnergies       []float64
	scratchTFRes          []int
	scratchOffsets        []int
	scratchPulses         []int
	scratchFineQuant      []int
	scratchFinePriority   []int
	scratchPrevBandEnergy []float64
	scratchSilenceE       []float64
	scratchCaps           []int
	scratchAllocWork      []int
	scratchBands          bandDecodeScratch
	scratchIMDCT          imdctScratch
	scratchIMDCTF32       imdctScratchF32
	scratchSynth          []float64
	scratchSynthR         []float64
	scratchStereo         []float64
	scratchQEXTEnergies   []float64
	scratchQEXTSpectrumL  []float64
	scratchQEXTSpectrumR  []float64
	scratchShortCoeffs    []float64
	scratchMonoToStereoR  []float64 // For coeffsR in decodeMonoPacketToStereo (must not alias scratchSynthR used by SynthesizeStereo)
	scratchMonoMix        []float64 // For coeffsMono in decodeStereoPacketToMono (must not alias scratchShortCoeffs used by Synthesize)
	postfilterScratch     []float64
	scratchPLC            []float64 // Scratch buffer for PLC concealment samples
	scratchPLCPitchLP     []float64
	scratchPLCPitchSearch encoderScratch
	scratchPLCFIRTmp      []float64
	scratchPLCWindowed    []float64
	scratchPLCIIRMem      []float64
	scratchPLCBuf         []float64
	scratchPLCExc         []float64
	scratchPLCUpdate48k   []float32
	scratchPLCDREDNeural  []float32
	scratchPLCDREDBase    []float64
	scratchPLCFoldSrc     []float64
	scratchPLCFoldDst     []float64
	scratchPLCHybridNormL []float64
	scratchPLCHybridNormR []float64
	scratchQEXTPulses     []int
	scratchQEXTFineQuant  []int

	qextRangeDecoderScratch rangecoding.Decoder
}
