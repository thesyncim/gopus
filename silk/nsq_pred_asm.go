//go:build arm64

package silk

//go:noescape
func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32

//go:noescape
func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32
