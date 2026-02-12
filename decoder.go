// decoder.go implements the public Decoder API for Opus decoding.

package gopus

import (
	"math"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/hybrid"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

const (
	defaultMaxPacketSamples = 5760
	defaultMaxPacketBytes   = 1500
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
	silkDecoder        *silk.Decoder   // SILK-only mode decoder
	celtDecoder        *celt.Decoder   // CELT-only mode decoder
	hybridDecoder      *hybrid.Decoder // Hybrid mode decoder
	sampleRate         int
	channels           int
	maxPacketSamples   int
	maxPacketBytes     int
	scratchPCM         []float32
	scratchTransition  []float32
	scratchRedundant   []float32
	lastFrameSize      int
	lastPacketDuration int
	prevMode           Mode // Track last mode for PLC
	lastBandwidth      Bandwidth
	prevRedundancy     bool
	prevPacketStereo   bool
	haveDecoded        bool
	redundantRng       uint32 // Range from redundancy decoding, XORed with final range
	lastDataLen        int    // Length of last packet data
	mainDecodeRng      uint32 // Final range from main decode (before any redundancy processing)
	decodeGainQ8       int    // Output gain in Q8 dB (libopus OPUS_SET_GAIN semantics)

	// FEC (Forward Error Correction) state
	// Stores LBRR data from the current packet for use by the next packet's FEC decode.
	fecData       []byte    // Stored packet data containing LBRR for FEC recovery
	fecMode       Mode      // Mode of the packet containing LBRR
	fecBandwidth  Bandwidth // Bandwidth of the packet containing LBRR
	fecStereo     bool      // Whether the packet was stereo
	fecFrameSize  int       // Frame size of the packet containing LBRR
	fecFrameCount int       // Number of frames in packet
	hasFEC        bool      // True if fecData contains valid LBRR data
	scratchFEC    []float32 // Scratch buffer for FEC decode

	// Scratch range decoder to avoid per-frame heap allocations
	scratchRangeDecoder rangecoding.Decoder

	// Soft clipping memory (float decode uses none; int16 decode uses this)
	softClipMem [2]float32
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
		scratchTransition: make([]float32, transitionSamples*cfg.Channels),
		scratchRedundant:  make([]float32, transitionSamples*cfg.Channels),
		lastFrameSize:     960,        // Default 20ms at 48kHz
		prevMode:          ModeHybrid, // Default for PLC until first decode
		lastBandwidth:     BandwidthFullband,
		fecData:           make([]byte, maxPacketBytes),
		scratchFEC:        make([]float32, maxPacketSamples*cfg.Channels),
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
			chunk := min(remaining, 48000/50)
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
		d.applyOutputGain(pcm[:frameSize*d.channels])

		d.lastFrameSize = frameSize
		d.lastPacketDuration = frameSize
		d.lastDataLen = 0 // PLC: set len to 0 so FinalRange returns 0
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
			var frameLens [48]int
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, err
				}
				offset += bytesRead
				frameLens[i] = frameLen
			}
			frameDataOffset := offset
			for i := 0; i < m-1; i++ {
				frameLen := frameLens[i]
				if frameDataOffset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(data[frameDataOffset : frameDataOffset+frameLen]); err != nil {
					return 0, err
				}
				frameDataOffset += frameLen
			}
			lastFrameLen := len(data) - frameDataOffset - padding
			if lastFrameLen < 0 {
				return 0, ErrInvalidPacket
			}
			if frameDataOffset+lastFrameLen > len(data)-padding {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(data[frameDataOffset : frameDataOffset+lastFrameLen]); err != nil {
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
	d.lastPacketDuration = totalSamples
	d.lastBandwidth = toc.Bandwidth
	// Set the full packet length for FinalRange check (per libopus: len <= 1 means rangeFinal = 0)
	d.lastDataLen = len(data)

	// Store packet info for FEC recovery on next lost packet.
	// LBRR is only available in SILK and Hybrid modes.
	if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
		d.storeFECData(data, toc, frameCount, frameSize)
	} else {
		d.hasFEC = false
	}

	d.applyOutputGain(pcm[:totalSamples*d.channels])

	return totalSamples, nil
}

