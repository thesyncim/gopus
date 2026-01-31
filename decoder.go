// decoder.go implements the public Decoder API for Opus decoding.

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/hybrid"
	"github.com/thesyncim/gopus/internal/silk"
)

const (
	defaultMaxPacketSamples = 5760
	defaultMaxPacketBytes   = 1275
)

// DecoderConfig configures a Decoder instance.
type DecoderConfig struct {
	// SampleRate must be one of: 8000, 12000, 16000, 24000, 48000.
	SampleRate int
	// Channels must be 1 (mono) or 2 (stereo).
	Channels int
	// MaxPacketSamples caps the maximum decoded samples per channel per packet.
	// If zero, defaultMaxPacketSamples is used.
	MaxPacketSamples int
	// MaxPacketBytes caps the maximum Opus packet size in bytes.
	// If zero, defaultMaxPacketBytes is used.
	MaxPacketBytes int
}

// DefaultDecoderConfig returns a config with default caps for the given stream format.
func DefaultDecoderConfig(sampleRate, channels int) DecoderConfig {
	return DecoderConfig{
		SampleRate:       sampleRate,
		Channels:         channels,
		MaxPacketSamples: defaultMaxPacketSamples,
		MaxPacketBytes:   defaultMaxPacketBytes,
	}
}

// Decoder decodes Opus packets into PCM audio samples.
//
// A Decoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own Decoder instance.
//
// The decoder supports all Opus modes (SILK, Hybrid, CELT) and automatically
// detects the mode from the TOC byte in each packet.
type Decoder struct {
	silkDecoder       *silk.Decoder   // SILK-only mode decoder
	celtDecoder       *celt.Decoder   // CELT-only mode decoder
	hybridDecoder     *hybrid.Decoder // Hybrid mode decoder
	sampleRate        int
	channels          int
	maxPacketSamples  int
	maxPacketBytes    int
	scratchPCM        []float32
	scratchInt16      []int16
	scratchTransition []float32
	scratchRedundant  []float32
	lastFrameSize     int
	prevMode          Mode // Track last mode for PLC
	lastBandwidth     Bandwidth
	prevRedundancy    bool
	prevPacketStereo  bool
	haveDecoded       bool
}

