// Package cgo provides CGO wrappers for intermediate value comparison.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H -DVAR_ARRAYS
#cgo LDFLAGS: -L${SRCDIR}/../../../tmp_check/opus-1.6.1/.libs -lopus -lm

#include <stdlib.h>
#include <string.h>
#include <math.h>
#include "opus.h"
// Note: We implement our own band energy computation to avoid complex libopus internal dependencies

// Apply pre-emphasis using libopus algorithm
// Input: pcm in [-1, 1] float range
// Output: pre-emphasized signal at CELT signal scale
void test_celt_preemphasis(const float *pcm, float *out, int N, float coef, float *mem) {
    float m = *mem;
    for (int i = 0; i < N; i++) {
        float x = pcm[i] * 32768.0f;  // Scale to CELT signal scale
        out[i] = x - m;
        m = coef * x;
    }
    *mem = m;
}

// eMeans values from libopus (float version)
static const float eMeans_libopus[25] = {
    6.437500f, 6.250000f, 5.750000f, 5.312500f, 5.062500f,
    4.812500f, 4.500000f, 4.375000f, 4.875000f, 4.687500f,
    4.562500f, 4.437500f, 4.875000f, 4.625000f, 4.312500f,
    4.500000f, 4.375000f, 4.625000f, 4.750000f, 4.437500f,
    3.750000f, 3.750000f, 3.750000f, 3.750000f, 3.750000f
};

// EBands table (base 2.5ms frame)
static const int eBands_base[22] = {
    0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
    12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
    78, 100
};

// Compute band energies like libopus
// Input: MDCT coefficients
// Output: log2(amplitude) for each band (BEFORE eMeans subtraction)
void test_compute_band_energies_raw(const float *mdct, float *bandE, int N, int nbBands, int LM) {
    int scale = 1 << LM;  // 1, 2, 4, or 8 for LM=0,1,2,3

    for (int band = 0; band < nbBands && band < 21; band++) {
        int start = eBands_base[band] * scale;
        int end = eBands_base[band + 1] * scale;
        if (end > N) end = N;

        float sum = 1e-27f;
        for (int i = start; i < end; i++) {
            sum += mdct[i] * mdct[i];
        }

        // bandE = sqrt(sum), then log2
        float amplitude = sqrtf(sum);
        bandE[band] = log2f(amplitude);  // This is what amp2Log2 does for float
    }
}

// Compute band energies with eMeans subtraction (mean-relative)
void test_compute_band_energies(const float *mdct, float *bandE, int N, int nbBands, int LM) {
    test_compute_band_energies_raw(mdct, bandE, N, nbBands, LM);
    // Subtract eMeans
    for (int band = 0; band < nbBands && band < 25; band++) {
        bandE[band] -= eMeans_libopus[band];
    }
}

// Subtract eMeans to get mean-relative energy
void test_subtract_emeans(float *bandLogE, int nbBands) {
    for (int band = 0; band < nbBands && band < 25; band++) {
        bandLogE[band] -= eMeans_libopus[band];
    }
}

// Get eMeans value for a band
float test_get_emeans(int band) {
    if (band < 0 || band >= 25) return 0.0f;
    return eMeans_libopus[band];
}

// Get eBand boundaries for a given LM
void test_get_ebands_scaled(int LM, int *out_bands, int nbBands) {
    int scale = 1 << LM;
    for (int i = 0; i <= nbBands && i < 22; i++) {
        out_bands[i] = eBands_base[i] * scale;
    }
}

// Compute LINEAR band energy (sqrt of sum of squares) like libopus compute_band_energies()
void test_compute_band_energy_linear(const float *mdct, float *bandE, int N, int nbBands, int LM) {
    int scale = 1 << LM;
    for (int band = 0; band < nbBands && band < 21; band++) {
        int start = eBands_base[band] * scale;
        int end = eBands_base[band + 1] * scale;
        if (end > N) end = N;

        float sum = 1e-27f;  // epsilon like libopus
        for (int i = start; i < end; i++) {
            sum += mdct[i] * mdct[i];
        }
        bandE[band] = sqrtf(sum);  // LINEAR amplitude
    }
}

