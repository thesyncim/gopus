// Package cgo provides CGO wrappers for libopus comparison tests.
// This is in a separate package to enable CGO in tests.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: -L${SRCDIR}/../../../tmp_check/opus-1.6.1/.libs -lopus -lm

#include <stdlib.h>
#include <string.h>
#include <math.h>
#include "opus.h"
#include "celt.h"
#include "entdec.h"
#include "laplace.h"

// Test harness to decode a Laplace symbol
int test_laplace_decode(const unsigned char *data, int data_len, int fs, int decay, int *out_val) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    *out_val = ec_laplace_decode(&dec, fs, decay);
    return 0;
}

// Get range coder state after init
void test_get_range_state(const unsigned char *data, int data_len, unsigned int *out_rng, unsigned int *out_val) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    *out_rng = dec.rng;
    *out_val = dec.val;
}

// Decode a bit with logp probability
int test_decode_bit_logp(const unsigned char *data, int data_len, int logp) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    return ec_dec_bit_logp(&dec, logp);
}

// Decode using ICDF
int test_decode_icdf(const unsigned char *data, int data_len, const unsigned char *icdf, int ftb) {
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    return ec_dec_icdf(&dec, icdf, ftb);
}

// Declaration for comb_filter from celt.h
void comb_filter(opus_val32 *y, opus_val32 *x, int T0, int T1, int N,
      opus_val16 g0, opus_val16 g1, int tapset0, int tapset1,
      const opus_val16 *window, int overlap, int arch);

// Test harness for comb_filter
// Allocates internal buffer, copies input, applies filter, copies output.
// Input x and output y are float arrays of length n.
// Window is float array of length overlap.
// Uses arch=0 (generic implementation).
void test_comb_filter(float *y, float *x, int history, int T0, int T1, int n,
                      float g0, float g1, int tapset0, int tapset1,
                      const float *window, int overlap) {
    // Apply comb filter (x pointer starts at history, so x[-T] is valid)
    comb_filter(y + history, x + history, T0, T1, n, g0, g1, tapset0, tapset1, window, overlap, 0);
}

// Compute Vorbis window value at position i for overlap length
float test_vorbis_window(int i, int overlap) {
    float x = (float)(i) + 0.5f;
    float sinArg = 0.5f * M_PI * x / (float)(overlap);
    float s = sinf(sinArg);
    return sinf(0.5f * M_PI * s * s);
}

*/
import "C"

import (
	"unsafe"
)

// DecodeLaplace calls libopus ec_laplace_decode
func DecodeLaplace(data []byte, fs, decay int) int {
	var val C.int
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	C.test_laplace_decode(cData, C.int(len(data)), C.int(fs), C.int(decay), &val)
	return int(val)
}

// GetRangeState gets the range coder state after initialization
func GetRangeState(data []byte) (rng, val uint32) {
	var cRng, cVal C.uint
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	C.test_get_range_state(cData, C.int(len(data)), &cRng, &cVal)
	return uint32(cRng), uint32(cVal)
}

// DecodeBitLogp calls libopus ec_dec_bit_logp
func DecodeBitLogp(data []byte, logp int) int {
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	return int(C.test_decode_bit_logp(cData, C.int(len(data)), C.int(logp)))
}

// DecodeICDF calls libopus ec_dec_icdf
func DecodeICDF(data []byte, icdf []byte, ftb int) int {
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	cICDF := (*C.uchar)(unsafe.Pointer(&icdf[0]))
	return int(C.test_decode_icdf(cData, C.int(len(data)), cICDF, C.int(ftb)))
}

// CombFilter calls libopus comb_filter function.
// x is the input buffer (includes history), y is the output buffer.
// history is the offset where the actual frame data starts.
// T0, T1 are the old and new pitch periods.
// g0, g1 are the old and new gains.
// tapset0, tapset1 are the old and new tapsets.
// window is the Vorbis window for crossfade.
// n is the number of samples to process.
// overlap is the crossfade length.
func CombFilter(x []float32, history, T0, T1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) []float32 {
	y := make([]float32, len(x))
	copy(y, x) // libopus comb_filter modifies y in-place

	cX := (*C.float)(unsafe.Pointer(&x[0]))
	cY := (*C.float)(unsafe.Pointer(&y[0]))
	cWindow := (*C.float)(unsafe.Pointer(&window[0]))

	C.test_comb_filter(cY, cX, C.int(history), C.int(T0), C.int(T1), C.int(n),
		C.float(g0), C.float(g1), C.int(tapset0), C.int(tapset1),
		cWindow, C.int(overlap))

	return y
}

// VorbisWindow computes the Vorbis window value using libopus formula.
func VorbisWindow(i, overlap int) float32 {
	return float32(C.test_vorbis_window(C.int(i), C.int(overlap)))
}
