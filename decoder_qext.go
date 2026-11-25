//go:build gopus_qext

package gopus

func (d *Decoder) setCELTQEXTPayload(payload []byte) {
	if d == nil || d.celtDecoder == nil {
		return
	}
	d.celtDecoder.SetQEXTPayload(payload)
}

type decoderQEXTPayloads struct {
	payloads [maxRepacketizerFrames][]byte
}

func (p *decoderQEXTPayloads) frame(frameIndex int) []byte {
	if p == nil || frameIndex < 0 || frameIndex >= len(p.payloads) {
		return nil
	}
	return p.payloads[frameIndex]
}

func (p *decoderQEXTPayloads) collect(data []byte, nbFrames, id int) {
	if p == nil {
		return
	}
	collectQEXTPacketExtensions(data, nbFrames, id, &p.payloads)
}

func collectQEXTPacketExtensions(data []byte, nbFrames, id int, payloads *[maxRepacketizerFrames][]byte) {
	if payloads == nil {
		return
	}
	for i := 0; i < maxRepacketizerFrames; i++ {
		payloads[i] = nil
	}
	if len(data) == 0 || nbFrames <= 0 {
		return
	}

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, data, nbFrames)
	for {
		var ext packetExtensionData
		ok, err := iter.next(&ext)
		if err != nil || !ok {
			return
		}
		if ext.ID != id || ext.Frame < 0 || ext.Frame >= nbFrames {
			continue
		}
		if payloads[ext.Frame] == nil {
			payloads[ext.Frame] = ext.Data
		}
	}
}

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
	scratchLen, err := d.scratchLen96k(data, len(pcm), channels)
	if err != nil {
		return 0, err
	}
	d.ensureScratchPCM(scratchLen)
	// Integer decode owns the soft-clip history. The public float wrapper clears
	// that history after each Decode call, so route through the raw packet decode
	// with clearing disabled before applying the int16 soft clip below.
	n, err := d.decodeFloat32(data, d.scratchPCM, false)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	if len(data) == 0 {
		// opus_decode_native returns from its PLC branch before soft clipping.
		// Keep the existing clip history for the next received packet.
		if !d.fixedInt16PLCOutput(pcm, n, channels) {
			float32ToInt16NoSoftClip(pcm, d.scratchPCM, n, channels)
		}
	} else {
		d.finishInt16Output(pcm, d.scratchPCM, n, channels)
	}
	return n, nil
}

// decodeInt2496k decodes at 96 kHz into int32 (24-bit) through the
// decoder-owned float scratch used by raw packet decoding.
func (d *Decoder) decodeInt2496k(data []byte, pcm []int32) (int, error) {
	d.beginFixedPacket()
	defer d.endFixedPacket()
	channels := int(d.channels)
	scratchLen, err := d.scratchLen96k(data, len(pcm), channels)
	if err != nil {
		return 0, err
	}
	d.ensureScratchPCM(scratchLen)
	// opus_decode24 disables clipping in opus_decode_native, which clears the
	// int16 soft-clip history after a received packet. The native PLC branch
	// returns before that clearing step.
	n, err := d.decodeFloat32(data, d.scratchPCM, true)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	d.finishInt24Output(pcm, d.scratchPCM, n, channels)
	return n, nil
}

// scratchLen96k validates packet/PLC bounds before sizing d.scratchPCM so
// oversized caller buffers or rejected packets cannot grow retained scratch.
func (d *Decoder) scratchLen96k(data []byte, pcmLen, channels int) (int, error) {
	if len(data) == 0 {
		frameSize, err := d.plcOutputFrameSize(pcmLen)
		if err != nil {
			return 0, err
		}
		return frameSize * channels, nil
	}
	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}
	return min(pcmLen, d.maxPacketSamples*channels), nil
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