// NewDecoder creates a new Opus decoder.
func NewDecoder(cfg DecoderConfig) (*Decoder, error) {
	if !validSampleRate(cfg.SampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if cfg.Channels < 1 || cfg.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	maxPacketSamples := cfg.MaxPacketSamples
	if maxPacketSamples == 0 {
		maxPacketSamples = defaultMaxPacketSamples
	}
	if maxPacketSamples < 1 {
		return nil, ErrInvalidMaxPacketSamples
	}

	maxPacketBytes := cfg.MaxPacketBytes
	if maxPacketBytes == 0 {
		maxPacketBytes = defaultMaxPacketBytes
	}
	if maxPacketBytes < 1 {
		return nil, ErrInvalidMaxPacketBytes
	}

	silkDec := silk.NewDecoder()
	celtDec := celt.NewDecoder(cfg.Channels)
	hybridDec := hybrid.NewDecoderWithSharedDecoders(cfg.Channels, silkDec, celtDec)

	transitionSamples := 48000 / 200 // 5ms at 48kHz

	return &Decoder{
		silkDecoder:       silkDec,
		celtDecoder:       celtDec,
		hybridDecoder:     hybridDec,
		sampleRate:        cfg.SampleRate,
		channels:          cfg.Channels,
		maxPacketSamples:  maxPacketSamples,
		maxPacketBytes:    maxPacketBytes,
		scratchPCM:        make([]float32, maxPacketSamples*cfg.Channels),
		scratchInt16:      make([]int16, maxPacketSamples*cfg.Channels),
		scratchTransition: make([]float32, transitionSamples*cfg.Channels),
		scratchRedundant:  make([]float32, transitionSamples*cfg.Channels),
		lastFrameSize:     960,        // Default 20ms at 48kHz
		prevMode:          ModeHybrid, // Default for PLC until first decode
		lastBandwidth:     BandwidthFullband,
	}, nil
}

// NewDecoderDefault creates a decoder using default caps for the given stream format.
func NewDecoderDefault(sampleRate, channels int) (*Decoder, error) {
	return NewDecoder(DefaultDecoderConfig(sampleRate, channels))
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
	if data == nil || len(data) == 0 {
		frameSize := d.lastFrameSize
		if frameSize <= 0 {
			frameSize = 960
		}
		if frameSize > d.maxPacketSamples {
			return 0, ErrPacketTooLarge
		}
		needed := frameSize * d.channels
		if len(pcm) < needed {
			return 0, ErrBufferTooSmall
		}

		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := minInt(remaining, 48000/50)
			n, err := d.decodeOpusFrameInto(pcm[offset*d.channels:], nil, chunk, d.lastFrameSize, d.prevMode, d.lastBandwidth, d.prevPacketStereo)
			if err != nil {
				return 0, err
			}
			if n == 0 {
				break
			}
			offset += n
			remaining -= n
		}

		d.lastFrameSize = frameSize
		return frameSize, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	toc, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	frameSize := toc.FrameSize
	totalSamples := frameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	offsetSamples := 0
	decodeFrame := func(frameData []byte) error {
		n, err := d.decodeOpusFrameInto(pcm[offsetSamples*d.channels:], frameData, frameSize, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
		if err != nil {
			return err
		}
		offsetSamples += n
		d.prevPacketStereo = toc.Stereo
		return nil
	}

	switch toc.FrameCode {
	case 0:
		if err := decodeFrame(data[1:]); err != nil {
			return 0, err
		}
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return 0, ErrInvalidPacket
		}
		frameLen := frameDataLen / 2
		offset := 1
		for i := 0; i < 2; i++ {
			if offset+frameLen > len(data) {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(data[offset : offset+frameLen]); err != nil {
				return 0, err
			}
			offset += frameLen
		}
	case 2:
		if len(data) < 2 {
			return 0, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return 0, err
		}
		headerLen := 1 + bytesRead
		frame2Len := len(data) - headerLen - frame1Len
		if frame2Len < 0 {
			return 0, ErrInvalidPacket
		}
		if headerLen+frame1Len > len(data) {
			return 0, ErrInvalidPacket
		}
		if err := decodeFrame(data[headerLen : headerLen+frame1Len]); err != nil {
			return 0, err
		}
		offset := headerLen + frame1Len
		if offset+frame2Len > len(data) {
			return 0, ErrInvalidPacket
		}
		if err := decodeFrame(data[offset : offset+frame2Len]); err != nil {
			return 0, err
		}
	case 3:
		if len(data) < 2 {
			return 0, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)
		if m == 0 || m > 48 {
			return 0, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0

		if hasPadding {
			for {
				if offset >= len(data) {
					return 0, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
				}
				if padByte < 255 {
					break
				}
			}
		}

		if vbr {
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, err
				}
				offset += bytesRead
				if offset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(data[offset : offset+frameLen]); err != nil {
					return 0, err
				}
				offset += frameLen
			}
			lastFrameLen := len(data) - offset - padding
			if lastFrameLen < 0 {
				return 0, ErrInvalidPacket
			}
			if offset+lastFrameLen > len(data) {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(data[offset : offset+lastFrameLen]); err != nil {
				return 0, err
			}
		} else {
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return 0, ErrInvalidPacket
			}
			if frameDataLen%m != 0 {
				return 0, ErrInvalidPacket
			}
			frameLen := frameDataLen / m
			for i := 0; i < m; i++ {
				if offset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(data[offset : offset+frameLen]); err != nil {
					return 0, err
				}
				offset += frameLen
			}
		}
	}

	d.lastFrameSize = frameSize
	d.lastBandwidth = toc.Bandwidth

	return totalSamples, nil
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
	if data == nil || len(data) == 0 {
		frameSize := d.lastFrameSize
		if frameSize <= 0 {
			frameSize = 960
		}
		if frameSize > d.maxPacketSamples {
			return 0, ErrPacketTooLarge
		}
		needed := frameSize * d.channels
		if len(pcm) < needed {
			return 0, ErrBufferTooSmall
		}

		n, err := d.Decode(data, d.scratchPCM)
		if err != nil {
			return 0, err
		}
		for i := 0; i < n*d.channels; i++ {
			pcm[i] = float32ToInt16(d.scratchPCM[i])
		}
		return n, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	toc, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	totalSamples := toc.FrameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}
	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	n, err := d.Decode(data, d.scratchPCM)
	if err != nil {
		return 0, err
	}
	for i := 0; i < n*d.channels; i++ {
		pcm[i] = float32ToInt16(d.scratchPCM[i])
	}
	return n, nil
}

// DecodeFloat32 decodes an Opus packet and returns a slice backed by internal scratch.
// The returned slice is only valid until the next decode call on this Decoder.
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error) {
	n, err := d.Decode(data, d.scratchPCM)
	if err != nil {
		return nil, err
	}
	return d.scratchPCM[:n*d.channels], nil
}

// DecodeInt16Slice decodes an Opus packet and returns a slice backed by internal scratch.
// The returned slice is only valid until the next decode call on this Decoder.
func (d *Decoder) DecodeInt16Slice(data []byte) ([]int16, error) {
	n, err := d.DecodeInt16(data, d.scratchInt16)
	if err != nil {
		return nil, err
	}
	return d.scratchInt16[:n*d.channels], nil
}

func packetFrameCount(data []byte) (TOC, int, error) {
	if len(data) < 1 {
		return TOC{}, 0, ErrPacketTooShort
	}
	toc := ParseTOC(data[0])
	switch toc.FrameCode {
	case 0:
		return toc, 1, nil
	case 1, 2:
		return toc, 2, nil
	case 3:
		if len(data) < 2 {
			return TOC{}, 0, ErrPacketTooShort
		}
		m := int(data[1] & 0x3F)
		if m == 0 || m > 48 {
			return TOC{}, 0, ErrInvalidFrameCount
		}
		return toc, m, nil
	default:
		return TOC{}, 0, ErrInvalidPacket
	}
}

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()
	d.hybridDecoder.Reset()
	d.lastFrameSize = 960
	d.prevMode = ModeHybrid
	d.lastBandwidth = BandwidthFullband
	d.prevRedundancy = false
	d.prevPacketStereo = false
	d.haveDecoded = false
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SampleRate returns the sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}

// GetCELTDecoder returns the internal CELT decoder for debugging purposes.
// This allows access to internal state like preemph_state and overlap_buffer.
func (d *Decoder) GetCELTDecoder() *celt.Decoder {
	return d.celtDecoder
}

// GetSILKDecoder returns the internal SILK decoder for debugging purposes.
// This allows access to internal state like resampler state and sMid buffer.
func (d *Decoder) GetSILKDecoder() *silk.Decoder {
	return d.silkDecoder
}
