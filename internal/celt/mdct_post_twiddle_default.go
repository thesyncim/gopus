//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// mdctUsePostTwiddleNeon is false off the fused arm64 build, so the forward
// MDCT keeps its scalar post-twiddle loop and the nosimd/amd64 byte-exact
// oracle holds.
const mdctUsePostTwiddleNeon = false

// mdctPostTwiddleNeon is never called off arm64 (guarded by
// mdctUsePostTwiddleNeon); the stub keeps the package building on all
// targets.
func mdctPostTwiddleNeon(coeffs []float32, fftStage []float32, trig []float32, n2, n4, pairBlocks int) {
	for i := 0; i < 4*pairBlocks; i++ {
		j := n4 - 1 - i
		coeffs[2*i] = mdctMulSubMixEncode(fftStage[2*i+1], fftStage[2*i], trig[n4+i], trig[i])
		coeffs[n2-1-2*i] = mdctMulAddMixEncode(fftStage[2*i], fftStage[2*i+1], trig[n4+i], trig[i])
		coeffs[2*j] = mdctMulSubMixEncode(fftStage[2*j+1], fftStage[2*j], trig[n4+j], trig[j])
		coeffs[n2-1-2*j] = mdctMulAddMixEncode(fftStage[2*j], fftStage[2*j+1], trig[n4+j], trig[j])
	}
}
