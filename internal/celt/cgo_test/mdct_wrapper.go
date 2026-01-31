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

// ============================================================================
// FFT Twiddle computation - matches kiss_fft.c compute_twiddles() float path
// ============================================================================

// Compute FFT twiddles exactly as libopus does for float builds
// Reference: kiss_fft.c lines 427-431
static void compute_fft_twiddles(float *twiddles_r, float *twiddles_i, int nfft) {
    const double pi = 3.14159265358979323846264338327;
    for (int i = 0; i < nfft; i++) {
        double phase = (-2.0 * pi / (double)nfft) * (double)i;
        twiddles_r[i] = (float)cos(phase);
        twiddles_i[i] = (float)sin(phase);
    }
}

// ============================================================================
// MDCT trig computation - matches mdct.c clt_mdct_init() float path
// ============================================================================

// PI constant from celt/mathops.h
#define LIBOPUS_PI 3.1415926535897931

// Compute MDCT trig table exactly as libopus does for float builds
// Reference: mdct.c lines 100-101
// trig[i] = (kiss_twiddle_scalar)cos(2*PI*(i+.125)/N)
static void compute_mdct_trig(float *trig, int N) {
    int N2 = N >> 1;
    for (int i = 0; i < N2; i++) {
        trig[i] = (float)cos(2.0 * LIBOPUS_PI * ((double)i + 0.125) / (double)N);
    }
}

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

// ComputeLibopusFFTTwiddles computes FFT twiddles using the same formula as libopus float path.
// Returns two slices: real parts and imaginary parts.
func ComputeLibopusFFTTwiddles(nfft int) ([]float32, []float32) {
	twiddlesR := make([]float32, nfft)
	twiddlesI := make([]float32, nfft)
	C.compute_fft_twiddles(
		(*C.float)(unsafe.Pointer(&twiddlesR[0])),
		(*C.float)(unsafe.Pointer(&twiddlesI[0])),
		C.int(nfft),
	)
	return twiddlesR, twiddlesI
}

// ComputeLibopusMDCTTrig computes MDCT trig table using the same formula as libopus float path.
// N is the MDCT size (e.g., 1920 for 20ms at 48kHz).
// Returns N/2 trig values.
func ComputeLibopusMDCTTrig(N int) []float32 {
	N2 := N / 2
	trig := make([]float32, N2)
	C.compute_mdct_trig(
		(*C.float)(unsafe.Pointer(&trig[0])),
		C.int(N),
	)
	return trig
}
