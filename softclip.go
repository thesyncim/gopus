package gopus

// opusPCMSoftClip applies the libopus soft clipping algorithm in-place.
// It expects interleaved samples in the range of roughly [-1, 1].
// This mirrors opus_pcm_soft_clip_impl() in libopus for float builds.
func opusPCMSoftClip(x []float32, n, channels int, declipMem []float32) {
	if channels < 1 || n < 1 || len(x) == 0 || len(declipMem) < channels {
		return
	}

	total := n * channels
	if total > len(x) {
		total = len(x)
	}

	// Clamp to [-2, 2] while detecting the common no-op case. When all samples
	// are already within [-1, 1] and no declip curve carries over from the
	// previous frame, the libopus soft-clip algorithm leaves the buffer
	// unchanged and clears the memory to zero.
	allWithinNeg1Pos1 := true
	for i := 0; i < total; i++ {
		v := x[i]
		if v > 2 {
			x[i] = 2
			allWithinNeg1Pos1 = false
		} else if v < -2 {
			x[i] = -2
			allWithinNeg1Pos1 = false
		} else if v > 1 || v < -1 {
			allWithinNeg1Pos1 = false
		}
	}
	if allWithinNeg1Pos1 {
		for c := 0; c < channels; c++ {
			if declipMem[c] != 0 {
				goto applySoftClip
			}
		}
		return
	}

applySoftClip:
	for c := 0; c < channels; c++ {
		a := declipMem[c]

		// Continue applying the non-linearity from the previous frame.
		for i := 0; i < n; i++ {
			idx := i*channels + c
			if idx >= len(x) {
				break
			}
			v := x[idx]
			if v*a >= 0 {
				break
			}
			x[idx] = v + a*v*v
		}

		curr := 0
		if c >= len(x) {
			declipMem[c] = a
			continue
		}
		x0 := x[c]

		for {
			var i int
			if allWithinNeg1Pos1 {
				i = n
			} else {
				for i = curr; i < n; i++ {
					idx := i*channels + c
					if idx >= len(x) {
						i = n
						break
					}
					v := x[idx]
					if v > 1 || v < -1 {
						break
					}
				}
			}

			if i == n {
				a = 0
				break
			}

			peakPos := i
			start := i
			end := i
			idx := i*channels + c
			if idx >= len(x) {
				a = 0
				break
			}
			vref := x[idx]
			maxval := float32Abs(vref)

			for start > 0 {
				idxPrev := (start-1)*channels + c
				if idxPrev >= len(x) {
					break
				}
				if vref*x[idxPrev] < 0 {
					break
				}
				start--
			}
			for end < n {
				idxEnd := end*channels + c
				if idxEnd >= len(x) {
					break
				}
				if vref*x[idxEnd] < 0 {
					break
				}
				val := float32Abs(x[idxEnd])
				if val > maxval {
					maxval = val
					peakPos = end
				}
				end++
			}

			special := start == 0 && vref*x[c] >= 0

			if maxval > 0 {
				a = (maxval - 1) / (maxval * maxval)
				a += a * 2.4e-7
				if vref > 0 {
					a = -a
				}
			} else {
				a = 0
			}

			for i = start; i < end; i++ {
				idx2 := i*channels + c
				if idx2 >= len(x) {
					break
				}
				v := x[idx2]
				x[idx2] = v + a*v*v
			}

			if special && peakPos >= 2 {
				offset := x0 - x[c]
				delta := offset / float32(peakPos)
				for i = curr; i < peakPos; i++ {
					offset -= delta
					idx2 := i*channels + c
					if idx2 >= len(x) {
						break
					}
					v := x[idx2] + offset
					if v > 1 {
						v = 1
					} else if v < -1 {
						v = -1
					}
					x[idx2] = v
				}
			}

			curr = end
			if curr == n {
				break
			}
		}

		declipMem[c] = a
	}
}

func softClipAndFloat32ToInt16(dst []int16, src []float32, n, channels int, declipMem []float32) {
	if channels < 1 || n < 1 || len(src) == 0 || len(dst) == 0 {
		return
	}
	total := n * channels
	if total > len(src) {
		total = len(src)
	}
	if total > len(dst) {
		total = len(dst)
	}
	if total <= 0 {
		return
	}

	if len(declipMem) >= channels {
		for c := 0; c < channels; c++ {
			if declipMem[c] != 0 {
				goto fallback
			}
		}
		_ = src[total-1]
		_ = dst[total-1]
		for i := 0; i < total; i++ {
			v := src[i]
			if v > 1 || v < -1 {
				goto fallback
			}
			dst[i] = float32ToInt16(v)
		}
		return
	}

fallback:
	opusPCMSoftClip(src[:total], n, channels, declipMem)
	for i := 0; i < total; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}

func float32Abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
