package gopus

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *MultistreamDecoder) Reset() {
	d.dec.Reset()
	d.lastFrameSize = 960
}

// Channels returns the number of audio channels.
func (d *MultistreamDecoder) Channels() int {
	return d.channels
}

// SampleRate returns the sample rate in Hz.
func (d *MultistreamDecoder) SampleRate() int {
	return d.sampleRate
}

// Streams returns the total number of elementary streams.
func (d *MultistreamDecoder) Streams() int {
	return d.dec.Streams()
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (d *MultistreamDecoder) CoupledStreams() int {
	return d.dec.CoupledStreams()
}

// SetIgnoreExtensions toggles whether unknown packet extensions should be ignored.
func (d *MultistreamDecoder) SetIgnoreExtensions(ignore bool) {
	d.ignoreExtensions = ignore
}

// IgnoreExtensions reports whether unknown packet extensions are ignored.
func (d *MultistreamDecoder) IgnoreExtensions() bool {
	return d.ignoreExtensions
}

// SetOSCEBWE toggles libopus's optional ENABLE_OSCE_BWE decoder extension.
//
// The default gopus build does not enable this extension; check
// SupportsOptionalExtension(OptionalExtensionOSCEBWE) and expect
// ErrUnsupportedExtension when unavailable.
func (d *MultistreamDecoder) SetOSCEBWE(_ bool) error {
	return ErrUnsupportedExtension
}

// OSCEBWE reports whether the optional OSCE bandwidth extension path is enabled.
func (d *MultistreamDecoder) OSCEBWE() (bool, error) {
	return false, ErrUnsupportedExtension
}

// SetDNNBlob loads the optional libopus USE_WEIGHTS_FILE decoder model blob.
//
// The loaded blob is validated using libopus-style weights-record framing and
// retained across Reset(), matching libopus USE_WEIGHTS_FILE control lifetime.
func (d *MultistreamDecoder) SetDNNBlob(data []byte) error {
	blob, err := cloneDecoderDNNBlobForControl(data)
	if err != nil {
		return err
	}
	d.dnnBlob = blob
	return nil
}