// DecodeWithFEC decodes an Opus packet, optionally recovering a lost frame using FEC.
//
// If fec is true and the previous packet contained LBRR redundancy data, the decoder
// will use that data to recover the lost frame instead of using PLC extrapolation.
// This produces better audio quality during packet loss.
//
// If fec is true but no LBRR data is available, the decoder falls back to standard PLC.
// If fec is false, this behaves identically to Decode().
//
// Parameters:
//   - data: Opus packet data, or nil to trigger FEC/PLC for a lost packet
//   - pcm: Output buffer for decoded samples
//   - fec: If true and data is nil, attempt FEC recovery before falling back to PLC
//
// Returns the number of samples per channel decoded, or an error.
//
// Usage pattern for handling packet loss:
//
//	// When packet N is lost:
//	// 1. First decode packet N+1's FEC data to recover N
//	samples, _ := decoder.DecodeWithFEC(packetN1, pcmN, true)
//	// 2. Then decode packet N+1 normally
//	samples, _ := decoder.Decode(packetN1, pcmN1)
//
// Note: FEC recovery uses LBRR (Low Bitrate Redundancy) data that is encoded
// at the encoder side when the encoder has FEC enabled. LBRR data is only
// available in SILK and Hybrid modes, not in CELT-only mode.
func (d *Decoder) DecodeWithFEC(data []byte, pcm []float32, fec bool) (int, error) {
	// If not requesting FEC or we have actual data with fec=false, use normal decode
	if !fec || (data != nil && len(data) > 0) {
		return d.Decode(data, pcm)
	}

	// FEC decode requested for a lost packet (data is nil)
	// Try to use stored LBRR data from the previous packet
	if d.hasFEC && len(d.fecData) > 0 {
		// Decode using LBRR data from previous packet
		n, err := d.decodeFECFrame(pcm)
		if err == nil {
			return n, nil
		}
		// If FEC decode fails, fall back to PLC
	}

	// No FEC available or FEC failed, fall back to standard PLC
	return d.Decode(nil, pcm)
}

// storeFECData stores the current packet's information for FEC recovery.
// This is called after successfully decoding a SILK or Hybrid packet.
func (d *Decoder) storeFECData(data []byte, toc TOC, frameCount, frameSize int) {
	// Copy packet data to FEC buffer. Keep backing storage to avoid churn when
	// packet sizes vary frame-to-frame.
	if cap(d.fecData) < len(data) {
		d.fecData = make([]byte, len(data))
	} else {
		d.fecData = d.fecData[:len(data)]
	}
	copy(d.fecData, data)

	d.fecMode = toc.Mode
	d.fecBandwidth = toc.Bandwidth
	d.fecStereo = toc.Stereo
	d.fecFrameSize = frameSize
	d.fecFrameCount = frameCount
	d.hasFEC = true
}

// decodeFECFrame decodes LBRR data from the stored FEC packet.
// This is used to recover a lost frame using forward error correction.
func (d *Decoder) decodeFECFrame(pcm []float32) (int, error) {
	if !d.hasFEC || len(d.fecData) == 0 {
		return 0, ErrNoFECData
	}

	frameSize := d.fecFrameSize
	if frameSize <= 0 {
		frameSize = d.lastFrameSize
	}
	if frameSize <= 0 {
		frameSize = 960
	}

	totalSamples := frameSize * d.fecFrameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Decode LBRR frames
	n, err := d.decodeLBRRFrames(pcm, frameSize)
	if err != nil {
		return 0, err
	}
	d.applyOutputGain(pcm[:n*d.channels])

	// Clear FEC data after use to prevent reuse
	d.hasFEC = false

	return n, nil
}

// decodeLBRRFrames decodes LBRR (FEC) data from the stored packet.
func (d *Decoder) decodeLBRRFrames(pcm []float32, frameSize int) (int, error) {
	// Use the SILK decoder's FEC decode capability
	// The LBRR data is embedded in the SILK bitstream and was already parsed
	// during normal decode. We need to re-decode the packet with FEC flag.

	switch d.fecMode {
	case ModeSILK:
		return d.decodeSILKFEC(pcm, frameSize)
	case ModeHybrid:
		return d.decodeHybridFEC(pcm, frameSize)
	default:
		// CELT-only mode doesn't have LBRR
		return 0, ErrNoFECData
	}
}

// decodeSILKFEC decodes SILK LBRR data for FEC recovery.
func (d *Decoder) decodeSILKFEC(pcm []float32, frameSize int) (int, error) {
	silkBW, ok := silk.BandwidthFromOpus(int(d.fecBandwidth))
	if !ok {
		silkBW = silk.BandwidthWideband
	}

	// Decode FEC frames using SILK decoder's LBRR support
	fecSamples, err := d.silkDecoder.DecodeFEC(d.fecData, silkBW, frameSize, d.fecStereo, d.channels)
	if err != nil {
		return 0, err
	}

	// Copy to output buffer
	needed := len(fecSamples)
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	copy(pcm[:needed], fecSamples)

	d.lastFrameSize = frameSize
	d.haveDecoded = true

	return frameSize, nil
}

