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

// Stub for opus_debug_range (required by debug builds)
void opus_debug_range(unsigned int a, unsigned int b, unsigned int c, unsigned int d) {
    // Debug stub - does nothing
}

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

// Create an opus decoder
OpusDecoder* test_decoder_create(int sample_rate, int channels, int *error) {
    return opus_decoder_create(sample_rate, channels, error);
}

// Destroy an opus decoder
void test_decoder_destroy(OpusDecoder* dec) {
    opus_decoder_destroy(dec);
}

// Decode a single packet with persistent decoder state
int test_decode_float(OpusDecoder* dec, const unsigned char *data, int data_len,
                      float *pcm_out, int max_samples) {
    return opus_decode_float(dec, data, data_len, pcm_out, max_samples, 0);
}

// MDCT/IMDCT test functions using internal libopus modes
#include "modes.h"
#include "mdct.h"

// Get the static mode for 48kHz / 960 samples
CELTMode* test_get_celt_mode_48000_960() {
    return opus_custom_mode_create(48000, 960, NULL);
}

// Get the window from the mode
const float* test_get_mode_window(CELTMode* mode) {
    return mode->window;
}

// Get overlap from the mode
int test_get_mode_overlap(CELTMode* mode) {
    return mode->overlap;
}

// Get MDCT size for a given shift
int test_get_mdct_size(CELTMode* mode, int shift) {
    return mode->mdct.n >> shift;
}

// Perform IMDCT using libopus clt_mdct_backward
// Input: N2 frequency coefficients
// Output: N2 + overlap time samples (windowed overlap-add format)
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
void test_imdct_backward(CELTMode* mode, float* in, float* out, int shift) {
    int n = mode->mdct.n >> shift;
    int n2 = n >> 1;
    int overlap = mode->overlap;

    // Zero output buffer
    memset(out, 0, (n2 + overlap) * sizeof(float));

    // Call libopus IMDCT
    clt_mdct_backward(&mode->mdct, in, out, mode->window, overlap, shift, 1, 0);
}

// Perform MDCT using libopus clt_mdct_forward
// Input: N2 + overlap time samples
// Output: N2 frequency coefficients
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
void test_mdct_forward(CELTMode* mode, float* in, float* out, int shift) {
    int n = mode->mdct.n >> shift;
    int n2 = n >> 1;
    int overlap = mode->overlap;

    // Call libopus MDCT
    clt_mdct_forward(&mode->mdct, in, out, mode->window, overlap, shift, 1, 0);
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

	cY := (*C.float)(unsafe.Pointer(&y[0]))
	cWindow := (*C.float)(unsafe.Pointer(&window[0]))

	// Pass y for both input and output to match the in-place usage in the decoder.
	C.test_comb_filter(cY, cY, C.int(history), C.int(T0), C.int(T1), C.int(n),
		C.float(g0), C.float(g1), C.int(tapset0), C.int(tapset1),
		cWindow, C.int(overlap))

	return y
}

// VorbisWindow computes the Vorbis window value using libopus formula.
func VorbisWindow(i, overlap int) float32 {
	return float32(C.test_vorbis_window(C.int(i), C.int(overlap)))
}

// LibopusDecoder wraps an opus decoder for comparison tests.
type LibopusDecoder struct {
	dec *C.OpusDecoder
}

// NewLibopusDecoder creates a new libopus decoder.
func NewLibopusDecoder(sampleRate, channels int) (*LibopusDecoder, error) {
	var err C.int
	dec := C.test_decoder_create(C.int(sampleRate), C.int(channels), &err)
	if err != 0 || dec == nil {
		return nil, nil // Return nil to indicate failure
	}
	return &LibopusDecoder{dec: dec}, nil
}

// Destroy frees the decoder resources.
func (d *LibopusDecoder) Destroy() {
	if d.dec != nil {
		C.test_decoder_destroy(d.dec)
		d.dec = nil
	}
}

// DecodeFloat decodes a packet to float32 samples.
func (d *LibopusDecoder) DecodeFloat(data []byte, maxSamples int) ([]float32, int) {
	if d.dec == nil || len(data) == 0 {
		return nil, -1
	}

	pcm := make([]float32, maxSamples*2) // stereo
	cData := (*C.uchar)(unsafe.Pointer(&data[0]))
	cPcm := (*C.float)(unsafe.Pointer(&pcm[0]))

	samples := int(C.test_decode_float(d.dec, cData, C.int(len(data)), cPcm, C.int(maxSamples)))
	if samples < 0 {
		return nil, samples
	}
	return pcm, samples
}

// CELTMode wraps a libopus CELT mode for MDCT tests.
type CELTMode struct {
	mode *C.CELTMode
}

// GetCELTMode48000_960 returns the standard CELT mode for 48kHz/960 samples.
func GetCELTMode48000_960() *CELTMode {
	mode := C.test_get_celt_mode_48000_960()
	if mode == nil {
		return nil
	}
	return &CELTMode{mode: mode}
}

// Overlap returns the overlap size for this mode.
func (m *CELTMode) Overlap() int {
	return int(C.test_get_mode_overlap(m.mode))
}

// MDCTSize returns the MDCT size for a given shift value.
// shift: 0=1920, 1=960, 2=480, 3=240
func (m *CELTMode) MDCTSize(shift int) int {
	return int(C.test_get_mdct_size(m.mode, C.int(shift)))
}

// MDCTForward computes forward MDCT using libopus.
// Input: n2 + overlap time samples
// Output: n2 frequency coefficients
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
func (m *CELTMode) MDCTForward(input []float32, shift int) []float32 {
	nfft := m.MDCTSize(shift)
	n2 := nfft / 2
	output := make([]float32, n2)

	cIn := (*C.float)(unsafe.Pointer(&input[0]))
	cOut := (*C.float)(unsafe.Pointer(&output[0]))
	C.test_mdct_forward(m.mode, cIn, cOut, C.int(shift))

	return output
}

// IMDCTBackward computes inverse MDCT using libopus.
// Input: n2 frequency coefficients
// Output: n2 + overlap time samples
// shift: 0=1920, 1=960, 2=480, 3=240 (MDCT size = 1920 >> shift)
func (m *CELTMode) IMDCTBackward(input []float32, shift int) []float32 {
	nfft := m.MDCTSize(shift)
	n2 := nfft / 2
	overlap := m.Overlap()
	output := make([]float32, n2+overlap)

	cIn := (*C.float)(unsafe.Pointer(&input[0]))
	cOut := (*C.float)(unsafe.Pointer(&output[0]))
	C.test_imdct_backward(m.mode, cIn, cOut, C.int(shift))

	return output
}

// GetWindow returns the Vorbis window values for the mode's overlap.
func (m *CELTMode) GetWindow() []float32 {
	overlap := m.Overlap()
	cWindow := C.test_get_mode_window(m.mode)

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		// Access C array directly
		window[i] = float32(*(*C.float)(unsafe.Pointer(uintptr(unsafe.Pointer(cWindow)) + uintptr(i)*unsafe.Sizeof(*cWindow))))
	}
	return window
}
