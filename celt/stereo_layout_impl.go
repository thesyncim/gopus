//go:build !arm64 || purego

package celt

func deinterleaveStereoIntoImpl(interleaved, left, right []float64, n int) {
	i := 0
	for ; i+3 < n; i += 4 {
		b0 := i * 2
		left[i] = interleaved[b0]
		right[i] = interleaved[b0+1]
		left[i+1] = interleaved[b0+2]
		right[i+1] = interleaved[b0+3]
		left[i+2] = interleaved[b0+4]
		right[i+2] = interleaved[b0+5]
		left[i+3] = interleaved[b0+6]
		right[i+3] = interleaved[b0+7]
	}
	for ; i < n; i++ {
		b := i * 2
		left[i] = interleaved[b]
		right[i] = interleaved[b+1]
	}
}

func interleaveStereoIntoImpl(left, right, interleaved []float64, n int) {
	i := 0
	for ; i+3 < n; i += 4 {
		b0 := i << 1
		interleaved[b0] = left[i]
		interleaved[b0+1] = right[i]
		interleaved[b0+2] = left[i+1]
		interleaved[b0+3] = right[i+1]
		interleaved[b0+4] = left[i+2]
		interleaved[b0+5] = right[i+2]
		interleaved[b0+6] = left[i+3]
		interleaved[b0+7] = right[i+3]
	}
	for ; i < n; i++ {
		b := i << 1
		interleaved[b] = left[i]
		interleaved[b+1] = right[i]
	}
}
