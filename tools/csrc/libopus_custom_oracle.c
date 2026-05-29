/*
 * libopus_custom_oracle.c — binary-protocol oracle for Opus Custom encode/decode.
 *
 * Requires a libopus build with --enable-custom-modes; link against its
 * .libs/libopus.a.
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
 *       uint32 packetLen
 *       packetLen bytes
 *
 * Reference: libopus include/opus_custom.h, celt/celt_encoder.c.
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

static int read_f32(float *out) {
    uint32_t bits;
    if (!read_u32(&bits)) return 0;
    memcpy(out, &bits, 4);
    return 1;
}

int main(void) {
    if (!set_binary_stdio()) { fprintf(stderr, "binary stdio failed\n"); return 1; }

    /* Read and verify input magic. */
    char magic[4];
    if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
        fprintf(stderr, "bad input magic\n"); return 1;
    }

    uint32_t n_cases;
    if (!read_u32(&n_cases)) { fprintf(stderr, "read N\n"); return 1; }

    /* Write output header. */
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

        /* Create mode. */
        int err = OPUS_OK;
        OpusCustomMode *mode = opus_custom_mode_create((opus_int32)Fs, (int)frame_size, &err);
        if (!mode || err != OPUS_OK) {
            fprintf(stderr, "case %u: opus_custom_mode_create(%u,%u) error %d\n",
                    c, Fs, frame_size, err);
            /* Emit zero-length packet so the Go side can detect the failure. */
            write_u32(0);
            continue;
        }

        /* Create encoder. */
        OpusCustomEncoder *enc = opus_custom_encoder_create(mode, (int)channels, &err);
        if (!enc || err != OPUS_OK) {
            fprintf(stderr, "case %u: encoder create error %d\n", c, err);
            opus_custom_mode_destroy(mode);
            write_u32(0);
            continue;
        }

        /* Configure: CBR, complexity 9, LSB depth 16 (match gopus defaults). */
        opus_custom_encoder_ctl(enc, OPUS_SET_VBR(0));
        opus_custom_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0));
        opus_custom_encoder_ctl(enc, OPUS_SET_COMPLEXITY(9));
        opus_custom_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(16));
        opus_custom_encoder_ctl(enc, CELT_SET_SIGNALLING(0));

        /* Encode. */
        unsigned char packet[MAX_PACKET];
        int sz = opus_custom_encode_float(enc, pcm, (int)frame_size,
                                          packet, (int)maxBytes);
        opus_custom_encoder_destroy(enc);
        opus_custom_mode_destroy(mode);

        if (sz < 0) {
            fprintf(stderr, "case %u: encode error %d\n", c, sz);
            write_u32(0);
            continue;
        }

        if (!write_u32((uint32_t)sz) || !write_exact(packet, (size_t)sz)) {
            fprintf(stderr, "case %u: write packet\n", c); return 1;
        }
    }

    fflush(stdout);
    return 0;
}
