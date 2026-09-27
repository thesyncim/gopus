//go:build gopus_fixed_point && gopus_qext

package fixedpoint

type qextMDCTScratch struct {
	mdct QEXTMDCTScratch
}

// mdctForward uses the Q31 CELT transform selected by ENABLE_QEXT, including
// frames for which the encoder reserves no extension payload.
func (e *CELTEncoder) mdctForward(in, out []int32, overlap, shift, stride int, scratch *celtEncodeScratch) {
	NewStaticQEXTMDCTLookup48000().MDCTForward(in, out, nil, overlap, shift, stride, &scratch.qextMDCT.mdct)
}
