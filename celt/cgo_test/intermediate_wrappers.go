//go:build cgo_libopus
// +build cgo_libopus

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

// Compute spectral tilt diff like libopus alloc_trim_analysis does
// Reference: libopus celt/celt_encoder.c lines 922-929
// In float mode: diff += bandLogE[i+c*nbEBands] * (2 + 2*i - end)
// diff /= C*(end-1)
float test_compute_spectral_tilt(const float *bandLogE, int end, int C, int nbEBands) {
    float diff = 0;
    for (int c = 0; c < C; c++) {
        for (int i = 0; i < end-1; i++) {
            // In libopus float mode, SHR32(bandLogE[i], 5) is just bandLogE[i]
            diff += bandLogE[i + c*nbEBands] * (float)(2 + 2*i - end);
        }
    }
    diff /= (float)(C * (end - 1));
    return diff;
}

// Compute full alloc_trim following libopus logic (float mode)
// Returns: trim_index in [0, 10], also outputs intermediate values
int test_compute_alloc_trim(
    const float *bandLogE,
    int end,
    int C,
    int nbEBands,
    int equivRate,
    float tfEstimate,
    float surroundTrim,
    float tonalitySlope,
    float *out_diff,
    float *out_tiltAdjust,
    float *out_baseTrim,
    float *out_tfAdjust)
{
    // Base trim
    float trim = 5.0f;
    if (equivRate < 64000) {
        trim = 4.0f;
    } else if (equivRate < 80000) {
        float frac = (float)(equivRate - 64000) / 16000.0f;
        trim = 4.0f + frac;
    }
    *out_baseTrim = trim;

    // Spectral tilt
    float diff = test_compute_spectral_tilt(bandLogE, end, C, nbEBands);
    *out_diff = diff;

    // Tilt adjust: trim -= max(-2, min(2, (diff+1)/6))
    float tiltAdjust = (diff + 1.0f) / 6.0f;
    if (tiltAdjust < -2.0f) tiltAdjust = -2.0f;
    if (tiltAdjust > 2.0f) tiltAdjust = 2.0f;
    *out_tiltAdjust = tiltAdjust;
    trim -= tiltAdjust;

    // Surround trim (no-op for 0)
    trim -= surroundTrim;

    // TF estimate: trim -= 2*tfEstimate
    float tfAdjust = 2.0f * tfEstimate;
    *out_tfAdjust = tfAdjust;
    trim -= tfAdjust;

    // Tonality slope
    if (tonalitySlope != 0.0f) {
        float tonalAdjust = 2.0f * (tonalitySlope + 0.05f);
        if (tonalAdjust < -2.0f) tonalAdjust = -2.0f;
        if (tonalAdjust > 2.0f) tonalAdjust = 2.0f;
        trim -= tonalAdjust;
    }

    // Final rounding and clamping
    int trimIndex = (int)floorf(0.5f + trim);
    if (trimIndex < 0) trimIndex = 0;
    if (trimIndex > 10) trimIndex = 10;

    return trimIndex;
}

