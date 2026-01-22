// decoder.go implements the public Decoder API for Opus decoding.

package gopus

import (
	"gopus/internal/hybrid"
)

// Decoder decodes Opus packets into PCM audio samples.
//
// A Decoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own Decoder instance.
//
// The decoder supports all Opus modes (SILK, Hybrid, CELT) and automatically
// detects the mode from the TOC byte in each packet.
type Decoder struct {
	dec           *hybrid.Decoder
	sampleRate    int
	channels      int
	lastFrameSize int
}

// NewDecoder creates a new Opus decoder.
//
// sampleRate must be one of: 8000, 12000, 16000, 24000, 48000.
// channels must be 1 (mono) or 2 (stereo).
//
// Returns an error if the parameters are invalid.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrInvalidChannels
	}

	return &Decoder{
		dec:           hybrid.NewDecoder(channels),
		sampleRate:    sampleRate,
		channels:      channels,
		lastFrameSize: 960, // Default 20ms at 48kHz
	}, nil
}

// Decode decodes an Opus packet into float32 PCM samples.
//
// data: Opus packet data, or nil for Packet Loss Concealment (PLC).
// pcm: Output buffer for decoded samples. Must be large enough to hold
// frameSize * channels samples, where frameSize is determined from the packet TOC.
//
// Returns the number of samples per channel decoded, or an error.
//
// When data is nil, the decoder performs packet loss concealment using
// the last successfully decoded frame parameters.
//
// Buffer sizing: For 60ms frames at 48kHz stereo, pcm must have at least
// 2880 * 2 = 5760 elements.
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error) {
	// Determine frame size from TOC or use last frame size for PLC
	var frameSize int
	if data != nil && len(data) > 0 {
		toc := ParseTOC(data[0])
		frameSize = toc.FrameSize
	} else {
		frameSize = d.lastFrameSize
	}

	// Validate output buffer size
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Decode using internal hybrid decoder
	var samples []float32
	var err error

	if data != nil && len(data) > 0 {
		// Extract frame data (skip TOC byte)
		frameData := data[1:]

		if d.channels == 2 {
			samples, err = d.dec.DecodeStereoToFloat32(frameData, frameSize)
		} else {
			samples, err = d.dec.DecodeToFloat32(frameData, frameSize)
		}
	} else {
		// PLC: pass nil to internal decoder
		if d.channels == 2 {
			samples, err = d.dec.DecodeStereoToFloat32(nil, frameSize)
		} else {
			samples, err = d.dec.DecodeToFloat32(nil, frameSize)
		}
	}

	if err != nil {
		return 0, err
	}

	// Copy to output buffer
	copy(pcm, samples)

	// Store frame size for PLC
	d.lastFrameSize = frameSize

	return frameSize, nil
}

// DecodeInt16 decodes an Opus packet into int16 PCM samples.
//
// data: Opus packet data, or nil for PLC.
// pcm: Output buffer for decoded samples.
//
// Returns the number of samples per channel decoded, or an error.
//
// The samples are converted from float32 with proper clamping to [-32768, 32767].
func (d *Decoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	// Determine frame size from TOC or use last frame size for PLC
	var frameSize int
	if data != nil && len(data) > 0 {
		toc := ParseTOC(data[0])
		frameSize = toc.FrameSize
	} else {
		frameSize = d.lastFrameSize
	}

	// Validate output buffer size
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Decode to intermediate float32 buffer
	pcm32 := make([]float32, needed)
	n, err := d.Decode(data, pcm32)
	if err != nil {
		return 0, err
	}

	// Convert float32 -> int16 with clamping
	for i := 0; i < n*d.channels; i++ {
		scaled := pcm32[i] * 32767.0
		if scaled > 32767 {
			pcm[i] = 32767
		} else if scaled < -32768 {
			pcm[i] = -32768
		} else {
			pcm[i] = int16(scaled)
		}
	}

	return n, nil
}

// DecodeFloat32 decodes an Opus packet and returns a new float32 slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use Decode with a pre-allocated buffer.
//
// data: Opus packet data, or nil for PLC.
//
// Returns the decoded samples or an error.
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error) {
	// Determine frame size from TOC or use last frame size for PLC
	var frameSize int
	if data != nil && len(data) > 0 {
		toc := ParseTOC(data[0])
		frameSize = toc.FrameSize
	} else {
		frameSize = d.lastFrameSize
	}

	// Allocate buffer
	pcm := make([]float32, frameSize*d.channels)

	n, err := d.Decode(data, pcm)
	if err != nil {
		return nil, err
	}

	return pcm[:n*d.channels], nil
}

// DecodeInt16Slice decodes an Opus packet and returns a new int16 slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use DecodeInt16 with a pre-allocated buffer.
//
// data: Opus packet data, or nil for PLC.
//
// Returns the decoded samples or an error.
func (d *Decoder) DecodeInt16Slice(data []byte) ([]int16, error) {
	// Determine frame size from TOC or use last frame size for PLC
	var frameSize int
	if data != nil && len(data) > 0 {
		toc := ParseTOC(data[0])
		frameSize = toc.FrameSize
	} else {
		frameSize = d.lastFrameSize
	}

	// Allocate buffer
	pcm := make([]int16, frameSize*d.channels)

	n, err := d.DecodeInt16(data, pcm)
	if err != nil {
		return nil, err
	}

	return pcm[:n*d.channels], nil
}

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.dec.Reset()
	d.lastFrameSize = 960
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SampleRate returns the sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}
