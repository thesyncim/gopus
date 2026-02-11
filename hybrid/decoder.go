// Package hybrid implements the Hybrid decoder for Opus.
// Hybrid mode combines SILK (low frequencies, 0-8kHz) with CELT (high frequencies, 8-20kHz)
// for super-wideband and fullband speech at medium bitrates.
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode)
package hybrid

import (
	"errors"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// Constants for Hybrid mode
const (
	// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
	// Bands 0-16 are covered by SILK; CELT only decodes bands 17-21.
	HybridCELTStartBand = 17

	// SilkCELTDelay is the delay compensation in samples at 48kHz.
	// SILK output must be delayed relative to CELT for proper time alignment.
	// This matches celt.SilkCELTDelay = 60
	SilkCELTDelay = 60
)

// Errors for Hybrid decoding
var (
	// ErrInvalidFrameSize indicates an unsupported frame size for hybrid mode.
	// Hybrid only supports 10ms (480 samples) and 20ms (960 samples) frames.
	ErrInvalidFrameSize = errors.New("hybrid: invalid frame size (only 10ms/20ms supported)")

	// ErrDecodeFailed indicates a frame decode error.
	ErrDecodeFailed = errors.New("hybrid: frame decode failed")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("hybrid: nil range decoder")
)

// Decoder decodes Hybrid-mode Opus frames (SILK + CELT combined).
// Hybrid mode uses SILK for 0-8kHz and CELT for 8-20kHz.
//
// The decoder coordinates two sub-decoders:
// - SILK: Decodes low-frequency content at WB (16kHz), upsampled to 48kHz
// - CELT: Decodes high-frequency content (bands 17-21) at 48kHz
//
// SILK output is delayed by SilkCELTDelay (60) samples before summing with CELT.
type Decoder struct {
	// Sub-decoders
	silkDecoder *silk.Decoder
	celtDecoder *celt.Decoder

	// Delay buffer for SILK (60 samples at 48kHz per channel)
	// This ensures proper time alignment between SILK and CELT layers.
	silkDelayBuffer []float64

	// Note: Resamplers are NOT stored here. We use the SILK decoder's built-in
	// resamplers via GetResampler() and GetResamplerRightChannel() to ensure
	// resampler state persists across SILK-only <-> Hybrid mode transitions.
	// This matches libopus behavior where the silk_DecControl.resampler_state
	// is shared across all decoding modes.

	// Track previous packet stereo flag for transition handling.
	prevPacketStereo bool

	// Channel count (1 for mono, 2 for stereo)
	channels int

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	// Max frame size is 960 samples at 48kHz (20ms), stereo needs 960*2 = 1920 samples.
	scratchSilkUpsampled []float64 // SILK upsampled output (max 960*2 for stereo 20ms)
	scratchOutput        []float64 // Final output buffer (max 960*2 for stereo 20ms)
}

// NewDecoder creates a new Hybrid decoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
//
// The decoder initializes:
// - SILK decoder in WB (wideband, 16kHz) mode (always WB for hybrid)
// - CELT decoder for high-frequency bands
// - Delay buffer for SILK-CELT time alignment
func NewDecoder(channels int) *Decoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	// Max frame: 960 samples (20ms at 48kHz) * 2 channels = 1920
	maxSamples := 960 * channels

	return &Decoder{
		silkDecoder: silk.NewDecoder(),
		celtDecoder: celt.NewDecoder(channels),

		// Delay buffer: 60 samples per channel
		silkDelayBuffer: make([]float64, SilkCELTDelay*channels),

		channels: channels,

		// Pre-allocate scratch buffers for zero-alloc decode path
		scratchSilkUpsampled: make([]float64, maxSamples),
		scratchOutput:        make([]float64, maxSamples),
	}
}

