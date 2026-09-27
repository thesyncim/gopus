//go:build gopus_qext

package gopus

// decoderHD96kFields holds the 96 kHz API-rate state for the Decoder.
//
// C ref: opus_decoder.c opus_decoder_init() ENABLE_QEXT gate (Fs == 96000),
// which runs the native 96 kHz CELT mode (mode96000_1920_240) plus the >20 kHz
// extension-band decode chain. gopus routes Fs=96000 to the native HD96k CELT
// decode driver (celt EnableHD96kMode + DecodeFrame at frameSize=1920); the
// QEXT extension payload reserved in the Opus packet padding is parsed by the
// existing top-level extension machinery and forwarded to the CELT layer.
type decoderHD96kFields struct {
	apiIs96kHz bool
}

func (d *Decoder) is96kHz() bool { return d.apiIs96kHz }

// decode96kFloat32 decodes an Opus packet (or PLC frame) natively at 96 kHz.
// The top-level sample rate is 96000 so 20 ms CELT frames decode to 1920
// samples/channel; the CELT decoder runs the native HD96k mode.
func (d *Decoder) decode96kFloat32(data []byte, pcm []float32) (int, error) {
	return d.decodePublicFloat32(data, pcm)
}

// decodeInt1696k decodes at 96 kHz into int16 through the decoder-owned float
// scratch used by raw packet decoding, then applies the int16 output stage.
func (d *Decoder) decodeInt1696k(data []byte, pcm []int16) (int, error) {
	d.beginFixedPacket()
	defer d.endFixedPacket()
	channels := int(d.channels)
	// Reuse the decoder-owned float scratch used by the other public integer
	// wrappers. Match the caller's exact length on every call: exposing a
	// warmed larger scratch slice here would let an undersized output buffer
	// decode farther than the public API permits.
	d.ensureScratchPCM(len(pcm))
	// Integer decode owns the soft-clip history. The public float wrapper clears
	// that history after each Decode call, so route through the raw packet decode
	// with clearing disabled before applying the int16 soft clip below.
	n, err := d.decodeFloat32(data, d.scratchPCM, false)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	d.finishInt16Output(pcm, d.scratchPCM, n, channels)
	return n, nil
}

// decodeInt2496k decodes at 96 kHz into int32 (24-bit) through the
// decoder-owned float scratch used by raw packet decoding.
func (d *Decoder) decodeInt2496k(data []byte, pcm []int32) (int, error) {
	d.beginFixedPacket()
	defer d.endFixedPacket()
	channels := int(d.channels)
	// Keep the temporary float buffer decoder-owned and set its length to the
	// caller's length for the same undersized-buffer behavior as Decode.
	d.ensureScratchPCM(len(pcm))
	// opus_decode24 does not soft-clip its opus_res output. Keep the int16
	// soft-clip history untouched while decoding this format.
	n, err := d.decodeFloat32(data, d.scratchPCM, false)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	d.finishInt24Output(pcm, d.scratchPCM, n, channels)
	return n, nil
}

// init96kDecoder initialises a new Decoder for native 96 kHz API output.
// Called from NewDecoder under gopus_qext when cfg.SampleRate == 96000.
func init96kDecoder(d *Decoder) {
	d.apiIs96kHz = true
	d.sampleRate = 96000
	d.hybridDecoder.SetAPISampleRate(96000)
	// 20 ms at 96 kHz default frame size for PLC sizing.
	d.lastFrameSize = 96000 / 50
	// Switch the CELT decoder into the native HD96k mode (overlap 240,
	// 3840-sample MDCT, HD preemphasis, Fs=96000).
	d.celtDecoder.EnableHD96kMode()
}
