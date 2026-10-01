/* A single selected-libopus Custom API encode with explicit CELT controls. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "config.h"
#include "opus_custom.h"
#include "opus_defines.h"
#include "celt.h"

static uint32_t read_u32(void) {
    unsigned char b[4];
    if (fread(b, 1, 4, stdin) != 4) exit(2);
    return (uint32_t)b[0] | (uint32_t)b[1]<<8 | (uint32_t)b[2]<<16 | (uint32_t)b[3]<<24;
}

static void write_u32(uint32_t v) {
    unsigned char b[4] = {v, v>>8, v>>16, v>>24};
    if (fwrite(b, 1, 4, stdout) != 4) exit(3);
}

static void control(int status) {
    if (status != OPUS_OK) {
        fprintf(stderr, "custom control failed: %d\n", status);
        exit(4);
    }
}

int main(void) {
    char magic[4];
    if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GCCL", 4)) return 2;
    int fs = (int)read_u32();
    int frame_size = (int)read_u32();
    int channels = (int)read_u32();
    int max_bytes = (int)read_u32();
    int complexity = (int)read_u32();
    int lsb_depth = (int)read_u32();
    int bitrate = (int32_t)read_u32();
    int vbr = (int)read_u32();
    int constrained_vbr = (int)read_u32();
    int prediction = (int)read_u32();
    int loss_rate = (int)read_u32();
    int n_samples = (int)read_u32();
    if (fs != 48000 || frame_size < 120 || frame_size > 960 ||
        channels < 1 || channels > 2 || max_bytes < 2 || max_bytes > 1275 ||
        n_samples != frame_size * channels) return 2;

    float pcm[2*960];
    for (int i = 0; i < n_samples; i++) {
        uint32_t bits = read_u32();
        memcpy(&pcm[i], &bits, 4);
    }

    int err;
    OpusCustomMode *mode = opus_custom_mode_create(fs, frame_size, &err);
    if (!mode || err != OPUS_OK) return 4;
    OpusCustomEncoder *enc = opus_custom_encoder_create(mode, channels, &err);
    if (!enc || err != OPUS_OK) return 4;
    control(opus_custom_encoder_ctl(enc, CELT_SET_SIGNALLING(0)));
    control(opus_custom_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity)));
    control(opus_custom_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(lsb_depth)));
    control(opus_custom_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate)));
    control(opus_custom_encoder_ctl(enc, OPUS_SET_VBR(vbr)));
    control(opus_custom_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(constrained_vbr)));
    control(opus_custom_encoder_ctl(enc, CELT_SET_PREDICTION(prediction)));
    control(opus_custom_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(loss_rate)));

    unsigned char packet[1275];
    int size = opus_custom_encode_float(enc, pcm, frame_size, packet, max_bytes);
    opus_uint32 range = 0;
    control(opus_custom_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&range)));
    if (size < 0 || size > max_bytes) return 5;
    if (fwrite("GCCL", 1, 4, stdout) != 4) return 3;
    write_u32((uint32_t)size);
    write_u32(range);
    if (fwrite(packet, 1, (size_t)size, stdout) != (size_t)size) return 3;
    opus_custom_encoder_destroy(enc);
    opus_custom_mode_destroy(mode);
    return 0;
}
