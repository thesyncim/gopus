//go:build !amd64 || !goexperiment.simd || nosimd

package silk

// silkNSQDelDecUsesAVX2 is false outside the amd64 SIMD build: scalar and
// non-x86 libopus builds run silk_NSQ_del_dec_c.
const silkNSQDelDecUsesAVX2 = false
