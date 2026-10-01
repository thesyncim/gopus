package silk

import "testing"

// TestNSQDelDecXqQ0Rounding pins the two libopus delayed-decision output
// roundings: silk_NSQ_del_dec_c's SAT16(RSHIFT_ROUND(SMULWW(xq, g), bits)),
// whose 32-bit SMULWW wraps for large products, and silk_NSQ_del_dec_avx2's
// silk_sar_round_smulww, which rounds the exact 64-bit product.
func TestNSQDelDecXqQ0Rounding(t *testing.T) {
	for _, tc := range []struct {
		xq, gain int32
		bits     int
		c, avx2  int16
	}{
		{xq: 123456, gain: 70000, bits: 8, c: 515, avx2: 515},
		{xq: -98765, gain: 1 << 20, bits: 14, c: -96, avx2: -96},
		// 2^30 * 2^20 >> 16 = 2^34 wraps to 0 in the 32-bit SMULWW.
		{xq: 1 << 30, gain: 1 << 20, bits: 8, c: 0, avx2: 32767},
		{xq: -(1 << 30), gain: 1 << 20, bits: 8, c: 0, avx2: -32768},
	} {
		var nsq NSQState
		if got := nsqDelDecXqQ0(&nsq, tc.xq, tc.gain, tc.bits); got != tc.c {
			t.Errorf("C rounding xq=%d gain=%d bits=%d = %d, want %d", tc.xq, tc.gain, tc.bits, got, tc.c)
		}
		nsq.delDecExactXqRound = true
		if got := nsqDelDecXqQ0(&nsq, tc.xq, tc.gain, tc.bits); got != tc.avx2 {
			t.Errorf("AVX2 rounding xq=%d gain=%d bits=%d = %d, want %d", tc.xq, tc.gain, tc.bits, got, tc.avx2)
		}
	}
}
