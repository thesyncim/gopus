//go:build gopus_qext

package gopus

// encoderHD96kFields holds the 96 kHz API-rate flag for the Encoder.
// Public Encode methods accept 2x the internal 48 kHz frame size. In a QEXT
// build, supported frame sizes use native 96 kHz CELT; the compatibility
// resampler handles the remaining application-limited path.
//
// In ENABLE_QEXT builds, libopus uses native 96 kHz CELT modes for 2.5/5/10/20
// ms frames (240/480/960/1920 samples per channel).
//
// Native 96 kHz CELT uses overlap=240, the 2-tap HD pre-emphasis and the
// Fs=96000 bitrate/QEXT-reservation budget for 2.5/5/10/20 ms frames, and
// drives the >20 kHz extension-band encode into the secondary range coder.
//
// In a QEXT build, the public Encode at Fs=96000 runs that native HD96k
// CELT-only path whether runtime QEXT is on or off. When runtime QEXT is on,
// it assembles the extension with the encoder package's
// top-level QEXT framing (TOC code 3, padding-length field, main CELT payload
// and the reserved 0xF8 QEXT extension) byte-for-byte like libopus
// --enable-qext at Fs=96000: see tryEncodeNative96k and
// encoder.EncodeNativeHD96k. The TOC, frame-count byte, padding-length field,
// main-payload byte budget and the QEXT extension layout are bit-exact vs the
// QEXT reference; the main CELT payload bytes still carry the HD-scale comb
// prefilter and band data from the native 96 kHz mode.
//
// SILK/Hybrid modes are not available at 96 kHz. The compatibility 2:1 path is
// used only when the native route is unavailable for the selected application.
type encoderHD96kFields struct {
	apiIs96kHz bool
	scratch96k []float32 // downsampled 48 kHz scratch for 96 kHz input path
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

// checkAndDownsample96k prepares the 48 kHz compatibility path for a 96 kHz
// PCM buffer. The native QEXT route passes the original samples directly.
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

	// This compatibility path averages each adjacent input pair per channel.
	for i := 0; i < frameSize48; i++ {
		for c := 0; c < channels; c++ {
			a := pcm[(2*i)*channels+c]
			b := pcm[(2*i+1)*channels+c]
			dst[i*channels+c] = (a + b) * 0.5
		}
	}
	return dst, frameSize48, nil
}

// tryEncodeNative96k routes supported 96 kHz frame sizes through native HD96k
// CELT in QEXT builds. Runtime QEXT controls whether the secondary extension
// coder emits a side payload; it does not select the sample-rate path. The
// caller retains the compatibility path when the native application mode is
// unavailable or the duration is unsupported.
func (e *Encoder) tryEncodeNative96k(pcm []float32, data []byte) (int, bool, error) {
	frameSize := e.apiFrameSize()
	if (frameSize != 240 && frameSize != 480 && frameSize != 960 && frameSize != 1920) || e.application == ApplicationVoIP {
		return 0, false, nil
	}
	if len(pcm) != frameSize*int(e.channels) {
		return 0, true, ErrInvalidFrameSize
	}
	n, err := e.enc.EncodeNativeHD96k(pcm, frameSize, data)
	if err != nil {
		return 0, true, err
	}
	return n, true, nil
}

// init96kEncoder initialises the 96 kHz API flag on a newly-created Encoder.
func init96kEncoder(e *Encoder) {
	e.apiIs96kHz = true
}
