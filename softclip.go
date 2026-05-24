package gopus

import "github.com/thesyncim/gopus/internal/opusmath"

// opusPCMSoftClip applies the libopus soft clipping algorithm in-place.
// It expects interleaved samples in the range of roughly [-1, 1].
// This mirrors opus_pcm_soft_clip_impl() in libopus for float builds.
func opusPCMSoftClip(x []float32, n, channels int, declipMem []float32) {
	opusmath.PCMSoftClip(x, n, channels, declipMem)
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
		if convertFloat32ToInt16Unit(dst, src, total) {
			return
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
	convertFloat32ToInt16NoSoftClipUnit(dst, src, total)
}

func softClipAndFloat32ToInt16Scalar(dst []int16, src []float32, n, channels int, declipMem []float32) {
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
	opusPCMSoftClip(src[:total], n, channels, declipMem)
	for i := 0; i < total; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}

func float32ToInt16NoSoftClip(dst []int16, src []float32, n, channels int) {
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
	convertFloat32ToInt16NoSoftClipUnit(dst, src, total)
}

func float32ToInt16NoSoftClipScalar(dst []int16, src []float32, n, channels int) {
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
	for i := 0; i < total; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}