// NewDecoderWithSharedDecoders creates a Hybrid decoder that reuses external SILK/CELT decoders.
// This is useful for sharing decoder state across Opus modes.
func NewDecoderWithSharedDecoders(channels int, silkDec *silk.Decoder, celtDec *celt.Decoder) *Decoder {
	d := NewDecoder(channels)
	if silkDec != nil {
		d.silkDecoder = silkDec
	}
	if celtDec != nil {
		d.celtDecoder = celtDec
	}
	return d
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	// Reset sub-decoders
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()

	// Clear delay buffer
	for i := range d.silkDelayBuffer {
		d.silkDelayBuffer[i] = 0
	}

	d.prevPacketStereo = false
}

// SetPrevPacketStereo synchronizes the previous packet stereo flag.
// This is used when Hybrid decoding is driven by an external Opus decoder.
func (d *Decoder) SetPrevPacketStereo(stereo bool) {
	d.prevPacketStereo = stereo
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SetBandwidth sets the CELT bandwidth for hybrid decoding.
func (d *Decoder) SetBandwidth(bw celt.CELTBandwidth) {
	d.celtDecoder.SetBandwidth(bw)
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// For hybrid mode, this returns the CELT decoder's final range since CELT encodes last.
func (d *Decoder) FinalRange() uint32 {
	return d.celtDecoder.FinalRange()
}

// ValidHybridFrameSize returns true if the frame size is valid for hybrid mode.
// Hybrid only supports 10ms (480 samples) and 20ms (960 samples) at 48kHz.
func ValidHybridFrameSize(frameSize int) bool {
	return frameSize == 480 || frameSize == 960
}

// decodeFrame decodes a single hybrid frame using a shared range decoder.
// This is the core decoding function that coordinates SILK and CELT.
//
// Parameters:
//   - rd: Range decoder (shared between SILK and CELT)
//   - frameSize: Expected output samples at 48kHz (480 or 960)
//   - stereo: True for stereo decoding
//
// Returns: PCM samples as float64 slice at 48kHz
func (d *Decoder) decodeFrame(rd *rangecoding.Decoder, frameSize int, packetStereo bool) ([]float64, error) {
	return d.decodeFrameWithHook(rd, frameSize, packetStereo, nil)
}

// decodeFrameWithHook decodes a single hybrid frame and allows a hook after SILK decode.
func (d *Decoder) decodeFrameWithHook(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) error) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	if !ValidHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Determine SILK frame duration from 48kHz frame size
	// 480 samples at 48kHz = 10ms, 960 samples = 20ms
	silkDuration := silk.Frame10ms
	if frameSize == 960 {
		silkDuration = silk.Frame20ms
	}

	// SILK sample count at 16kHz (WB)
	// 10ms: 160 samples at 16kHz
	// 20ms: 320 samples at 16kHz
	silkSamples := frameSize / 3 // 48kHz -> 16kHz = divide by 3

	monoToStereo := packetStereo && !d.prevPacketStereo
	stereoToMono := !packetStereo && d.prevPacketStereo
	if monoToStereo {
		// Reset side-channel state to match libopus mono->stereo transition.
		d.silkDecoder.ResetSideChannel()
		// Copy left resampler state to right resampler on mono->stereo transition.
		// This ensures the right channel has proper history for smooth transition.
		// Resetting would cause zeros at the start of the right channel output.
		leftResampler := d.silkDecoder.GetResampler(silk.BandwidthWideband)
		rightResampler := d.silkDecoder.GetResamplerRightChannel(silk.BandwidthWideband)
		if rightResampler != nil && leftResampler != nil {
			rightResampler.CopyFrom(leftResampler)
		}
	}

	// Step 1: Decode SILK layer (0-8kHz at 16kHz native rate)
	// SILK reads from the shared range decoder first.
	// Use SILK decoder's resamplers for state continuity between SILK-only and Hybrid modes.
	//
	// IMPORTANT: Notify the SILK decoder that we're using WB bandwidth.
	// This ensures proper resampler state management when transitioning between
	// SILK-only (NB/MB/WB) and Hybrid (always WB) modes.
	// Without this, the prevBandwidth tracking gets out of sync, causing
	// resamplers to not be reset when returning to SILK-only mode.
	d.silkDecoder.NotifyBandwidthChange(silk.BandwidthWideband)
	leftResampler := d.silkDecoder.GetResampler(silk.BandwidthWideband)
	rightResampler := d.silkDecoder.GetResamplerRightChannel(silk.BandwidthWideband)

	// Use scratch buffer for SILK upsampled output
	totalSamples := frameSize * d.channels
	silkUpsampled := d.ensureSilkUpsampled(totalSamples)

	// Scratch buffer for resampler output (float32)
	scratchF32L := d.silkDecoder.GetResamplerScratch(frameSize)
	scratchF32R := d.silkDecoder.GetResamplerScratchR(frameSize)

	if packetStereo {
		if d.channels == 1 {
			mid, err := d.silkDecoder.DecodeStereoFrameToMono(
				rd,
				silk.BandwidthWideband, // Always WB for hybrid
				silkDuration,
				true,
			)
			if err != nil {
				return nil, err
			}
			resamplerInput := d.silkDecoder.BuildMonoResamplerInput(mid)
			nL := leftResampler.ProcessInto(resamplerInput, scratchF32L)
			for i := 0; i < nL && i < totalSamples; i++ {
				silkUpsampled[i] = float64(scratchF32L[i])
			}
		} else {
			silkOutputL, silkOutputR, err := d.silkDecoder.DecodeStereoFrame(
				rd,
				silk.BandwidthWideband, // Always WB for hybrid
				silkDuration,
				true,
			)
			if err != nil {
				return nil, err
			}
			nL := leftResampler.ProcessInto(silkOutputL, scratchF32L)
			nR := rightResampler.ProcessInto(silkOutputR, scratchF32R)
			n := nL
			if nR < n {
				n = nR
			}
			for i := 0; i < n && i*2+1 < totalSamples; i++ {
				silkUpsampled[i*2] = float64(scratchF32L[i])
				silkUpsampled[i*2+1] = float64(scratchF32R[i])
			}
		}
	} else {
		// Use int16-native SILK decode/resampler path for hot hybrid decode.
		silkOutput, err := d.silkDecoder.DecodeFrameRawInt16(
			rd,
			silk.BandwidthWideband,
			silkDuration,
			true,
		)
		if err != nil {
			return nil, err
		}
		resamplerInput := d.silkDecoder.BuildMonoResamplerInputInt16(silkOutput)
		nL := leftResampler.ProcessInt16Into(resamplerInput, scratchF32L)
		if d.channels == 2 {
			if stereoToMono {
				nR := rightResampler.ProcessInt16Into(resamplerInput, scratchF32R)
				n := nL
				if nR < n {
					n = nR
				}
				for i := 0; i < n && i*2+1 < totalSamples; i++ {
					silkUpsampled[i*2] = float64(scratchF32L[i])
					silkUpsampled[i*2+1] = float64(scratchF32R[i])
				}
			} else {
				for i := 0; i < nL && i*2+1 < totalSamples; i++ {
					val := float64(scratchF32L[i])
					silkUpsampled[i*2] = val
					silkUpsampled[i*2+1] = val
				}
			}
		} else {
			for i := 0; i < nL && i < totalSamples; i++ {
				silkUpsampled[i] = float64(scratchF32L[i])
			}
		}
	}

	if afterSilk != nil {
		if err := afterSilk(rd); err != nil {
			return nil, err
		}
	}

	// Step 2: Decode CELT layer (8-20kHz, bands 17-21 only)
	// CELT reads from the same range decoder (SILK already consumed its portion)
	celtOutput, err := d.celtDecoder.DecodeFrameHybridWithPacketStereo(rd, frameSize, packetStereo)
	if err != nil {
		return nil, err
	}

	// Step 3: Use SILK output directly
	// The delay compensation is handled internally by the SILK resampler,
	// matching libopus behavior where SILK outputs at API rate with proper alignment.

	// Step 4: Sum SILK and CELT outputs into scratch buffer
	output := d.ensureOutput(totalSamples)

	for i := 0; i < totalSamples; i++ {
		silkSample := float64(0)
		celtSample := float64(0)

		if i < len(silkUpsampled) {
			silkSample = silkUpsampled[i]
		}
		if i < len(celtOutput) {
			celtSample = celtOutput[i]
		}

		output[i] = silkSample + celtSample
	}

	// Ensure we used the correct number of SILK samples
	_ = silkSamples // Used for documentation/debugging

	d.prevPacketStereo = packetStereo
	return output, nil
}