// Compute spreading_decision following libopus logic
// Reference: libopus celt/bands.c spreading_decision()
//
// Returns the spread decision (0=NONE, 1=LIGHT, 2=NORMAL, 3=AGGRESSIVE)
// Also updates state variables through pointers.
int test_spreading_decision(
    const float *X,       // Normalized MDCT coefficients
    int N,                // Total number of coefficients
    int nbBands,          // Number of bands
    int LM,               // Frame size mode
    int *tonal_average,   // State: tonal average (in/out)
    int *spread_decision, // State: last spread decision (in/out)
    int *hf_average,      // State: HF average (in/out)
    int *tapset_decision, // State: tapset decision (in/out)
    const int *spread_weight,  // Per-band weights
    int update_hf,        // Whether to update HF average
    int *out_sum,         // Debug: final sum value
    int *out_sum_before   // Debug: sum before averaging/hysteresis
)
{
    int scale = 1 << LM;

    // Check last band width
    int lastBandWidth = (eBands_base[nbBands] - eBands_base[nbBands-1]) * scale;
    if (lastBandWidth <= 8) {
        *out_sum = 0;
        *out_sum_before = 0;
        return 0;  // SPREAD_NONE
    }

    int sum = 0;
    int nbBandsTotal = 0;
    int hfSum = 0;

    // Process each band
    for (int band = 0; band < nbBands && band < 21; band++) {
        int bandStart = eBands_base[band] * scale;
        int bandEnd = eBands_base[band + 1] * scale;
        int bandN = bandEnd - bandStart;

        if (bandN <= 8) {
            continue;
        }

        if (bandStart >= N) {
            continue;
        }
        if (bandEnd > N) {
            bandEnd = N;
            bandN = bandEnd - bandStart;
        }

        // Count coefficients below thresholds
        int tcount[3] = {0, 0, 0};
        float Nf = (float)bandN;

        for (int j = 0; j < bandN; j++) {
            float x = X[bandStart + j];
            float x2N = x * x * Nf;

            if (x2N < 0.25f) {
                tcount[0]++;
            }
            if (x2N < 0.0625f) {
                tcount[1]++;
            }
            if (x2N < 0.015625f) {
                tcount[2]++;
            }
        }

        // High frequency bands contribution (last 4 bands)
        if (band > nbBands - 4) {
            hfSum += (32 * (tcount[1] + tcount[0])) / bandN;
        }

        // Compute tmp
        int tmp = 0;
        if (2 * tcount[2] >= bandN) tmp++;
        if (2 * tcount[1] >= bandN) tmp++;
        if (2 * tcount[0] >= bandN) tmp++;

        int weight = (spread_weight && band < nbBands) ? spread_weight[band] : 1;
        sum += tmp * weight;
        nbBandsTotal += weight;
    }

    // Update HF average for tapset decision
    if (update_hf) {
        int hfBandCount = 4;  // Last 4 bands
        if (nbBands < 4) hfBandCount = nbBands;
        if (hfBandCount > 0) {
            hfSum = hfSum / hfBandCount;
        }
        *hf_average = (*hf_average + hfSum) >> 1;

        // Adjust for current tapset decision
        int adjustedHF = *hf_average;
        if (*tapset_decision == 2) {
            adjustedHF += 4;
        } else if (*tapset_decision == 0) {
            adjustedHF -= 4;
        }

        // Update tapset decision with hysteresis
        if (adjustedHF > 22) {
            *tapset_decision = 2;
        } else if (adjustedHF > 18) {
            *tapset_decision = 1;
        } else {
            *tapset_decision = 0;
        }
    }

    if (nbBandsTotal <= 0) {
        *out_sum = 256;
        *out_sum_before = 0;
        return 2;  // SPREAD_NORMAL
    }

    // Normalize sum to Q8
    sum = (sum << 8) / nbBandsTotal;
    *out_sum_before = sum;

    // Recursive averaging with previous
    sum = (sum + *tonal_average) >> 1;
    *tonal_average = sum;

    // Apply hysteresis based on last decision
    // sum = (3*sum + (((3-last_decision)<<7) + 64) + 2)>>2
    sum = (3*sum + ((3 - *spread_decision) << 7) + 64 + 2) >> 2;
    *out_sum = sum;

    // Make decision based on thresholds
    int decision;
    if (sum < 80) {
        decision = 3;  // SPREAD_AGGRESSIVE
    } else if (sum < 256) {
        decision = 2;  // SPREAD_NORMAL
    } else if (sum < 384) {
        decision = 1;  // SPREAD_LIGHT
    } else {
        decision = 0;  // SPREAD_NONE
    }

    *spread_decision = decision;
    return decision;
}

// logN values from libopus for 960 samples (LM=3)
// These are from logN400 in static_modes_float.h
static const short logN_LM3[21] = {
    0, 0, 0, 0, 0, 0, 0, 0, 8, 8, 8, 8, 16, 16, 16, 21, 21, 24, 29, 34, 36
};

// Compute noise floor like libopus (float version)
static float compute_noise_floor_float(int i, int lsbDepth, short logN) {
    float eMean = (i < 25) ? eMeans_libopus[i] : 0.0f;
    return 0.0625f * (float)logN + 0.5f + (float)(9 - lsbDepth) - eMean + 0.0062f * (float)((i+5)*(i+5));
}

static inline float minfloat(float a, float b) {
    return (a < b) ? a : b;
}
static inline float maxfloat(float a, float b) {
    return (a > b) ? a : b;
}

