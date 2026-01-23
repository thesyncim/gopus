// decoder.go implements the public Decoder API for Opus decoding.

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/hybrid"
	"github.com/thesyncim/gopus/internal/silk"
)

// Decoder decodes Opus packets into PCM audio samples.
//
// A Decoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own Decoder instance.
//
// The decoder supports all Opus modes (SILK, Hybrid, CELT) and automatically
// detects the mode from the TOC byte in each packet.
type Decoder struct {
	silkDecoder   *silk.Decoder   // SILK-only mode decoder
	celtDecoder   *celt.Decoder   // CELT-only mode decoder
	hybridDecoder *hybrid.Decoder // Hybrid mode decoder
	sampleRate    int
	channels      int
	lastFrameSize int
	lastMode      Mode // Track last mode for PLC
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
		silkDecoder:   silk.NewDecoder(),
		celtDecoder:   celt.NewDecoder(channels),
		hybridDecoder: hybrid.NewDecoder(channels),
		sampleRate:    sampleRate,
		channels:      channels,
		lastFrameSize: 960,        // Default 20ms at 48kHz
		lastMode:      ModeHybrid, // Default for PLC
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
	var toc TOC
	var frameSize int
	var mode Mode

	if data != nil && len(data) > 0 {
		toc = ParseTOC(data[0])
		frameSize = toc.FrameSize
		mode = toc.Mode
	} else {
		// PLC: use last frame parameters
		frameSize = d.lastFrameSize
		mode = d.lastMode
	}

	// Validate output buffer size
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	var samples []float32
	var err error

	// Extract frame data (skip TOC byte) for normal decode
	var frameData []byte
	if data != nil && len(data) > 0 {
		frameData = data[1:]
	}

	// Route based on mode
	switch mode {
	case ModeSILK:
		samples, err = d.decodeSILK(frameData, toc, frameSize)
	case ModeCELT:
		samples, err = d.decodeCELT(frameData, frameSize)
	case ModeHybrid:
		samples, err = d.decodeHybrid(frameData, frameSize)
	default:
		return 0, ErrInvalidMode
	}

	if err != nil {
		return 0, err
	}

	copy(pcm, samples)
	d.lastFrameSize = frameSize
	d.lastMode = mode

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
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()
	d.hybridDecoder.Reset()
	d.lastFrameSize = 960
	d.lastMode = ModeHybrid
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SampleRate returns the sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}

// decodeSILK routes to SILK decoder for SILK-only mode packets.
func (d *Decoder) decodeSILK(data []byte, toc TOC, frameSize int) ([]float32, error) {
	// Map TOC bandwidth to SILK bandwidth
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		return nil, ErrInvalidBandwidth
	}

	if d.channels == 2 {
		return d.silkDecoder.DecodeStereo(data, silkBW, frameSize, true)
	}
	return d.silkDecoder.Decode(data, silkBW, frameSize, true)
}

// decodeCELT routes to CELT decoder for CELT-only mode packets.
func (d *Decoder) decodeCELT(data []byte, frameSize int) ([]float32, error) {
	samples, err := d.celtDecoder.DecodeFrame(data, frameSize)
	if err != nil {
		return nil, err
	}
	// Convert float64 to float32
	result := make([]float32, len(samples))
	for i, s := range samples {
		result[i] = float32(s)
	}
	return result, nil
}

// decodeHybrid routes to Hybrid decoder for Hybrid mode packets.
func (d *Decoder) decodeHybrid(data []byte, frameSize int) ([]float32, error) {
	if d.channels == 2 {
		return d.hybridDecoder.DecodeStereoToFloat32(data, frameSize)
	}
	return d.hybridDecoder.DecodeToFloat32(data, frameSize)
}
