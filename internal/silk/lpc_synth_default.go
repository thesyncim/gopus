//go:build (!arm64 && !amd64) || !goexperiment.simd || nosimd || purego

package silk

func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	synthesizeLPCOrder16CoreScalar(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
}

func synthesizeLPCOrder10Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	synthesizeLPCOrder10CoreScalar(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
}
