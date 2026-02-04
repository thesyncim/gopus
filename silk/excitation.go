package silk

// scaleExcitation applies gain to excitation signal for one subframe.
// Per RFC 6716 Section 4.2.7.9.
//
// The gain is in Q16 format. The excitation is scaled to Q0 (PCM units)
// and returned in the same array (modified in place).
func scaleExcitation(excitation []int32, gain int32) {
	for i := range excitation {
		// Multiply by Q16 gain and return to Q0.
		excitation[i] = (excitation[i] * gain) >> 16
	}
}
