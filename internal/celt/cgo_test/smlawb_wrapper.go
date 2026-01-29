// Package cgo provides wrappers for testing fixed-point macros
package cgo

/*
#include <stdint.h>

// libopus silk_SMLAWB macro
static inline int32_t silk_SMLAWB(int32_t a32, int32_t b32, int32_t c32) {
    return (a32) + ((((b32) >> 16) * (int32_t)((int16_t)(c32))) + ((((b32) & 0x0000FFFF) * (int32_t)((int16_t)(c32))) >> 16));
}

// libopus silk_SMULWB macro
static inline int32_t silk_SMULWB(int32_t a32, int32_t b32) {
    return ((((a32) >> 16) * (int32_t)((int16_t)(b32))) + ((((a32) & 0x0000FFFF) * (int32_t)((int16_t)(b32))) >> 16));
}

// libopus silk_SMLABB macro
static inline int32_t silk_SMLABB(int32_t a32, int32_t b32, int32_t c32) {
    return ((a32) + ((int32_t)((int16_t)(b32))) * (int32_t)((int16_t)(c32)));
}

// libopus silk_SMULBB macro
static inline int32_t silk_SMULBB(int32_t a32, int32_t b32) {
    return (int32_t)((int16_t)(a32)) * (int32_t)((int16_t)(b32));
}

// silk_RSHIFT_ROUND with correct rounding
static inline int32_t silk_RSHIFT_ROUND(int32_t a, int32_t shift) {
    if (shift <= 0) return a;
    if (shift == 1) return (a >> 1) + (a & 1);
    return ((a >> (shift - 1)) + 1) >> 1;
}

// silk_DIV32_16
static inline int32_t silk_DIV32_16(int32_t a, int32_t b) {
    return a / b;
}

#define STEREO_INTERP_LEN_MS 8

// Compute delta exactly as libopus does
int32_t compute_delta_libopus(int32_t pred_Q13, int32_t prev_Q13, int32_t fs_kHz) {
    int32_t denom_Q16 = silk_DIV32_16((int32_t)1 << 16, STEREO_INTERP_LEN_MS * fs_kHz);
    return silk_RSHIFT_ROUND(silk_SMULBB(pred_Q13 - prev_Q13, denom_Q16), 16);
}

// Get the denomQ16 value
int32_t get_denom_Q16_libopus(int32_t fs_kHz) {
    return silk_DIV32_16((int32_t)1 << 16, STEREO_INTERP_LEN_MS * fs_kHz);
}

// Test functions
int32_t cgo_silk_SMLAWB(int32_t a, int32_t b, int32_t c) {
    return silk_SMLAWB(a, b, c);
}

int32_t cgo_silk_SMULWB(int32_t a, int32_t b) {
    return silk_SMULWB(a, b);
}

int32_t cgo_silk_SMLABB(int32_t a, int32_t b, int32_t c) {
    return silk_SMLABB(a, b, c);
}
*/
import "C"

// LibopusSMLAWB calls the libopus SMLAWB macro through CGO
func LibopusSMLAWB(a, b, c int32) int32 {
	return int32(C.cgo_silk_SMLAWB(C.int32_t(a), C.int32_t(b), C.int32_t(c)))
}

// LibopusSMULWB calls the libopus SMULWB macro through CGO
func LibopusSMULWB(a, b int32) int32 {
	return int32(C.cgo_silk_SMULWB(C.int32_t(a), C.int32_t(b)))
}

// LibopusSMLABB calls the libopus SMLABB macro through CGO
func LibopusSMLABB(a, b, c int32) int32 {
	return int32(C.cgo_silk_SMLABB(C.int32_t(a), C.int32_t(b), C.int32_t(c)))
}

// LibopusComputeDelta computes the stereo prediction delta as libopus does
func LibopusComputeDelta(predQ13, prevQ13, fsKHz int32) int32 {
	return int32(C.compute_delta_libopus(C.int32_t(predQ13), C.int32_t(prevQ13), C.int32_t(fsKHz)))
}

// LibopusGetDenomQ16 gets the denomQ16 value for a given sample rate
func LibopusGetDenomQ16(fsKHz int32) int32 {
	return int32(C.get_denom_Q16_libopus(C.int32_t(fsKHz)))
}