// ensureSilkUpsampled returns a pre-allocated buffer for SILK upsampled output.
func (d *Decoder) ensureSilkUpsampled(n int) []float64 {
	if cap(d.scratchSilkUpsampled) < n {
		d.scratchSilkUpsampled = make([]float64, n)
	} else {
		d.scratchSilkUpsampled = d.scratchSilkUpsampled[:n]
	}
	// Clear the buffer
	for i := range d.scratchSilkUpsampled {
		d.scratchSilkUpsampled[i] = 0
	}
	return d.scratchSilkUpsampled
}

// ensureOutput returns a pre-allocated buffer for final output.
func (d *Decoder) ensureOutput(n int) []float64 {
	if cap(d.scratchOutput) < n {
		d.scratchOutput = make([]float64, n)
	} else {
		d.scratchOutput = d.scratchOutput[:n]
	}
	return d.scratchOutput
}

// applyDelayMono applies the SilkCELTDelay to mono SILK output.
// Maintains a delay buffer of 60 samples that persists across frames.
func (d *Decoder) applyDelayMono(input []float64) []float64 {
	if len(input) == 0 {
		return input
	}

	output := make([]float64, len(input))

	// Output delayed samples: first 60 samples come from delay buffer
	delayLen := SilkCELTDelay
	if delayLen > len(input) {
		delayLen = len(input)
	}

	// Copy delay buffer to output start
	copy(output[:delayLen], d.silkDelayBuffer[:delayLen])

	// Copy input (minus tail) to output after delay
	if len(input) > SilkCELTDelay {
		copy(output[SilkCELTDelay:], input[:len(input)-SilkCELTDelay])
	}

	// Update delay buffer with input tail
	tailStart := len(input) - SilkCELTDelay
	if tailStart < 0 {
		tailStart = 0
	}
	copy(d.silkDelayBuffer, input[tailStart:])

	return output
}

