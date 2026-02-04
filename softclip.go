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

	// Clamp to [-2, 2]. The generic libopus C path does not provide
	// a fast-path hint for all samples within [-1, 1], so we keep
	// allWithinNeg1Pos1=false to mirror that behavior.
	allWithinNeg1Pos1 := false
	for i := 0; i < total; i++ {
		v := x[i]
		if v > 2 {
			x[i] = 2
		} else if v < -2 {
			x[i] = -2
		}
	}

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

func float32Abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
