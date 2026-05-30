/*
 * libopus_custom_oracle.c — binary-protocol oracle for Opus Custom encode/decode.
 *
 * Requires a libopus build with --enable-custom-modes (CUSTOM_MODES + the
 * Opus Custom API); link against its .libs/libopus.a. The gopus harness builds
 * that tree via LIBOPUS_ENABLE_CUSTOM=1 tools/ensure_libopus.sh
 * (-> tmp_check/opus-1.6.1-custom) and compiles this file against it through
 * libopustest.BuildCHelper(CHelperConfig{CustomRef: true, ...}).
 *
 * Each case creates an OpusCustomMode for the requested (Fs, frame_size),
 * encodes one frame with opus_custom_encode_float, then decodes the produced
 * packet back with opus_custom_decode_float. It returns both the packet bytes
 * and the decoded PCM so the Go side can assert bit/sample-exact parity against
 * gopus celt/custom.
 *
 * Protocol (little-endian):
 *   STDIN:
 *     "GCCO"           4 bytes magic
 *     uint32 N         number of encode cases
 *     for each case:
 *       uint32 Fs
 *       uint32 frame_size
 *       uint32 channels
 *       uint32 maxBytes
 *       uint32 nSamples  (= frame_size * channels)
 *       nSamples * float32  PCM
 *
 *   STDOUT:
 *     "GCCO"           4 bytes magic
 *     uint32 N         number of results
 *     for each result:
 *       int32  status    (>=0 packet length, <0 libopus error code)
 *       uint32 encRange  encoder final range (OPUS_GET_FINAL_RANGE)
 *       uint32 decRange  decoder final range (OPUS_GET_FINAL_RANGE)
 *       uint32 packetLen
 *       packetLen bytes
 *       uint32 nDecoded  (= frame_size * channels, 0 on failure)
 *       nDecoded * float32  decoded PCM
 *       --- mode geometry (opus_custom_mode_create result) ---
 *       int32  overlap
 *       int32  nbEBands
 *       int32  effEBands
 *       int32  maxLM
 *       int32  nbShortMdcts
 *       int32  shortMdctSize
 *       4 * float32  preemph[0..3]
 *       int32  nEBandEdges (= nbEBands+1)
 *       nEBandEdges * int32  eBands[]
 *       int32  nLogN (= nbEBands)
 *       nLogN * int32  logN[]
 *       int32  nAlloc (= 11*nbEBands)
 *       nAlloc * int32  allocVectors[]
 *       int32  nCacheIndex (= (maxLM+2)*nbEBands)
 *       nCacheIndex * int32  cacheIndex[]
 *       int32  nCacheBits (= cache.size)
 *       nCacheBits * int32  cacheBits[]
 *       int32  nCacheCaps (= (maxLM+1)*2*nbEBands)
 *       nCacheCaps * int32  cacheCaps[]
 *
 * Reference: libopus include/opus_custom.h, celt/celt_encoder.c, celt/celt_decoder.c.
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_custom.h"
#include "opus_defines.h"
/* celt.h provides CELT_SET_SIGNALLING, which is an internal (non-public) CTL.
 * modes.h provides the full OpusCustomMode (CELTMode) struct so the oracle can
 * report the mode geometry opus_custom_mode_create() derives.
 * The helper build adds -I <ref>/celt via CHelperConfig.RefIncludes. */
#include "celt.h"
#include "modes.h"

#define INPUT_MAGIC  "GCCO"
#define OUTPUT_MAGIC "GCCO"
#define MAX_FRAME    2048
#define MAX_PACKET   1500

static int set_binary_stdio(void) {
#ifdef _WIN32
    if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
    if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
    return 1;
}

static int read_exact(void *dst, size_t n) {
    return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
    return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) { return read_exact(out, 4); }
static int write_u32(uint32_t v)   { return write_exact(&v, 4); }
static int write_i32(int32_t v)    { return write_exact(&v, 4); }

static int read_f32(float *out) {
    uint32_t bits;
    if (!read_u32(&bits)) return 0;
    memcpy(out, &bits, 4);
    return 1;
}

static int write_f32(float v) {
    uint32_t bits;
    memcpy(&bits, &v, 4);
    return write_u32(bits);
}

/* Emit a failure result: negative status, empty packet, no decoded PCM,
 * and zeroed mode geometry (so the per-result frame stays fixed-shape). */
