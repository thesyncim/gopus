// Package hybrid implements the Hybrid decoder for Opus.
// Hybrid mode combines SILK (low frequencies, 0-8kHz) with CELT (high frequencies, 8-20kHz)
// for super-wideband and fullband speech at medium bitrates.
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode)
package hybrid

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
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

	// Libopus-compatible resamplers for SILK 16k -> 48k
	silkResamplerL *silk.LibopusResampler
	silkResamplerR *silk.LibopusResampler
	// Mono resampler history (matches SILK sMid buffering)
	silkMid [2]int16

	// Track previous packet stereo flag for transition handling.
	prevPacketStereo bool

	// Channel count (1 for mono, 2 for stereo)
	channels int
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

	return &Decoder{
		silkDecoder: silk.NewDecoder(),
		celtDecoder: celt.NewDecoder(channels),

		// Delay buffer: 60 samples per channel
		silkDelayBuffer: make([]float64, SilkCELTDelay*channels),
		silkResamplerL:  silk.NewLibopusResampler(16000, 48000),
		silkResamplerR:  silk.NewLibopusResampler(16000, 48000),

		channels: channels,
	}
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

	// Reset resamplers and mono history
	if d.silkResamplerL != nil {
		d.silkResamplerL.Reset()
	}
	if d.silkResamplerR != nil {
		d.silkResamplerR.Reset()
	}
	d.silkMid = [2]int16{0, 0}
	d.prevPacketStereo = false
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SetBandwidth sets the CELT bandwidth for hybrid decoding.
func (d *Decoder) SetBandwidth(bw celt.CELTBandwidth) {
	d.celtDecoder.SetBandwidth(bw)
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

	// Handle mono<->stereo transitions for resampler/ delay state.
	if packetStereo && !d.prevPacketStereo {
		// Transition mono -> stereo: sync right resampler from left.
		if d.silkResamplerL != nil && d.silkResamplerR != nil {
			d.silkResamplerR.CopyFrom(d.silkResamplerL)
		}
	}

	// Step 1: Decode SILK layer (0-8kHz at 16kHz native rate)
	// SILK reads from the shared range decoder first.
	var silkUpsampled []float64
	if packetStereo {
		silkOutputL, silkOutputR, err := d.silkDecoder.DecodeStereoFrame(
			rd,
			silk.BandwidthWideband, // Always WB for hybrid
			silkDuration,
			true,
		)
		if err != nil {
			return nil, err
		}
		if d.silkResamplerL == nil {
			d.silkResamplerL = silk.NewLibopusResampler(16000, 48000)
		}
		if d.silkResamplerR == nil {
			d.silkResamplerR = silk.NewLibopusResampler(16000, 48000)
		}
		upL := d.silkResamplerL.Process(silkOutputL)
		upR := d.silkResamplerR.Process(silkOutputR)
		silkUpsampled = make([]float64, len(upL)*2)
		for i := range upL {
			silkUpsampled[i*2] = float64(upL[i])
			silkUpsampled[i*2+1] = float64(upR[i])
		}
	} else {
		silkOutput, err := d.silkDecoder.DecodeFrame(
			rd,
			silk.BandwidthWideband,
			silkDuration,
			true,
		)
		if err != nil {
			return nil, err
		}
		if d.silkResamplerL == nil {
			d.silkResamplerL = silk.NewLibopusResampler(16000, 48000)
		}
		// Libopus-style sMid buffering for mono resampling.
		up := d.resampleMonoWithMid(silkOutput)
		if d.channels == 2 {
			silkUpsampled = make([]float64, len(up)*2)
			for i := range up {
				val := float64(up[i])
				silkUpsampled[i*2] = val
				silkUpsampled[i*2+1] = val
			}
		} else {
			silkUpsampled = make([]float64, len(up))
			for i := range up {
				silkUpsampled[i] = float64(up[i])
			}
		}
	}

	// Step 2: Decode CELT layer (8-20kHz, bands 17-21 only)
	// CELT reads from the same range decoder (SILK already consumed its portion)
	celtOutput, err := d.celtDecoder.DecodeFrameHybridWithPacketStereo(rd, frameSize, packetStereo)
	if err != nil {
		return nil, err
	}

	// Step 3: Apply 60-sample delay to SILK output
	// This compensates for the time alignment difference between SILK and CELT.
	var silkDelayed []float64
	if d.channels == 2 {
		// For mono packets in a stereo stream, keep delay buffer in sync.
		if !packetStereo {
			d.syncDelayBufferMono()
		}
		silkDelayed = d.applyDelayStereo(silkUpsampled)
	} else {
		silkDelayed = d.applyDelayMono(silkUpsampled)
	}

	// Step 4: Sum SILK and CELT outputs
	totalSamples := frameSize * d.channels
	output := make([]float64, totalSamples)

	for i := 0; i < totalSamples; i++ {
		silkSample := float64(0)
		celtSample := float64(0)

		if i < len(silkDelayed) {
			silkSample = silkDelayed[i]
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

// resampleMonoWithMid resamples mono SILK output using libopus-style sMid buffering.
func (d *Decoder) resampleMonoWithMid(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}
	if d.silkResamplerL == nil {
		d.silkResamplerL = silk.NewLibopusResampler(16000, 48000)
	}

	resamplerInput := make([]float32, len(samples))
	resamplerInput[0] = float32(d.silkMid[1]) / 32768.0
	if len(samples) > 1 {
		copy(resamplerInput[1:], samples[:len(samples)-1])
		d.silkMid[0] = float32ToInt16(samples[len(samples)-2])
		d.silkMid[1] = float32ToInt16(samples[len(samples)-1])
	} else {
		d.silkMid[0] = d.silkMid[1]
		d.silkMid[1] = float32ToInt16(samples[0])
	}

	return d.silkResamplerL.Process(resamplerInput)
}

// syncDelayBufferMono ensures the stereo delay buffer is in mono state.
func (d *Decoder) syncDelayBufferMono() {
	if d.channels != 2 || len(d.silkDelayBuffer) < SilkCELTDelay*2 {
		return
	}
	for i := 0; i < SilkCELTDelay; i++ {
		d.silkDelayBuffer[i*2+1] = d.silkDelayBuffer[i*2]
	}
}

func float32ToInt16(v float32) int16 {
	scaled := float64(v) * 32768.0
	if scaled > 32767.0 {
		return 32767
	}
	if scaled < -32768.0 {
		return -32768
	}
	return int16(math.RoundToEven(scaled))
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
