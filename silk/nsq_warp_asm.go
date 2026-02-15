//go:build arm64

package silk

//go:noescape
func warpedARFeedback24(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32

//go:noescape
func warpedARFeedback16(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32
