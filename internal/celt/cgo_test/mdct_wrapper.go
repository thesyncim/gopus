// Package cgo provides wrappers for comparing MDCT operations with libopus
package cgo

/*
#cgo pkg-config: opus
#include <opus.h>
#include <opus_types.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

// Access internal CELT structures - we need to manually define these
// because they're not in the public API

// Window function from mode tables
static void compute_vorbis_window(float *win, int overlap) {
    for (int i = 0; i < overlap; i++) {
        double x = (double)(i) + 0.5;
        double sinArg = 0.5 * M_PI * x / (double)(overlap);
        double s = sin(sinArg);
        win[i] = (float)sin(0.5 * M_PI * s * s);
    }
}

// Simple comparison function - decode same packet with libopus and return samples
static int decode_and_get_samples(const unsigned char *pkt, int pkt_len,
                                   float *out, int max_samples, int channels) {
    int error;
    OpusDecoder *dec = opus_decoder_create(48000, channels, &error);
    if (!dec || error != 0) {
        return -1;
    }

    int samples = opus_decode_float(dec, pkt, pkt_len, out, max_samples, 0);
    opus_decoder_destroy(dec);
    return samples;
}

// Get the internal overlap buffer state from libopus (for comparison)
// This is tricky because it requires access to internal state
// For now, we'll use a simpler approach - compare frame outputs
*/
import "C"
import (
	"math"
	"unsafe"
)

// ComputeLibopusWindow computes the Vorbis window using the same formula as libopus
func ComputeLibopusWindow(overlap int) []float64 {
	win := make([]float32, overlap)
	C.compute_vorbis_window((*C.float)(unsafe.Pointer(&win[0])), C.int(overlap))

	result := make([]float64, overlap)
	for i, v := range win {
		result[i] = float64(v)
	}
	return result
}

// DecodeWithLibopus decodes a packet using libopus and returns float32 samples
func DecodeWithLibopus(pkt []byte, channels int) []float32 {
	if len(pkt) == 0 {
		return nil
	}

	maxSamples := 5760 * channels
	out := make([]float32, maxSamples)

	samples := C.decode_and_get_samples(
		(*C.uchar)(unsafe.Pointer(&pkt[0])),
		C.int(len(pkt)),
		(*C.float)(unsafe.Pointer(&out[0])),
		C.int(5760),
		C.int(channels),
	)

	if samples <= 0 {
		return nil
	}

	return out[:int(samples)*channels]
}

// CompareWindowCoefficients compares our window with libopus's formula
func CompareWindowCoefficients(overlap int, ourWindow []float64) (maxDiff float64, maxDiffIdx int) {
	libWin := ComputeLibopusWindow(overlap)

	for i := 0; i < overlap; i++ {
		diff := math.Abs(ourWindow[i] - libWin[i])
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	return
}
