package celt

// TestEncodeLaplace exposes encodeLaplace for testing.
func (e *Encoder) TestEncodeLaplace(val, fs, decay int) int {
	return e.encodeLaplace(val, fs, decay)
}

// TestDecodeLaplace exposes decodeLaplace for testing.
func (d *Decoder) TestDecodeLaplace(fs, decay int) int {
	return d.decodeLaplace(fs, decay)
}