// median_of_3_float from libopus
static float median_of_3_float(const float *x) {
    float t0, t1, t2;
    if (x[0] > x[1]) {
        t0 = x[1];
        t1 = x[0];
    } else {
        t0 = x[0];
        t1 = x[1];
    }
    t2 = x[2];
    if (t1 < t2)
        return t1;
    else if (t0 < t2)
        return t2;
    else
        return t0;
}

// median_of_5_float from libopus
static float median_of_5_float(const float *x) {
    float t0, t1, t2, t3, t4;
    float tmp;
    t2 = x[2];
    if (x[0] > x[1]) {
        t0 = x[1];
        t1 = x[0];
    } else {
        t0 = x[0];
        t1 = x[1];
    }
    if (x[3] > x[4]) {
        t3 = x[4];
        t4 = x[3];
    } else {
        t3 = x[3];
        t4 = x[4];
    }
    if (t0 > t3) {
        tmp = t0; t0 = t3; t3 = tmp;
        tmp = t1; t1 = t4; t4 = tmp;
    }
    if (t2 > t1) {
        if (t1 < t3)
            return minfloat(t2, t3);
        else
            return minfloat(t4, t1);
    } else {
        if (t2 < t3)
            return minfloat(t1, t3);
        else
            return minfloat(t2, t4);
    }
}

// Compute follower using libopus algorithm (float version)
// This matches celt_encoder.c lines 1123-1166
void compute_follower_libopus(
    const float *bandLogE2,  // Secondary MDCT band energies
    const float *oldBandE,   // Previous frame band energies (can be NULL)
    int end,
    int lsbDepth,
    int lm,
    float *follower,         // Output: final follower values
    int *out_last,           // Output: last variable value
    float *f_after_forward,  // Output: follower after forward pass
    float *f_after_backward, // Output: follower after backward pass
    float *f_after_median,   // Output: follower after median filter
    float *noise_floor       // Output: noise floor values
) {
    int i;
    float bandLogE3[25];
    float offset = 1.0f;

    // Compute noise floor
    for (i = 0; i < end; i++) {
        short logN = (i < 21) ? logN_LM3[i] : 0;
        noise_floor[i] = compute_noise_floor_float(i, lsbDepth, logN);
    }

    // Copy bandLogE2 to bandLogE3
    for (i = 0; i < end; i++) {
        bandLogE3[i] = bandLogE2[i];
    }

    // For LM=0, take max with oldBandE for first 8 bands
    if (lm == 0 && oldBandE != NULL) {
        int limit = (8 < end) ? 8 : end;
        for (i = 0; i < limit; i++) {
            if (oldBandE[i] > bandLogE3[i]) {
                bandLogE3[i] = oldBandE[i];
            }
        }
    }

    // Forward pass
    int last = 0;
    follower[0] = bandLogE3[0];
    for (i = 1; i < end; i++) {
        if (bandLogE3[i] > bandLogE3[i-1] + 0.5f) {
            last = i;
        }
        follower[i] = minfloat(follower[i-1] + 1.5f, bandLogE3[i]);
    }
    *out_last = last;

    // Copy follower after forward pass
    for (i = 0; i < end; i++) {
        f_after_forward[i] = follower[i];
    }

    // Backward pass: from last-1 down to 0
    for (i = last - 1; i >= 0; i--) {
        follower[i] = minfloat(follower[i], minfloat(follower[i+1] + 2.0f, bandLogE3[i]));
    }

    // Copy follower after backward pass
    for (i = 0; i < end; i++) {
        f_after_backward[i] = follower[i];
    }

    // Median filter for middle bands
    for (i = 2; i < end - 2; i++) {
        float medVal = median_of_5_float(&bandLogE3[i-2]);
        follower[i] = maxfloat(follower[i], medVal - offset);
    }

    // Handle edge bands with median of 3
    if (end >= 3) {
        float tmp = median_of_3_float(&bandLogE3[0]) - offset;
        follower[0] = maxfloat(follower[0], tmp);
        follower[1] = maxfloat(follower[1], tmp);

        tmp = median_of_3_float(&bandLogE3[end-3]) - offset;
        follower[end-2] = maxfloat(follower[end-2], tmp);
        follower[end-1] = maxfloat(follower[end-1], tmp);
    }

    // Copy follower after median filter
    for (i = 0; i < end; i++) {
        f_after_median[i] = follower[i];
    }

    // Clamp to noise floor
    for (i = 0; i < end; i++) {
        follower[i] = maxfloat(follower[i], noise_floor[i]);
    }
}