static void emit_failure(int32_t status) {
    write_i32(status);
    write_u32(0); /* encRange */
    write_u32(0); /* decRange */
    write_u32(0); /* packetLen */
    write_u32(0); /* nDecoded */
    /* mode geometry: overlap, nbEBands, effEBands, maxLM, nbShortMdcts,
     * shortMdctSize, preemph[4], nEBandEdges, nLogN — all zero. */
    for (int i = 0; i < 6; i++) write_i32(0);
    for (int i = 0; i < 4; i++) write_f32(0.0f);
    write_i32(0); /* nEBandEdges */
    write_i32(0); /* nLogN */
    write_i32(0); /* nAlloc */
    write_i32(0); /* nCacheIndex */
    write_i32(0); /* nCacheBits */
    write_i32(0); /* nCacheCaps */
}

int main(void) {
    if (!set_binary_stdio()) { fprintf(stderr, "binary stdio failed\n"); return 1; }

    char magic[4];
    if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
        fprintf(stderr, "bad input magic\n"); return 1;
    }

    uint32_t n_cases;
    if (!read_u32(&n_cases)) { fprintf(stderr, "read N\n"); return 1; }

    if (!write_exact(OUTPUT_MAGIC, 4)) { fprintf(stderr, "write magic\n"); return 1; }
    if (!write_u32(n_cases))           { fprintf(stderr, "write N\n"); return 1; }

    for (uint32_t c = 0; c < n_cases; c++) {
        uint32_t Fs, frame_size, channels, maxBytes, nSamples;
        if (!read_u32(&Fs)         || !read_u32(&frame_size) ||
            !read_u32(&channels)   || !read_u32(&maxBytes)   ||
            !read_u32(&nSamples)) {
            fprintf(stderr, "case %u: read header\n", c); return 1;
        }

        if (nSamples > (uint32_t)MAX_FRAME * 2) {
            fprintf(stderr, "case %u: nSamples=%u too large\n", c, nSamples); return 1;
        }

        float pcm[MAX_FRAME * 2];
        for (uint32_t i = 0; i < nSamples; i++) {
            if (!read_f32(&pcm[i])) {
                fprintf(stderr, "case %u: read pcm[%u]\n", c, i); return 1;
            }
        }

        int err = OPUS_OK;
        OpusCustomMode *mode = opus_custom_mode_create((opus_int32)Fs, (int)frame_size, &err);
        if (!mode || err != OPUS_OK) {
            fprintf(stderr, "case %u: opus_custom_mode_create(%u,%u) error %d\n",
                    c, Fs, frame_size, err);
            emit_failure(err != OPUS_OK ? err : OPUS_INTERNAL_ERROR);
            continue;
        }

        OpusCustomEncoder *enc = opus_custom_encoder_create(mode, (int)channels, &err);
        if (!enc || err != OPUS_OK) {
            fprintf(stderr, "case %u: encoder create error %d\n", c, err);
            opus_custom_mode_destroy(mode);
            emit_failure(err != OPUS_OK ? err : OPUS_INTERNAL_ERROR);
            continue;
        }

        /* Configure to match gopus celt/custom encoder defaults: CBR,
         * complexity 9, LSB depth 16, no implicit signalling. */
        opus_custom_encoder_ctl(enc, OPUS_SET_VBR(0));
        opus_custom_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0));
        opus_custom_encoder_ctl(enc, OPUS_SET_COMPLEXITY(9));
        opus_custom_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(16));
        opus_custom_encoder_ctl(enc, CELT_SET_SIGNALLING(0));

        unsigned char packet[MAX_PACKET];
        int sz = opus_custom_encode_float(enc, pcm, (int)frame_size,
                                          packet, (int)maxBytes);
        opus_uint32 encRange = 0;
        opus_custom_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&encRange));
        opus_custom_encoder_destroy(enc);

        if (sz < 0) {
            fprintf(stderr, "case %u: encode error %d\n", c, sz);
            opus_custom_mode_destroy(mode);
            emit_failure(sz);
            continue;
        }

        /* Decode the packet we just produced. */
        OpusCustomDecoder *dec = opus_custom_decoder_create(mode, (int)channels, &err);
        float decoded[MAX_FRAME * 2];
        uint32_t nDecoded = 0;
        opus_uint32 decRange = 0;
        if (!dec || err != OPUS_OK) {
            fprintf(stderr, "case %u: decoder create error %d\n", c, err);
        } else {
            /* The encoder disabled implicit frame-size signalling, so the
             * decoder must too, otherwise it infers the wrong frame size. */
            opus_custom_decoder_ctl(dec, CELT_SET_SIGNALLING(0));
            int dn = opus_custom_decode_float(dec, packet, sz, decoded, (int)frame_size);
            opus_custom_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&decRange));
            opus_custom_decoder_destroy(dec);
            if (dn > 0) {
                nDecoded = (uint32_t)dn * channels;
            }
        }
        /* Snapshot the mode geometry before destroying the mode. These are the
         * tables opus_custom_mode_create() derives for this (Fs, frame_size),
         * which the gopus celt/custom control plane must reproduce. */
        int g_overlap       = mode->overlap;
        int g_nbEBands      = mode->nbEBands;
        int g_effEBands     = mode->effEBands;
        int g_maxLM         = mode->maxLM;
        int g_nbShortMdcts  = mode->nbShortMdcts;
        int g_shortMdctSize = mode->shortMdctSize;
        float g_preemph[4];
        for (int i = 0; i < 4; i++) g_preemph[i] = (float)mode->preemph[i];
        int g_nEdges = g_nbEBands + 1;
        opus_int16 g_eBands[64];
        opus_int16 g_logN[64];
        if (g_nEdges > 64) g_nEdges = 64;
        for (int i = 0; i < g_nEdges; i++) g_eBands[i] = mode->eBands[i];
        int g_nLogN = g_nbEBands < 64 ? g_nbEBands : 64;
        for (int i = 0; i < g_nLogN; i++) g_logN[i] = mode->logN[i];

        /* allocVectors: 11 (== mode->nbAllocVectors) rows of nbEBands columns. */
        int g_nAlloc = mode->nbAllocVectors * g_nbEBands;
        /* cache.index: (maxLM+2) * nbEBands; cache.bits: cache.size;
         * cache.caps: (maxLM+1) * 2 * nbEBands. */
        int g_nCacheIndex = (g_maxLM + 2) * g_nbEBands;
        int g_nCacheBits  = mode->cache.size;
        int g_nCacheCaps  = (g_maxLM + 1) * 2 * g_nbEBands;
        static int32_t g_alloc[11 * 64];
        static int32_t g_cidx[6 * 64];
        static int32_t g_cbits[4096];
        static int32_t g_ccaps[4 * 2 * 64];
        if (g_nAlloc > (int)(sizeof(g_alloc)/sizeof(g_alloc[0]))) g_nAlloc = sizeof(g_alloc)/sizeof(g_alloc[0]);
        if (g_nCacheIndex > (int)(sizeof(g_cidx)/sizeof(g_cidx[0]))) g_nCacheIndex = sizeof(g_cidx)/sizeof(g_cidx[0]);
        if (g_nCacheBits > (int)(sizeof(g_cbits)/sizeof(g_cbits[0]))) g_nCacheBits = sizeof(g_cbits)/sizeof(g_cbits[0]);
        if (g_nCacheCaps > (int)(sizeof(g_ccaps)/sizeof(g_ccaps[0]))) g_nCacheCaps = sizeof(g_ccaps)/sizeof(g_ccaps[0]);
        for (int i = 0; i < g_nAlloc; i++)      g_alloc[i] = (int32_t)mode->allocVectors[i];
        for (int i = 0; i < g_nCacheIndex; i++) g_cidx[i]  = (int32_t)mode->cache.index[i];
        for (int i = 0; i < g_nCacheBits; i++)  g_cbits[i] = (int32_t)mode->cache.bits[i];
        for (int i = 0; i < g_nCacheCaps; i++)  g_ccaps[i] = (int32_t)mode->cache.caps[i];

        opus_custom_mode_destroy(mode);

        write_i32(sz);
        write_u32((uint32_t)encRange);
        write_u32((uint32_t)decRange);
        if (!write_u32((uint32_t)sz) || !write_exact(packet, (size_t)sz)) {
            fprintf(stderr, "case %u: write packet\n", c); return 1;
        }
        write_u32(nDecoded);
        for (uint32_t i = 0; i < nDecoded; i++) {
            if (!write_f32(decoded[i])) {
                fprintf(stderr, "case %u: write decoded[%u]\n", c, i); return 1;
            }
        }

        write_i32(g_overlap);
        write_i32(g_nbEBands);
        write_i32(g_effEBands);
        write_i32(g_maxLM);
        write_i32(g_nbShortMdcts);
        write_i32(g_shortMdctSize);
        for (int i = 0; i < 4; i++) write_f32(g_preemph[i]);
        write_i32(g_nEdges);
        for (int i = 0; i < g_nEdges; i++) write_i32((int32_t)g_eBands[i]);
        write_i32(g_nLogN);
        for (int i = 0; i < g_nLogN; i++) write_i32((int32_t)g_logN[i]);
        write_i32(g_nAlloc);
        for (int i = 0; i < g_nAlloc; i++) write_i32(g_alloc[i]);
        write_i32(g_nCacheIndex);
        for (int i = 0; i < g_nCacheIndex; i++) write_i32(g_cidx[i]);
        write_i32(g_nCacheBits);
        for (int i = 0; i < g_nCacheBits; i++) write_i32(g_cbits[i]);
        write_i32(g_nCacheCaps);
        for (int i = 0; i < g_nCacheCaps; i++) write_i32(g_ccaps[i]);
    }

    fflush(stdout);
    return 0;
}
