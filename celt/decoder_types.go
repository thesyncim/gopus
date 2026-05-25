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
	downsample int // libopus CELT downsample factor, 1 at 48 kHz

	// Range decoder (set per frame)
	rangeDecoder *rangecoding.Decoder
	// rangeDecoderScratch holds a reusable decoder to avoid per-frame allocations.
	rangeDecoderScratch rangecoding.Decoder

	// Energy state (persists across frames for inter-frame prediction)
	prevEnergy  []celtGLog // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []celtGLog // Two frames ago energies (for anti-collapse)
	prevLogE    []celtGLog // Previous log energies (for anti-collapse history)
	prevLogE2   []celtGLog // Two frames ago log energies (for anti-collapse history)
	// Slow background floor estimate (libopus backgroundLogE cadence).
	backgroundEnergy []celtGLog

	// Synthesis state (persists for overlap-add)
	overlapBuffer []celtSig // Previous frame overlap tail [Overlap * channels]
	preemphState  []celtSig // De-emphasis filter state [channels]

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int     // Pitch period for comb filter
	postfilterGain   float32 // Comb filter gain
	postfilterTapset int     // Filter tap configuration (0, 1, or 2)
	// Previous postfilter state for overlap cross-fade
	postfilterPeriodOld int
	postfilterGainOld   float32
	postfilterTapsetOld int
	// Postfilter history buffer (per-channel)
	postfilterMem []celtSig
	// On no-gain frames, postfilter history can be lazily reconstructed from
	// the longer PLC decode history, avoiding a duplicate history shift.
	postfilterMemFromPLC   bool
	postfilterMemPLCBacked bool
	// PLC decode history buffer (per-channel), sized to match libopus
	// DECODE_BUFFER_SIZE cadence used by celt_plc_pitch_search().
	plcDecodeMem []celtSig
	// Stereo planar decode keeps PLC history as a ring during good packets and
	// materializes it only before PLC consumers need contiguous libopus layout.
	plcDecodeMemRingActive bool
	plcDecodeMemRingStart  int

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
	plcLPC []float32

	// Band processing state
	collapseMask uint32 // Tracks which bands received pulses (for anti-collapse)

	// Bandwidth (Opus TOC-derived)
	bandwidth              CELTBandwidth
	phaseInversionDisabled bool
	complexity             int

	// Channel transition tracking (for mono-to-stereo overlap buffer clearing)
	prevStreamChannels int // Previous packet's channel count (0 = uninitialized)
	directOutPCM       []float32
	decoderQEXTFields

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	scratchPrevEnergy     []celtGLog
	scratchPrevEnergyGLog []celtGLog
	scratchEnergies       []celtGLog
	scratchTFRes          []int
	scratchOffsets        []int
	scratchPulses         []int
	scratchFineQuant      []int
	scratchFinePriority   []int
	scratchPrevBandEnergy []float32
	scratchSilenceE       []celtGLog
	scratchCaps           []int
	scratchAllocWork      []int
	scratchBands          bandDecodeScratch
	scratchIMDCTF32       imdctScratchF32
	scratchIMDCTF32R      imdctScratchF32
	scratchSynth          []float64
	scratchSynthR         []float64
	scratchStereo         []float64
	scratchShortCoeffs    []float64
	scratchMonoToStereoR  []float64 // For coeffsR in decodeMonoPacketToStereo (must not alias scratchSynthR used by SynthesizeStereo)
	scratchMonoMix        []float64 // For coeffsMono in decodeStereoPacketToMono (must not alias scratchShortCoeffs used by Synthesize)
	postfilterScratch     []float64
	scratchPLC            []float64 // Scratch buffer for PLC concealment samples
	scratchPLCPitchLP     []float32
	scratchPLCPitchSearch plcPitchSearchScratch
	scratchPLCFIRTmp      []celtSig
	scratchPLCWindowed    []celtSig
	scratchPLCIIRY        []float32
	scratchPLCBuf         []celtSig
	scratchPLCExc         []celtSig
	decoderDREDState
	scratchPLCFoldSrc     []celtSig
	scratchPLCFoldDst     []celtSig
	scratchPLCHybridNormL []celtNorm
	scratchPLCHybridNormR []celtNorm
}
