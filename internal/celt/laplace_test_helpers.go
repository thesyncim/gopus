package celt

// TestEncodeLaplace exposes encodeLaplace for testing.
func (e *Encoder) TestEncodeLaplace(val, fs, decay int) int {
	return e.encodeLaplace(val, fs, decay)
}

// TestDecodeLaplace is defined in export_test.go