// eBands for 48kHz mode (base, before LM scaling)
static const int16_t eBands_base_48k[22] = {
    0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
    12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
    78, 100
};

// DynallocTraceResult holds all traced values from dynalloc_analysis
typedef struct {
    // Input energies
    float bandLogE[25];
    float bandLogE2[25];

    // Noise floor per band
    float noise_floor[25];

    // Follower values at each stage
    float follower_after_forward[25];
    float follower_after_backward[25];
    float follower_after_median[25];
    float follower_after_clamp[25];
    float follower_final[25];

    // Boost values
    int boost[25];
    int boost_bits[25];

    // Final offsets
    int offsets[25];

    // Summary values
    float maxDepth;
    int tot_boost;
    int importance[25];
    int spread_weight[25];

    // Debug info
    int last;
    int end;
} DynallocTraceResult;

// Trace dynalloc analysis step by step, matching libopus float mode logic
void trace_dynalloc_analysis(
    const float *bandLogE,
    const float *bandLogE2,
    int nbEBands,
    int start,
    int end,
    int C,
    int lsb_depth,
    int LM,
    int effectiveBytes,
    int isTransient,
    int vbr,
    int constrained_vbr,
    DynallocTraceResult *result)
{
    int i, c;
    int BITRES = 3;

    memset(result, 0, sizeof(*result));
    result->end = end;

    // Copy inputs
    for (i = 0; i < end && i < 25; i++) {
        result->bandLogE[i] = bandLogE[i];
        if (bandLogE2) {
            result->bandLogE2[i] = bandLogE2[i];
        } else {
            result->bandLogE2[i] = bandLogE[i];
        }
    }

    // Get logN for this LM (use LM=3 values for now)
    const short *logN = logN_LM3;

    // Compute noise floor per band
    // Reference: libopus celt_encoder.c lines 1071-1076
    for (i = 0; i < end; i++) {
        result->noise_floor[i] = compute_noise_floor_float(i, lsb_depth, logN[i]);
    }

    // Compute maxDepth
    result->maxDepth = -31.9f;
    c = 0;
    do {
        for (i = 0; i < end; i++) {
            float depth = bandLogE[c * nbEBands + i] - result->noise_floor[i];
            if (depth > result->maxDepth) {
                result->maxDepth = depth;
            }
        }
    } while (++c < C);

    // Compute spread_weight using masking model
    {
        float mask[25], sig[25];
        for (i = 0; i < end; i++) {
            mask[i] = bandLogE[i] - result->noise_floor[i];
        }
        if (C == 2) {
            for (i = 0; i < end; i++) {
                float ch2 = bandLogE[nbEBands + i] - result->noise_floor[i];
                if (ch2 > mask[i]) mask[i] = ch2;
            }
        }
        memcpy(sig, mask, sizeof(sig));

        // Forward masking
        for (i = 1; i < end; i++) {
            if (mask[i-1] - 2.0f > mask[i]) {
                mask[i] = mask[i-1] - 2.0f;
            }
        }
        // Backward masking
        for (i = end - 2; i >= 0; i--) {
            if (mask[i+1] - 3.0f > mask[i]) {
                mask[i] = mask[i+1] - 3.0f;
            }
        }

        // Compute SMR and spread_weight
        for (i = 0; i < end; i++) {
            float maskThresh = maxfloat(0, maxfloat(result->maxDepth - 12.0f, mask[i]));
            float smr = sig[i] - maskThresh;
            int shift = (int)fminf(5, fmaxf(0, -(int)floorf(0.5f + smr)));
            result->spread_weight[i] = 32 >> shift;
        }
    }

    // Dynamic allocation processing
    // Reference: libopus lines 1121-1265
    int minBytes = 30 + 5 * LM;
    if (effectiveBytes >= minBytes) {
        float follower[25];
        float bandLogE3[25];

        int last = 0;
        c = 0;
        do {
            // Copy bandLogE2 to bandLogE3
            for (i = 0; i < end; i++) {
                if (bandLogE2) {
                    bandLogE3[i] = bandLogE2[c * nbEBands + i];
                } else {
                    bandLogE3[i] = bandLogE[c * nbEBands + i];
                }
            }

            // For LM=0, take max with oldBandE for first 8 bands (not implemented here)

            follower[0] = bandLogE3[0];

            // Forward pass
            for (i = 1; i < end; i++) {
                if (bandLogE3[i] > bandLogE3[i-1] + 0.5f) {
                    last = i;
                }
                follower[i] = minfloat(follower[i-1] + 1.5f, bandLogE3[i]);
            }
            result->last = last;

            // Store after forward pass
            for (i = 0; i < end; i++) {
                result->follower_after_forward[i] = follower[i];
            }

            // Backward pass
            for (i = last - 1; i >= 0; i--) {
                follower[i] = minfloat(follower[i], minfloat(follower[i+1] + 2.0f, bandLogE3[i]));
            }

            // Store after backward pass
            for (i = 0; i < end; i++) {
                result->follower_after_backward[i] = follower[i];
            }

            // Median filter
            float offset = 1.0f;
            for (i = 2; i < end - 2; i++) {
                float med = median_of_5_float(&bandLogE3[i-2]) - offset;
                if (med > follower[i]) follower[i] = med;
            }
            // Edge bands
            if (end >= 3) {
                float tmp = median_of_3_float(&bandLogE3[0]) - offset;
                if (tmp > follower[0]) follower[0] = tmp;
                if (tmp > follower[1]) follower[1] = tmp;
                tmp = median_of_3_float(&bandLogE3[end-3]) - offset;
                if (tmp > follower[end-2]) follower[end-2] = tmp;
                if (tmp > follower[end-1]) follower[end-1] = tmp;
            }

            // Store after median
            for (i = 0; i < end; i++) {
                result->follower_after_median[i] = follower[i];
            }

            // Clamp to noise floor
            for (i = 0; i < end; i++) {
                if (result->noise_floor[i] > follower[i]) {
                    follower[i] = result->noise_floor[i];
                }
            }

            // Store after clamp
            for (i = 0; i < end; i++) {
                result->follower_after_clamp[i] = follower[i];
            }
        } while (++c < C);

        // Convert follower to boost values (mono case: follower = max(0, bandLogE - follower))
        float follower_final[25];
        if (C == 2) {
            // Stereo cross-talk handling (simplified)
            for (i = start; i < end; i++) {
                follower_final[i] = maxfloat(0, bandLogE[i] - result->follower_after_clamp[i]);
            }
        } else {
            for (i = start; i < end; i++) {
                follower_final[i] = maxfloat(0, bandLogE[i] - result->follower_after_clamp[i]);
            }
        }

        // Compute importance
        for (i = start; i < end; i++) {
            float expArg = minfloat(follower_final[i], 4.0f);
            result->importance[i] = (int)floorf(0.5f + 13.0f * powf(2.0f, expArg));
        }

        // For non-transient CBR/CVBR, halve the follower
        if ((!vbr || constrained_vbr) && !isTransient) {
            for (i = start; i < end; i++) {
                follower_final[i] *= 0.5f;
            }
        }

        // Frequency-dependent weighting
        for (i = start; i < end; i++) {
            if (i < 8) follower_final[i] *= 2.0f;
            if (i >= 12) follower_final[i] *= 0.5f;
        }

        // Store final follower
        for (i = 0; i < end; i++) {
            result->follower_final[i] = follower_final[i];
        }

        // Compute offsets and boost
        int tot_boost = 0;
        int scale = 1 << LM;
        for (i = start; i < end; i++) {
            int width;
            int boost;
            int boost_bits;

            // Clamp follower to 4
            follower_final[i] = minfloat(follower_final[i], 4.0f);

            // In float mode, SHR32(follower[i], 8) is just follower[i]
            float f_scaled = follower_final[i];

            // Get band width (scaled by LM)
            width = C * (eBands_base_48k[i+1] - eBands_base_48k[i]) * scale;

            if (width < 6) {
                boost = (int)f_scaled;
                boost_bits = boost * width << BITRES;
            } else if (width > 48) {
                boost = (int)(f_scaled * 8);
                boost_bits = (boost * width << BITRES) / 8;
            } else {
                boost = (int)(f_scaled * width / 6);
                boost_bits = boost * 6 << BITRES;
            }

            result->boost[i] = boost;
            result->boost_bits[i] = boost_bits;

            // Check CBR/CVBR limit
            if ((!vbr || (constrained_vbr && !isTransient))
                    && (tot_boost + boost_bits) >> BITRES >> 3 > 2 * effectiveBytes / 3) {
                int cap = (2 * effectiveBytes / 3) << BITRES << 3;
                result->offsets[i] = cap - tot_boost;
                tot_boost = cap;
                break;
            } else {
                result->offsets[i] = boost;
                tot_boost += boost_bits;
            }
        }
        result->tot_boost = tot_boost;
    } else {
        // Not enough bits for dynalloc
        for (i = start; i < end; i++) {
            result->importance[i] = 13;
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

// AllocTrimResult holds the results of alloc_trim computation
type AllocTrimResult struct {
	TrimIndex  int
	Diff       float32 // Spectral tilt diff
	TiltAdjust float32 // Clamped (diff+1)/6
	BaseTrim   float32 // Base trim from equiv_rate
	TfAdjust   float32 // 2*tfEstimate
}

// ComputeLibopusAllocTrim computes allocation trim following libopus logic
func ComputeLibopusAllocTrim(bandLogE []float32, end, channels, nbEBands, equivRate int, tfEstimate, surroundTrim, tonalitySlope float32) AllocTrimResult {
	if len(bandLogE) == 0 {
		return AllocTrimResult{}
	}

	var outDiff, outTiltAdjust, outBaseTrim, outTfAdjust C.float

	trimIndex := C.test_compute_alloc_trim(
		(*C.float)(unsafe.Pointer(&bandLogE[0])),
		C.int(end),
		C.int(channels),
		C.int(nbEBands),
		C.int(equivRate),
		C.float(tfEstimate),
		C.float(surroundTrim),
		C.float(tonalitySlope),
		&outDiff,
		&outTiltAdjust,
		&outBaseTrim,
		&outTfAdjust,
	)

	return AllocTrimResult{
		TrimIndex:  int(trimIndex),
		Diff:       float32(outDiff),
		TiltAdjust: float32(outTiltAdjust),
		BaseTrim:   float32(outBaseTrim),
		TfAdjust:   float32(outTfAdjust),
	}
}

// ComputeLibopusSpectralTilt computes just the spectral tilt diff
func ComputeLibopusSpectralTilt(bandLogE []float32, end, channels, nbEBands int) float32 {
	if len(bandLogE) == 0 {
		return 0
	}

	return float32(C.test_compute_spectral_tilt(
		(*C.float)(unsafe.Pointer(&bandLogE[0])),
		C.int(end),
		C.int(channels),
		C.int(nbEBands),
	))
}

// SpreadDecisionResult holds the results of spread decision computation
type SpreadDecisionResult struct {
	Decision       int // 0=NONE, 1=LIGHT, 2=NORMAL, 3=AGGRESSIVE
	Sum            int // Final sum value after hysteresis
	SumBefore      int // Sum before averaging/hysteresis
	TonalAverage   int // Updated tonal average state
	SpreadDecision int // Updated spread decision state
	HfAverage      int // Updated HF average state
	TapsetDecision int // Updated tapset decision state
}

// ComputeLibopusSpreadDecision computes spread decision following libopus logic.
// X: normalized MDCT coefficients
// N: total number of coefficients
// nbBands: number of bands
// LM: frame size mode (0-3)
// spreadWeight: per-band weights (can be nil for uniform weights)
// tonalAverage, spreadDecision, hfAverage, tapsetDecision: initial state values
// updateHF: whether to update HF average for tapset
func ComputeLibopusSpreadDecision(
	X []float32,
	N, nbBands, LM int,
	spreadWeight []int,
	tonalAverage, spreadDecisionState, hfAverage, tapsetDecision int,
	updateHF bool,
) SpreadDecisionResult {
	if len(X) == 0 {
		return SpreadDecisionResult{Decision: 2} // SPREAD_NORMAL
	}

	// Convert spread weights to C int array
	var weightsPtr *C.int
	cWeights := make([]C.int, nbBands)
	if spreadWeight != nil && len(spreadWeight) >= nbBands {
		for i := 0; i < nbBands; i++ {
			cWeights[i] = C.int(spreadWeight[i])
		}
		weightsPtr = &cWeights[0]
	}

	// State variables
	cTonalAvg := C.int(tonalAverage)
	cSpreadDec := C.int(spreadDecisionState)
	cHfAvg := C.int(hfAverage)
	cTapset := C.int(tapsetDecision)

	// Debug outputs
	var outSum, outSumBefore C.int

	updateHFInt := 0
	if updateHF {
		updateHFInt = 1
	}

	decision := C.test_spreading_decision(
		(*C.float)(unsafe.Pointer(&X[0])),
		C.int(N),
		C.int(nbBands),
		C.int(LM),
		&cTonalAvg,
		&cSpreadDec,
		&cHfAvg,
		&cTapset,
		weightsPtr,
		C.int(updateHFInt),
		&outSum,
		&outSumBefore,
	)

	return SpreadDecisionResult{
		Decision:       int(decision),
		Sum:            int(outSum),
		SumBefore:      int(outSumBefore),
		TonalAverage:   int(cTonalAvg),
		SpreadDecision: int(cSpreadDec),
		HfAverage:      int(cHfAvg),
		TapsetDecision: int(cTapset),
	}
}

// GetLibopusSpreadDecision is a placeholder that returns -1.
// The actual spread decision extraction from libopus packets requires complex
// decoding that we don't implement here. Use ComputeLibopusSpreadDecision instead
// to compute the spread decision using the libopus algorithm directly.
func GetLibopusSpreadDecision(pcm []float32, frameSize, sampleRate, bitrate int) int {
	// We can't easily extract spread decision from an encoded packet
	// because it requires full decoding of the packet header.
	// Use ComputeLibopusSpreadDecision with normalized coefficients instead.
	return -1
}

// GetLibopusNormalizedCoeffs gets the normalized MDCT coefficients from libopus.
// Returns nil if not available.
func GetLibopusNormalizedCoeffs(pcm []float32, frameSize, sampleRate, bitrate int) []float32 {
	// We can't easily extract normalized coefficients from libopus encoder
	// Instead, we compute them using our libopus-compatible functions

	// Apply pre-emphasis
	preemph := ApplyLibopusPreemphasis(pcm, 0.85)

	// For first frame, libopus uses short blocks (transient mode)
	// with 8 short MDCTs interleaved
	mode := 3 // LM=3 for 960 samples
	nbBands := 21
	shortBlocks := 8

	// We need to compute MDCT - but we don't have a direct libopus MDCT wrapper
	// So return nil and use gopus MDCT with libopus pre-emphasis
	_ = preemph
	_ = mode
	_ = nbBands
	_ = shortBlocks

	return nil
}

// FollowerTrace contains traced values from follower computation
type FollowerTrace struct {
	Last           int
	NoiseFloor     []float64
	FAfterForward  []float64
	FAfterBackward []float64
	FAfterMedian   []float64
	Follower       []float64
}

// ComputeFollowerLibopus computes the follower using libopus algorithm
func ComputeFollowerLibopus(bandLogE2, oldBandE []float64, end, lsbDepth, lm int) FollowerTrace {
	// Convert to float32 for C
	bandLogE232 := make([]float32, len(bandLogE2))
	for i := range bandLogE2 {
		bandLogE232[i] = float32(bandLogE2[i])
	}

	var oldBandE32 []float32
	var oldBandEPtr *C.float
	if oldBandE != nil {
		oldBandE32 = make([]float32, len(oldBandE))
		for i := range oldBandE {
			oldBandE32[i] = float32(oldBandE[i])
		}
		oldBandEPtr = (*C.float)(unsafe.Pointer(&oldBandE32[0]))
	}

	follower := make([]float32, end)
	fAfterForward := make([]float32, end)
	fAfterBackward := make([]float32, end)
	fAfterMedian := make([]float32, end)
	noiseFloor := make([]float32, end)
	var outLast C.int

	C.compute_follower_libopus(
		(*C.float)(unsafe.Pointer(&bandLogE232[0])),
		oldBandEPtr,
		C.int(end),
		C.int(lsbDepth),
		C.int(lm),
		(*C.float)(unsafe.Pointer(&follower[0])),
		&outLast,
		(*C.float)(unsafe.Pointer(&fAfterForward[0])),
		(*C.float)(unsafe.Pointer(&fAfterBackward[0])),
		(*C.float)(unsafe.Pointer(&fAfterMedian[0])),
		(*C.float)(unsafe.Pointer(&noiseFloor[0])),
	)

	// Convert back to float64
	result := FollowerTrace{
		Last:           int(outLast),
		NoiseFloor:     make([]float64, end),
		FAfterForward:  make([]float64, end),
		FAfterBackward: make([]float64, end),
		FAfterMedian:   make([]float64, end),
		Follower:       make([]float64, end),
	}

	for i := 0; i < end; i++ {
		result.NoiseFloor[i] = float64(noiseFloor[i])
		result.FAfterForward[i] = float64(fAfterForward[i])
		result.FAfterBackward[i] = float64(fAfterBackward[i])
		result.FAfterMedian[i] = float64(fAfterMedian[i])
		result.Follower[i] = float64(follower[i])
	}

	return result
}

// DynallocTraceResult holds all traced values from dynalloc_analysis.
type DynallocTraceResult struct {
	BandLogE  [25]float64
	BandLogE2 [25]float64

	NoiseFloor [25]float64

	FollowerAfterForward  [25]float64
	FollowerAfterBackward [25]float64
	FollowerAfterMedian   [25]float64
	FollowerAfterClamp    [25]float64
	FollowerFinal         [25]float64

	Boost     [25]int
	BoostBits [25]int

	Offsets [25]int

	MaxDepth     float64
	TotBoost     int
	Importance   [25]int
	SpreadWeight [25]int

	Last int
	End  int
}

// TraceDynallocAnalysis traces the dynalloc analysis step by step using libopus-compatible logic.
func TraceDynallocAnalysis(
	bandLogE, bandLogE2 []float64,
	nbBands, start, end, channels, lsbDepth, lm, effectiveBytes int,
	isTransient, vbr, constrainedVBR bool,
) DynallocTraceResult {
	var cResult C.DynallocTraceResult

	// Convert to float32 for C
	bandLogE32 := make([]float32, len(bandLogE))
	for i := range bandLogE {
		bandLogE32[i] = float32(bandLogE[i])
	}

	var bandLogE2Ptr *C.float
	var bandLogE232 []float32
	if bandLogE2 != nil && len(bandLogE2) > 0 {
		bandLogE232 = make([]float32, len(bandLogE2))
		for i := range bandLogE2 {
			bandLogE232[i] = float32(bandLogE2[i])
		}
		bandLogE2Ptr = (*C.float)(unsafe.Pointer(&bandLogE232[0]))
	}

	isTransientInt := 0
	if isTransient {
		isTransientInt = 1
	}
	vbrInt := 0
	if vbr {
		vbrInt = 1
	}
	constrainedVBRInt := 0
	if constrainedVBR {
		constrainedVBRInt = 1
	}

	C.trace_dynalloc_analysis(
		(*C.float)(unsafe.Pointer(&bandLogE32[0])),
		bandLogE2Ptr,
		C.int(nbBands),
		C.int(start),
		C.int(end),
		C.int(channels),
		C.int(lsbDepth),
		C.int(lm),
		C.int(effectiveBytes),
		C.int(isTransientInt),
		C.int(vbrInt),
		C.int(constrainedVBRInt),
		&cResult,
	)

	// Convert C struct to Go struct
	result := DynallocTraceResult{
		MaxDepth: float64(cResult.maxDepth),
		TotBoost: int(cResult.tot_boost),
		Last:     int(cResult.last),
		End:      int(cResult.end),
	}

	for i := 0; i < 25; i++ {
		result.BandLogE[i] = float64(cResult.bandLogE[i])
		result.BandLogE2[i] = float64(cResult.bandLogE2[i])
		result.NoiseFloor[i] = float64(cResult.noise_floor[i])
		result.FollowerAfterForward[i] = float64(cResult.follower_after_forward[i])
		result.FollowerAfterBackward[i] = float64(cResult.follower_after_backward[i])
		result.FollowerAfterMedian[i] = float64(cResult.follower_after_median[i])
		result.FollowerAfterClamp[i] = float64(cResult.follower_after_clamp[i])
		result.FollowerFinal[i] = float64(cResult.follower_final[i])
		result.Boost[i] = int(cResult.boost[i])
		result.BoostBits[i] = int(cResult.boost_bits[i])
		result.Offsets[i] = int(cResult.offsets[i])
		result.Importance[i] = int(cResult.importance[i])
		result.SpreadWeight[i] = int(cResult.spread_weight[i])
	}

	return result
}
