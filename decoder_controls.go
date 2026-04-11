package gopus

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()
	d.hybridDecoder.Reset()
	d.lastFrameSize = 960
	d.lastPacketDuration = 0
	d.prevMode = ModeHybrid
	d.lastPacketMode = ModeHybrid
	d.lastBandwidth = BandwidthFullband
	d.prevRedundancy = false
	d.prevPacketStereo = false
	d.haveDecoded = false
	d.softClipMem[0] = 0
	d.softClipMem[1] = 0
	d.clearDREDPayloadState()

	// Clear FEC state
	d.clearFECState()
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SampleRate returns the sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
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

// SetIgnoreExtensions toggles whether unknown packet extensions should be ignored.
//
// This mirrors libopus OPUS_SET_IGNORE_EXTENSIONS semantics.
func (d *Decoder) SetIgnoreExtensions(ignore bool) {
	d.ignoreExtensions = ignore
	if ignore {
		d.clearDREDPayloadState()
	}
}

// IgnoreExtensions reports whether unknown packet extensions are ignored.
func (d *Decoder) IgnoreExtensions() bool {
	return d.ignoreExtensions
}

// SetOSCEBWE toggles libopus's optional ENABLE_OSCE_BWE decoder extension.
//
// The default gopus build does not enable this extension; check
// SupportsOptionalExtension(OptionalExtensionOSCEBWE) and expect
// ErrUnsupportedExtension when unavailable.
func (d *Decoder) SetOSCEBWE(_ bool) error {
	return ErrUnsupportedExtension
}

// OSCEBWE reports whether the optional OSCE bandwidth extension path is enabled.
func (d *Decoder) OSCEBWE() (bool, error) {
	return false, ErrUnsupportedExtension
}

// SetDNNBlob loads the optional libopus USE_WEIGHTS_FILE decoder model blob.
//
// The loaded blob is validated using libopus-style weights-record framing and
// retained across Reset(), matching libopus USE_WEIGHTS_FILE control lifetime.
func (d *Decoder) SetDNNBlob(data []byte) error {
	blob, err := cloneDecoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	d.setDNNBlob(blob)
	return nil
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