// Normalize bands like libopus normalise_bands() - floating point version
// X[j] = freq[j] / (epsilon + bandE[i])
void test_normalise_bands(const float *freq, float *X, const float *bandE, int N, int nbBands, int LM) {
    int scale = 1 << LM;
    for (int band = 0; band < nbBands && band < 21; band++) {
        int start = eBands_base[band] * scale;
        int end = eBands_base[band + 1] * scale;
        if (end > N) end = N;

        float g = 1.0f / (1e-27f + bandE[band]);
        for (int j = start; j < end; j++) {
            X[j] = freq[j] * g;
        }
    }
}

*/
import "C"

import (
	"unsafe"
)

// ApplyLibopusPreemphasis applies pre-emphasis using libopus algorithm.
// Input pcm should be in [-1, 1] range.
// Returns pre-emphasized signal at CELT signal scale.
func ApplyLibopusPreemphasis(pcm []float32, coef float32) []float32 {
	n := len(pcm)
	out := make([]float32, n)
	var mem float32 = 0

	C.test_celt_preemphasis(
		(*C.float)(unsafe.Pointer(&pcm[0])),
		(*C.float)(unsafe.Pointer(&out[0])),
		C.int(n),
		C.float(coef),
		(*C.float)(unsafe.Pointer(&mem)),
	)

	return out
}

// ComputeLibopusBandEnergies computes band energies like libopus (with eMeans subtraction).
func ComputeLibopusBandEnergies(mdct []float32, nbBands, N, LM int) []float32 {
	bandE := make([]float32, nbBands)

	C.test_compute_band_energies(
		(*C.float)(unsafe.Pointer(&mdct[0])),
		(*C.float)(unsafe.Pointer(&bandE[0])),
		C.int(N),
		C.int(nbBands),
		C.int(LM),
	)

	return bandE
}

// ComputeLibopusBandEnergiesRaw computes band energies like libopus (without eMeans subtraction).
func ComputeLibopusBandEnergiesRaw(mdct []float32, nbBands, N, LM int) []float32 {
	bandE := make([]float32, nbBands)

	C.test_compute_band_energies_raw(
		(*C.float)(unsafe.Pointer(&mdct[0])),
		(*C.float)(unsafe.Pointer(&bandE[0])),
		C.int(N),
		C.int(nbBands),
		C.int(LM),
	)

	return bandE
}

// GetLibopusEMeans returns the eMeans value for a band.
func GetLibopusEMeans(band int) float32 {
	return float32(C.test_get_emeans(C.int(band)))
}

// GetLibopusEBands returns the eBand boundaries for a given LM.
func GetLibopusEBands(LM, nbBands int) []int {
	bands := make([]int32, nbBands+1)

	C.test_get_ebands_scaled(
		C.int(LM),
		(*C.int)(unsafe.Pointer(&bands[0])),
		C.int(nbBands),
	)

	result := make([]int, nbBands+1)
	for i := range bands {
		result[i] = int(bands[i])
	}
	return result
}

// ComputeLibopusBandEnergyLinear computes LINEAR band energies (sqrt of sum of squares).
// This matches libopus compute_band_energies() which returns sqrt(sum(x^2)).
func ComputeLibopusBandEnergyLinear(mdct []float32, nbBands, N, LM int) []float32 {
	bandE := make([]float32, nbBands)
	if len(mdct) == 0 {
		return bandE
	}

	C.test_compute_band_energy_linear(
		(*C.float)(unsafe.Pointer(&mdct[0])),
		(*C.float)(unsafe.Pointer(&bandE[0])),
		C.int(N),
		C.int(nbBands),
		C.int(LM),
	)

	return bandE
}

// NormaliseLibopusBands normalizes MDCT coefficients like libopus normalise_bands().
// X[j] = freq[j] / (epsilon + bandE[i])
func NormaliseLibopusBands(freq []float32, bandE []float32, N, nbBands, LM int) []float32 {
	X := make([]float32, N)
	if len(freq) == 0 || len(bandE) == 0 {
		return X
	}

	C.test_normalise_bands(
		(*C.float)(unsafe.Pointer(&freq[0])),
		(*C.float)(unsafe.Pointer(&X[0])),
		(*C.float)(unsafe.Pointer(&bandE[0])),
		C.int(N),
		C.int(nbBands),
		C.int(LM),
	)

	return X
}
