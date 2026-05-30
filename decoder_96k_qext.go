//go:build gopus_qext

package gopus

// decoderHD96kFields holds the 96 kHz API-rate flag for the Decoder.
// When set, the public decode methods downsample the requested frame size
// to 48 kHz internally and upsample the output back to 96 kHz.
//
// C ref: opus_decoder.c opus_decoder_init() ENABLE_QEXT gate (Fs != 96000).
// The internal CELT layer in libopus runs a native 96 kHz mode when Fs=96000.
// This path is the 2x linear-interpolation upsample wrapper over the 48 kHz
// core. The native-mode decode scaffolding lives in
// celt/decoder_hd96k_qext.go (HD96kSynthesizeMono, overlap=240, N=3840) and is
// validated against the native 96 kHz QEXT decode oracle
// (internal/libopustest/qext_decode96k_oracle.go). Routing Fs=96000 to the
// native HD96k CELT decode driver is increment 2b.
type decoderHD96kFields struct {
	apiIs96kHz bool
	scratch96k []float32 // 48 kHz scratch buffer for 96 kHz output path
}

func (d *Decoder) is96kHz() bool { return d.apiIs96kHz }

// decode96kFloat32 decodes an Opus packet into a 96 kHz output buffer.
// It decodes at 48 kHz internally and upsamples 2:1 using linear interpolation.
//
// State management note: d.lastFrameSize is kept in 48 kHz samples throughout
// (as set by the internal decodeFloat32 call).  This is required so that
// subsequent PLC calls via decode96kFloat32(nil, ...) can pass the correct
// 48 kHz frame size to decodeFloat32.  d.lastPacketDuration is updated to the
// 96 kHz sample count after each successful decode.
func (d *Decoder) decode96kFloat32(data []byte, pcm []float32) (int, error) {
	channels := int(d.channels)

	// Save/restore sampleRate around the 48 kHz internal decode.
	d.sampleRate = 48000
	defer func() { d.sampleRate = 96000 }()

	// Determine the 48 kHz frame size needed for the scratch buffer.
	// For real packets, derive from the TOC.
	// For PLC (data==nil), use the last stored 48 kHz frame size.
	var frameSize48 int
	if data != nil && len(data) > 0 {
		frameSize48 = packetTOCSamplesPerFrameAtRate(data[0], 48000)
		nbFrames := 1
		if data[0]&3 == 1 || data[0]&3 == 2 {
			nbFrames = 2
		} else if data[0]&3 == 3 && len(data) >= 2 {
			nbFrames = int(data[1] & 0x3F)
		}
		frameSize48 *= nbFrames
	} else {
		// PLC: d.lastFrameSize was set by the previous real-packet decode
		// via decodeFloat32 (48 kHz units), so use it directly.
		frameSize48 = int(d.lastFrameSize)
		if frameSize48 <= 0 {
			frameSize48 = 48000 / 50 // default 20ms at 48 kHz
		}
	}

	needed48 := frameSize48 * channels

	// Ensure scratch buffer is large enough.
	if cap(d.scratch96k) < needed48 {
		d.scratch96k = make([]float32, needed48)
	}
	scratch := d.scratch96k[:needed48]

	// Decode at 48 kHz.  d.sampleRate is 48000 during this call.
	n48, err := d.decodeFloat32(data, scratch, true)
	if err != nil {
		return 0, err
	}

	// n48 is the number of samples per channel at 48 kHz.
	// Output n96 = 2*n48 samples per channel at 96 kHz.
	n96 := n48 * 2
	needed96 := n96 * channels
	if len(pcm) < needed96 {
		return 0, ErrBufferTooSmall
	}

	// Linear interpolation upsample 2:1.
	// out[2i]   = in[i]
	// out[2i+1] = (in[i] + in[i+1]) / 2   (last sample: hold)
	//
	// C ref: celt/celt_decoder.c deemphasis() with downsample=1 for native 96
	// kHz mode.  gopus uses linear interpolation as an approximation since it
	// lacks the native 96 kHz MDCT mode.
	for c := 0; c < channels; c++ {
		for i := 0; i < n48; i++ {
			src := scratch[i*channels+c]
			var next float32
			if i+1 < n48 {
				next = scratch[(i+1)*channels+c]
			} else {
				next = src
			}
			pcm[(2*i)*channels+c] = src
			pcm[(2*i+1)*channels+c] = (src + next) * 0.5
		}
	}

	// d.lastFrameSize remains in 48 kHz units as set by decodeFloat32 (960 for
	// 20ms).  Update lastPacketDuration to the 96 kHz total sample count.
	d.lastPacketDuration = int32(n96)

	return n96, nil
}

// decodeInt1696k decodes at 96 kHz into int16, routing through decode96kFloat32.
func (d *Decoder) decodeInt1696k(data []byte, pcm []int16) (int, error) {
	channels := int(d.channels)
	needed := len(pcm)
	scratch := make([]float32, needed)
	n, err := d.decode96kFloat32(data, scratch)
	if err != nil {
		return 0, err
	}
	// Use soft-clip + int16 conversion for the 96 kHz output.
	softClipAndFloat32ToInt16(pcm[:n*channels], scratch[:n*channels], n, channels, d.softClipMem[:])
	return n, nil
}

// decodeInt2496k decodes at 96 kHz into int32 (24-bit), routing through decode96kFloat32.
func (d *Decoder) decodeInt2496k(data []byte, pcm []int32) (int, error) {
	channels := int(d.channels)
	needed := len(pcm)
	scratch := make([]float32, needed)
	n, err := d.decode96kFloat32(data, scratch)
	if err != nil {
		return 0, err
	}
	float32ToInt24Slice(pcm[:n*channels], scratch[:n*channels], n, channels)
	return n, nil
}

// init96kDecoder initialises a new Decoder for 96 kHz API output.
// Called from NewDecoder under gopus_qext when cfg.SampleRate == 96000.
func init96kDecoder(d *Decoder) {
	d.apiIs96kHz = true
	// Override the stored API rate back to 96000; sub-decoders stay at 48000.
	d.sampleRate = 96000
	// lastFrameSize at 96 kHz default: 20ms = 960 at 48 kHz (internal).
	// Reset() sets d.lastFrameSize = d.sampleRate/50 = 96000/50 = 1920, but
	// the internal decodeFloat32 uses 48 kHz, so we keep it at 960.
	d.lastFrameSize = 48000 / 50
}
