//go:build !(arm64 && goexperiment.simd && !nosimd && !purego)

package silk

func interleaveInt16AsFloat32Core(dst []float32, left, right []int16) {
	const inv32768 = 1.0 / 32768.0
	right = right[:len(left)]
	dst = dst[:2*len(left)]
	for i, l := range left {
		pair := (*[2]float32)(dst[2*i : 2*i+2])
		pair[0] = float32(l) * inv32768
		pair[1] = float32(right[i]) * inv32768
	}
}

func duplicateMonoFloat32ToStereoCore(dst, src []float32, n int) {
	if n <= 0 {
		return
	}
	dst = dst[: n*2 : n*2]
	src = src[:n:n]
	i := 0
	j := 0
	for ; i+3 < n; i += 4 {
		v0 := src[i]
		v1 := src[i+1]
		v2 := src[i+2]
		v3 := src[i+3]
		dst[j] = v0
		dst[j+1] = v0
		dst[j+2] = v1
		dst[j+3] = v1
		dst[j+4] = v2
		dst[j+5] = v2
		dst[j+6] = v3
		dst[j+7] = v3
		j += 8
	}
	for ; i < n; i++ {
		v := src[i]
		dst[j] = v
		dst[j+1] = v
		j += 2
	}
}
