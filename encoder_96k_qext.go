//go:build gopus_qext

package gopus

// encoderHD96kFields holds the 96 kHz API-rate flag for the Encoder.
// When set, public Encode methods accept 2x the internal 48 kHz frame size
// and downsample 2:1 before encoding.
//
// C ref: opus_encoder.c opus_encoder_init() ENABLE_QEXT gate (Fs != 96000).
// At 96 kHz, libopus uses a native 96 kHz CELT mode (1920-sample frames).
// gopus's CELT pipeline is 48 kHz-only; we decimate 2:1 at the public API
// boundary and encode the 48 kHz signal. SILK/Hybrid modes are not supported
// at 96 kHz (no 8/12 kHz resampler path in libopus at Fs=96000).
type encoderHD96kFields struct {
	apiIs96kHz  bool
	scratch96k  []float32 // downsampled 48 kHz scratch for 96 kHz input path
}

func (e *Encoder) is96kHz() bool { return e.apiIs96kHz }

// apiFrameSize returns the frame size in API-rate samples.
// At 96 kHz, this is 2 * e.frameSize (the 48 kHz internal frame size).
func (e *Encoder) apiFrameSize() int {
	if e.apiIs96kHz {
		return int(e.frameSize) * 2
	}
	return int(e.frameSize)
}

// checkAndDownsample96k validates a 96 kHz PCM buffer and downsamples 2:1.
// Returns the downsampled 48 kHz buffer, the 48 kHz frame size, or an error.
//
// Downsampling uses simple decimation (every other pair of samples averaged)
// which is the simplest correct approach for this intermediate layer.
// C ref: at 96 kHz libopus uses OPUS_COPY (no resampler) in the encoder path
// since the CELT codec runs natively at 96 kHz; we use 2:1 averaging here.
func (e *Encoder) checkAndDownsample96k(pcm []float32) ([]float32, int, error) {
	channels := int(e.channels)
	frameSize48 := int(e.frameSize)
	expected96 := frameSize48 * 2 * channels
	if len(pcm) != expected96 {
		return nil, 0, ErrInvalidFrameSize
	}

	needed48 := frameSize48 * channels
	if cap(e.scratch96k) < needed48 {
		e.scratch96k = make([]float32, needed48)
	}
	dst := e.scratch96k[:needed48]

	// Decimate 2:1: average each pair of input samples per channel.
	// This matches a simple anti-aliased 2:1 downsample.
	for i := 0; i < frameSize48; i++ {
		for c := 0; c < channels; c++ {
			a := pcm[(2*i)*channels+c]
			b := pcm[(2*i+1)*channels+c]
			dst[i*channels+c] = (a + b) * 0.5
		}
	}
	return dst, frameSize48, nil
}

// init96kEncoder initialises the 96 kHz API flag on a newly-created Encoder.
func init96kEncoder(e *Encoder) {
	e.apiIs96kHz = true
}
