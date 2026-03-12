package gopus

import "github.com/thesyncim/gopus/multistream"

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

// SetOSCEBWE toggles the optional OSCE bandwidth extension path.
func (d *MultistreamDecoder) SetOSCEBWE(_ bool) error {
	return ErrUnimplemented
}

// OSCEBWE reports whether the optional OSCE bandwidth extension path is enabled.
func (d *MultistreamDecoder) OSCEBWE() (bool, error) {
	return false, ErrUnimplemented
}

// SetDNNBlob loads an optional model blob for decoder extension features.
func (d *MultistreamDecoder) SetDNNBlob(_ []byte) error {
	return ErrUnimplemented
}

// GetDecoderState returns the decoder state for an individual multistream stream.
// This matches libopus OPUS_MULTISTREAM_GET_DECODER_STATE semantics.
func (d *MultistreamDecoder) GetDecoderState(index int) (*multistream.StreamDecoder, error) {
	if index < 0 || index >= d.dec.Streams() {
		return nil, ErrInvalidStreamIndex
	}
	return d.dec.GetDecoderState(index)
}
