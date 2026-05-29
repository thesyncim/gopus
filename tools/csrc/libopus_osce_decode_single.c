/*
 * libopus_osce_decode_single.c
 *
 * End-to-end OSCE decode oracle: decodes a sequence of Opus SILK-WB packets
 * using libopus with an OSCE model blob loaded and OSCE-BWE enabled, emitting
 * the float32 decoded PCM to stdout.
 *
 * This helper links against the OSCE-enabled libopus build
 * (--enable-osce --enable-osce-bwe) so that OPUS_SET_DNN_BLOB loads both the
 * LACE/NoLACE model (via silk_LoadOSCEModels) and optionally the BWE model.
 * OPUS_SET_OSCE_BWE(1) activates bandwidth extension when the API sample rate
 * is 48 kHz and the packet is SILK-only WB.
 *
 * The OSCE-enabled libopus build (`--enable-osce --enable-osce-bwe`) has the
 * LACE/NoLACE/BWE model weights compiled in statically (lacelayers_arrays,
 * nolacelayers_arrays, bbwenetlayers_arrays).  silk_ResetDecoder initialises
 * them automatically via osce_load_models(model, NULL, 0).  No runtime blob
 * loading is needed; OPUS_SET_DNN_BLOB is guarded by USE_WEIGHTS_FILE which
 * is NOT defined in the static-weights build.
 *
 * Input (stdin, binary):
 *   4-byte magic "GSOI"
 *   uint32 version (== 1)
 *   uint32 sample_rate
 *   uint32 channels
 *   uint32 frame_size (samples per channel per packet at API rate)
 *   uint32 complexity (0..10, selects OSCE method: >=6 LACE, >=7 NoLACE)
 *   uint32 enable_bwe (0 or 1)
 *   uint32 packet_count
 *   for each packet:
 *     uint32 packet_len   (0 == PLC)
 *     byte[packet_len]    raw Opus packet bytes
 *
 * Output (stdout, binary):
 *   4-byte magic "GSOO"
 *   uint32 version (== 1)
 *   uint32 total_samples  (total float32 elements == sample_count * channels)
 *   float32[total_samples] decoded PCM, interleaved if stereo
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"

#define INPUT_MAGIC  "GSOI"
#define OUTPUT_MAGIC "GSOO"

static int read_exact(void *dst, size_t n) {
    return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
    return fwrite(src, 1, n, stdout) == n;
}

static int read_u32(uint32_t *out) {
    unsigned char b[4];
    if (!read_exact(b, 4)) return 0;
    *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
           ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
    return 1;
}

static int write_u32(uint32_t v) {
    unsigned char b[4];
    b[0] = (unsigned char)(v & 0xFF);
    b[1] = (unsigned char)((v >> 8) & 0xFF);
    b[2] = (unsigned char)((v >> 16) & 0xFF);
    b[3] = (unsigned char)((v >> 24) & 0xFF);
    return write_exact(b, 4);
}

static int write_f32(float v) {
    union { float f; uint32_t u; } bits;
    bits.f = v;
    return write_u32(bits.u);
}

static int set_binary_stdio(void) {
#ifdef _WIN32
    if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
    if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
    return 1;
}

int main(void) {
    unsigned char magic[4];
    uint32_t version = 0;
    uint32_t sample_rate = 0;
    uint32_t channels = 0;
    uint32_t frame_size = 0;
    uint32_t complexity = 7;
    uint32_t enable_bwe = 0;
    uint32_t packet_count = 0;
    float *pcm = NULL;
    float *out_all = NULL;
    size_t out_all_len = 0;
    size_t out_all_cap = 0;
    OpusDecoder *dec = NULL;
    int err = OPUS_OK;
    uint32_t i;

    if (!set_binary_stdio()) {
        fprintf(stderr, "failed to set binary stdio mode\n");
        return 1;
    }

    if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
        fprintf(stderr, "invalid input magic\n");
        return 1;
    }
    if (!read_u32(&version) || version != 1) {
        fprintf(stderr, "unsupported version\n");
        return 1;
    }
    if (!read_u32(&sample_rate) || !read_u32(&channels) ||
        !read_u32(&frame_size)  || !read_u32(&complexity) ||
        !read_u32(&enable_bwe)) {
        fprintf(stderr, "failed to read header\n");
        return 1;
    }
    if (!read_u32(&packet_count)) {
        fprintf(stderr, "failed to read packet count\n");
        return 1;
    }

    if (channels == 0 || channels > 2 || frame_size == 0) {
        fprintf(stderr, "invalid decoder dimensions\n");
        return 1;
    }

    pcm = (float *)malloc(sizeof(float) * frame_size * channels);
    if (pcm == NULL) {
        fprintf(stderr, "failed to allocate PCM buffer\n");
        return 1;
    }

    dec = opus_decoder_create((opus_int32)sample_rate, (int)channels, &err);
    if (dec == NULL || err != OPUS_OK) {
        fprintf(stderr, "opus_decoder_create failed: %d\n", err);
        free(pcm);
        return 1;
    }

    /* Set complexity so libopus selects the matching OSCE method:
     * complexity >= 6  => OSCE_METHOD_LACE
     * complexity >= 7  => OSCE_METHOD_NOLACE */
    if (opus_decoder_ctl(dec, OPUS_SET_COMPLEXITY((int)complexity)) != OPUS_OK) {
        fprintf(stderr, "OPUS_SET_COMPLEXITY failed\n");
        opus_decoder_destroy(dec);
        free(pcm);
        return 1;
    }

    /* The OSCE build has LACE/NoLACE/BWE weights compiled in statically.
     * silk_ResetDecoder (called by opus_decoder_create) already loaded them
     * via osce_load_models(model, NULL, 0).  No runtime blob loading needed.
     *
     * Activate OSCE BWE if requested. */
    if (enable_bwe) {
        err = opus_decoder_ctl(dec, OPUS_SET_OSCE_BWE(1));
        if (err != OPUS_OK) {
            fprintf(stderr, "OPUS_SET_OSCE_BWE(1) failed: %d\n", err);
            opus_decoder_destroy(dec);
            free(pcm);
            return 1;
        }
    }

    for (i = 0; i < packet_count; i++) {
        uint32_t packet_len = 0;
        unsigned char *packet = NULL;
        int decoded_samples;
        size_t new_need;
        float *resized;

        if (!read_u32(&packet_len)) {
            fprintf(stderr, "failed to read packet length\n");
            goto fail;
        }
        if (packet_len > 0) {
            packet = (unsigned char *)malloc(packet_len);
            if (packet == NULL || !read_exact(packet, packet_len)) {
                fprintf(stderr, "failed to read packet payload\n");
                free(packet);
                goto fail;
            }
        }

        decoded_samples = opus_decode_float(dec, packet, (opus_int32)packet_len,
                                            pcm, (int)frame_size, 0);
        free(packet);

        if (decoded_samples < 0) {
            fprintf(stderr, "opus_decode_float failed: %d\n", decoded_samples);
            goto fail;
        }

        new_need = out_all_len + (size_t)decoded_samples * (size_t)channels;
        if (new_need > out_all_cap) {
            size_t new_cap = out_all_cap ? out_all_cap : 4096;
            while (new_cap < new_need) new_cap *= 2;
            resized = (float *)realloc(out_all, new_cap * sizeof(float));
            if (resized == NULL) {
                fprintf(stderr, "OOM growing output buffer\n");
                goto fail;
            }
            out_all = resized;
            out_all_cap = new_cap;
        }
        memcpy(out_all + out_all_len, pcm,
               (size_t)decoded_samples * (size_t)channels * sizeof(float));
        out_all_len += (size_t)decoded_samples * (size_t)channels;
    }

    opus_decoder_destroy(dec);
    free(pcm);

    /* Write output */
    if (!write_exact(OUTPUT_MAGIC, 4) ||
        !write_u32(1) ||
        !write_u32((uint32_t)out_all_len)) {
        fprintf(stderr, "failed to write output header\n");
        free(out_all);
        return 1;
    }
    for (i = 0; i < (uint32_t)out_all_len; i++) {
        if (!write_f32(out_all[i])) {
            fprintf(stderr, "failed to write output samples\n");
            free(out_all);
            return 1;
        }
    }
    free(out_all);
    return 0;

fail:
    opus_decoder_destroy(dec);
    free(pcm);
    free(out_all);
    return 1;
}