// decodeHybridFEC decodes Hybrid mode LBRR data for FEC recovery.
func (d *Decoder) decodeHybridFEC(pcm []float32, frameSize int) (int, error) {
	// For Hybrid mode, we decode the SILK LBRR and add CELT contribution
	// The LBRR is in the SILK part of the bitstream

	silkBW, ok := silk.BandwidthFromOpus(int(d.fecBandwidth))
	if !ok {
		silkBW = silk.BandwidthWideband
	}

	// Decode SILK FEC
	fecSamples, err := d.silkDecoder.DecodeFEC(d.fecData, silkBW, frameSize, d.fecStereo, d.channels)
	if err != nil {
		return 0, err
	}

	// Copy to output buffer
	needed := len(fecSamples)
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	copy(pcm[:needed], fecSamples)

	d.lastFrameSize = frameSize
	d.haveDecoded = true

	return frameSize, nil
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
		opusPCMSoftClip(d.scratchPCM[:n*d.channels], n, d.channels, d.softClipMem[:])
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
	opusPCMSoftClip(d.scratchPCM[:n*d.channels], n, d.channels, d.softClipMem[:])
	for i := 0; i < n*d.channels; i++ {
		pcm[i] = float32ToInt16(d.scratchPCM[i])
	}
	return n, nil
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

func decodeGainLinear(gainQ8 int) float32 {
	return float32(math.Pow(10.0, float64(gainQ8)/(20.0*256.0)))
}

func (d *Decoder) applyOutputGain(samples []float32) {
	if d.decodeGainQ8 == 0 || len(samples) == 0 {
		return
	}
	g := decodeGainLinear(d.decodeGainQ8)
	for i := range samples {
		samples[i] *= g
	}
}

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()
	d.hybridDecoder.Reset()
	d.lastFrameSize = 960
	d.lastPacketDuration = 0
	d.prevMode = ModeHybrid
	d.lastBandwidth = BandwidthFullband
	d.prevRedundancy = false
	d.prevPacketStereo = false
	d.haveDecoded = false
	d.softClipMem[0] = 0
	d.softClipMem[1] = 0

	// Clear FEC state
	d.hasFEC = false
	d.fecFrameSize = 0
	d.fecFrameCount = 0
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

// DebugPrevMode returns the previous decode mode (SILK/Hybrid/CELT).
// This is intended for testing/debugging parity with libopus.
func (d *Decoder) DebugPrevMode() Mode {
	return d.prevMode
}

// DebugPrevRedundancy reports whether the previous frame used CELT redundancy.
// This is intended for testing/debugging parity with libopus.
func (d *Decoder) DebugPrevRedundancy() bool {
	return d.prevRedundancy
}

// DebugPrevPacketStereo returns the last packet's stereo flag.
// This is intended for testing/debugging parity with libopus.
func (d *Decoder) DebugPrevPacketStereo() bool {
	return d.prevPacketStereo
}

// SetGain sets output gain in Q8 dB units (libopus OPUS_SET_GAIN semantics).
//
// Valid range is [-32768, 32767], where 256 = +1 dB and -256 = -1 dB.
func (d *Decoder) SetGain(gainQ8 int) error {
	if gainQ8 < -32768 || gainQ8 > 32767 {
		return ErrInvalidGain
	}
	d.decodeGainQ8 = gainQ8
	return nil
}

// Gain returns the current decoder output gain in Q8 dB units.
func (d *Decoder) Gain() int {
	return d.decodeGainQ8
}

// Pitch returns the most recent CELT postfilter pitch period.
//
// This mirrors OPUS_GET_PITCH behavior for decoded CELT/hybrid content.
// Returns 0 when no pitch information is available.
func (d *Decoder) Pitch() int {
	if d.celtDecoder == nil {
		return 0
	}
	return d.celtDecoder.PostfilterPeriod()
}

// Bandwidth returns the bandwidth of the last successfully decoded packet.
func (d *Decoder) Bandwidth() Bandwidth {
	return d.lastBandwidth
}

// LastPacketDuration returns the duration (in samples per channel at 48kHz scale)
// of the last decoded packet.
func (d *Decoder) LastPacketDuration() int {
	if d.lastPacketDuration > 0 {
		return d.lastPacketDuration
	}
	return d.lastFrameSize
}

// InDTX reports whether the most recently decoded packet was a DTX packet.
func (d *Decoder) InDTX() bool {
	return d.lastDataLen > 0 && d.lastDataLen <= 2
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after Decode() to get a meaningful value.
//
// Per libopus, the final range is XORed with any redundancy frame's range.
// If the packet length was <= 1, FinalRange returns 0.
func (d *Decoder) FinalRange() uint32 {
	// Per libopus: if len <= 1, rangeFinal = 0
	if d.lastDataLen <= 1 {
		return 0
	}

	// Use the captured main decode range (not the current decoder state,
	// which may have been modified by redundancy decoding)
	return d.mainDecodeRng ^ d.redundantRng
}