// applyDelayStereo applies the SilkCELTDelay to stereo SILK output.
// Input/output are interleaved [L0, R0, L1, R1, ...].
// Delay buffer stores 60 samples per channel (120 total).
func (d *Decoder) applyDelayStereo(input []float64) []float64 {
	if len(input) == 0 {
		return input
	}

	output := make([]float64, len(input))

	// Stereo delay: 60 samples per channel = 120 interleaved values
	delaySamples := SilkCELTDelay * 2

	// Copy delay buffer to output start
	delayLen := delaySamples
	if delayLen > len(input) {
		delayLen = len(input)
	}
	copy(output[:delayLen], d.silkDelayBuffer[:delayLen])

	// Copy input (minus tail) to output after delay
	if len(input) > delaySamples {
		copy(output[delaySamples:], input[:len(input)-delaySamples])
	}

	// Update delay buffer with input tail
	tailStart := len(input) - delaySamples
	if tailStart < 0 {
		tailStart = 0
	}
	copy(d.silkDelayBuffer, input[tailStart:])

	return output
}

// upsample3x upsamples SILK output from 16kHz to 48kHz using linear interpolation.
// Retained for test helpers.
func upsample3x(samples []float32) []float64 {
	if len(samples) == 0 {
		return nil
	}

	output := make([]float64, len(samples)*3)

	for i := 0; i < len(samples); i++ {
		curr := float64(samples[i])
		var next float64
		if i+1 < len(samples) {
			next = float64(samples[i+1])
		} else {
			next = curr
		}

		output[i*3+0] = curr
		output[i*3+1] = curr*2/3 + next*1/3
		output[i*3+2] = curr*1/3 + next*2/3
	}

	return output
}
