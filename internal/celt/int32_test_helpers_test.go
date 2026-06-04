package celt

func int32SliceForTest(src []int) []int32 {
	dst := make([]int32, len(src))
	for i, v := range src {
		dst[i] = int32(v)
	}
	return dst
}
