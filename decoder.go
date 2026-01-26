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
// frameSize * frameCount * channels samples, where frameSize and frameCount
// are determined from the packet TOC and frame code.
//
// Returns the number of samples per channel decoded, or an error.
//
// When data is nil, the decoder performs packet loss concealment using
// the last successfully decoded frame parameters.
//
// Buffer sizing: For 60ms frames at 48kHz stereo, pcm must have at least
// 2880 * 2 = 5760 elements. For multi-frame packets (code 1/2/3), the buffer
// must be large enough for all frames combined.
//
// Multi-frame packets (RFC 6716 Section 3.2):
//   - Code 0: 1 frame (most common)
//   - Code 1: 2 equal-sized frames
//   - Code 2: 2 different-sized frames
//   - Code 3: Arbitrary number of frames (1-48)
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

	// For PLC, decode a single frame
	if data == nil || len(data) == 0 {
		needed := frameSize * d.channels
		if len(pcm) < needed {
			return 0, ErrBufferTooSmall
		}

		samples, err := d.decodeSingleFrame(nil, toc, mode, frameSize)
		if err != nil {
			return 0, err
		}

		copy(pcm, samples)
		d.lastFrameSize = frameSize
		d.lastMode = mode
		return frameSize, nil
	}

	// Parse packet to extract all frame data per RFC 6716 Section 3.2
	// This handles all frame codes: 0 (1 frame), 1 (2 equal), 2 (2 different), 3 (M frames)
	pktInfo, err := ParsePacket(data)
	if err != nil {
		return 0, err
	}

	frameCount := pktInfo.FrameCount
	totalSamples := frameSize * frameCount

	// Validate output buffer size for all frames
	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Extract frame data from packet
	frameDataSlices := extractFrameData(data, pktInfo)

	// Decode each frame and concatenate output
	var allSamples []float32
	for i := 0; i < frameCount; i++ {
		frameData := frameDataSlices[i]

		samples, err := d.decodeSingleFrame(frameData, toc, mode, frameSize)
		if err != nil {
			return 0, err
		}

		allSamples = append(allSamples, samples...)
	}

	copy(pcm, allSamples)
	d.lastFrameSize = frameSize
	d.lastMode = mode

	return totalSamples, nil
}

// decodeSingleFrame decodes a single frame of the given mode.
// frameData is the raw frame bytes (without TOC or length headers).
func (d *Decoder) decodeSingleFrame(frameData []byte, toc TOC, mode Mode, frameSize int) ([]float32, error) {
	switch mode {
	case ModeSILK:
		return d.decodeSILK(frameData, toc, frameSize)
	case ModeCELT:
		return d.decodeCELT(frameData, toc, frameSize)
	case ModeHybrid:
		return d.decodeHybrid(frameData, toc, frameSize)
	default:
		return nil, ErrInvalidMode
	}
}

// extractFrameData extracts individual frame data slices from a packet.
// Returns a slice of byte slices, one per frame.
//
// This function calculates the header size and then extracts frame data
// based on the frame sizes determined by ParsePacket.
func extractFrameData(data []byte, info PacketInfo) [][]byte {
	frames := make([][]byte, info.FrameCount)

	// Calculate the total size of all frames
	var totalFrameBytes int
	for _, size := range info.FrameSizes {
		totalFrameBytes += size
	}

	// The frame data starts at: packet_size - padding - total_frame_bytes
	// This works for all frame codes because ParsePacket already validated the structure
	frameDataStart := len(data) - info.Padding - totalFrameBytes

	// Safety check: ensure we have valid bounds
	if frameDataStart < 1 {
		// At minimum, skip TOC byte (first byte)
		frameDataStart = 1
	}

	// Calculate usable data end (excluding padding)
	dataEnd := len(data) - info.Padding
	if dataEnd < frameDataStart {
		dataEnd = frameDataStart
	}

	// Extract each frame
	offset := frameDataStart
	for i := 0; i < info.FrameCount; i++ {
		frameLen := info.FrameSizes[i]
		endOffset := offset + frameLen

		// Clamp to available data
		if endOffset > dataEnd {
			endOffset = dataEnd
		}
		if offset >= dataEnd {
			// No data available for this frame
			frames[i] = nil
		} else {
			frames[i] = data[offset:endOffset]
		}
		offset = endOffset
	}

	return frames
}

// DecodeInt16 decodes an Opus packet into int16 PCM samples.
//
// data: Opus packet data, or nil for PLC.
// pcm: Output buffer for decoded samples. Must be large enough to hold all frames
// in multi-frame packets (frameSize * frameCount * channels samples).
//
// Returns the number of samples per channel decoded, or an error.
//
// The samples are converted from float32 with proper clamping to [-32768, 32767].
func (d *Decoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	// Determine total samples needed from TOC or use last frame size for PLC
	totalSamples, err := d.getTotalSamples(data)
	if err != nil {
		return 0, err
	}

	// Validate output buffer size
	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Decode to intermediate float32 buffer
	pcm32 := make([]float32, needed)
	n, err := d.Decode(data, pcm32)
	if err != nil {
		return 0, err
	}

	// Convert float32 -> int16 with libopus-compatible rounding
	for i := 0; i < n*d.channels; i++ {
		pcm[i] = float32ToInt16(pcm32[i])
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
	// Determine total samples needed from TOC or use last frame size for PLC
	totalSamples, err := d.getTotalSamples(data)
	if err != nil {
		return nil, err
	}

	// Allocate buffer
	pcm := make([]float32, totalSamples*d.channels)

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
	// Determine total samples needed from TOC or use last frame size for PLC
	totalSamples, err := d.getTotalSamples(data)
	if err != nil {
		return nil, err
	}

	// Allocate buffer
	pcm := make([]int16, totalSamples*d.channels)

	n, err := d.DecodeInt16(data, pcm)
	if err != nil {
		return nil, err
	}

	return pcm[:n*d.channels], nil
}

// getTotalSamples calculates the total samples per channel for a packet,
// accounting for multi-frame packets.
func (d *Decoder) getTotalSamples(data []byte) (int, error) {
	if data == nil || len(data) == 0 {
		// PLC: use last frame size
		return d.lastFrameSize, nil
	}

	toc := ParseTOC(data[0])
	frameSize := toc.FrameSize

	// Parse packet to get frame count for multi-frame packets
	pktInfo, err := ParsePacket(data)
	if err != nil {
		return 0, err
	}

	return frameSize * pktInfo.FrameCount, nil
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
// It passes the packet's stereo flag to handle mono/stereo conversion.
func (d *Decoder) decodeCELT(data []byte, toc TOC, frameSize int) ([]float32, error) {
	if data != nil {
		d.celtDecoder.SetBandwidth(celt.BandwidthFromOpusConfig(int(toc.Bandwidth)))
	}
	// Use DecodeFrameWithPacketStereo to handle mono/stereo packet vs decoder mismatch
	samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(data, frameSize, toc.Stereo)
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
func (d *Decoder) decodeHybrid(data []byte, toc TOC, frameSize int) ([]float32, error) {
	if data != nil {
		d.hybridDecoder.SetBandwidth(celt.BandwidthFromOpusConfig(int(toc.Bandwidth)))
	}
	if d.channels == 2 {
		return d.hybridDecoder.DecodeStereoToFloat32(data, frameSize)
	}
	return d.hybridDecoder.DecodeToFloat32(data, frameSize)
}
