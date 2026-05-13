#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"

#define MAGIC "GOCP"
#define MAX_PACKET 4000
#define PI 3.14159265358979323846

static void put_u32(uint32_t v) {
    unsigned char b[4];
    b[0] = (unsigned char)(v & 0xff);
    b[1] = (unsigned char)((v >> 8) & 0xff);
    b[2] = (unsigned char)((v >> 16) & 0xff);
    b[3] = (unsigned char)((v >> 24) & 0xff);
    fwrite(b, 1, 4, stdout);
}

static void put_i32(int v) {
    put_u32((uint32_t)v);
}

static int map_force_channels(int v) {
    return v == OPUS_AUTO ? -1 : v;
}

static int map_bandwidth(int v) {
    switch (v) {
    case OPUS_BANDWIDTH_NARROWBAND:
        return 0;
    case OPUS_BANDWIDTH_MEDIUMBAND:
        return 1;
    case OPUS_BANDWIDTH_WIDEBAND:
        return 2;
    case OPUS_BANDWIDTH_SUPERWIDEBAND:
        return 3;
    case OPUS_BANDWIDTH_FULLBAND:
        return 4;
    default:
        return -1;
    }
}

static void fill_pcm(float *pcm, int frame_size, int channels, int frame_index) {
    int start = frame_index * frame_size;
    for (int i = 0; i < frame_size; i++) {
        for (int ch = 0; ch < channels; ch++) {
            double freq = 180.0 + 170.0 * (double)ch + 37.0 * (double)frame_index;
            double t = (double)(start + i) / 48000.0;
            double carrier = 0.34 * sin(2.0 * PI * freq * t);
            double motion = 0.05 * sin(2.0 * PI * (double)(3 + ch) * t);
            pcm[i * channels + ch] = (float)(carrier + motion);
        }
    }
}

static void write_step(OpusEncoder *enc, int frame_size, int channels, int frame_index) {
    float pcm[5760 * 2];
    unsigned char packet[MAX_PACKET];
    int lookahead = 0;
    opus_uint32 final_range = 0;
    int bitrate = 0;
    int complexity = 0;
    int vbr = 0;
    int vbr_constraint = 0;
    int fec = 0;
    int packet_loss = 0;
    int dtx = 0;
    int in_dtx = 0;
    int force_channels = 0;
    int signal = 0;
    int bandwidth = 0;
    int max_bandwidth = 0;
    int expert_duration = 0;
    int lsb_depth = 0;
    int prediction_disabled = 0;
    int phase_inversion_disabled = 0;

    fill_pcm(pcm, frame_size, channels, frame_index);
    int n = opus_encode_float(enc, pcm, frame_size, packet, MAX_PACKET);

    opus_encoder_ctl(enc, OPUS_GET_LOOKAHEAD(&lookahead));
    opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range));
    opus_encoder_ctl(enc, OPUS_GET_BITRATE(&bitrate));
    opus_encoder_ctl(enc, OPUS_GET_COMPLEXITY(&complexity));
    opus_encoder_ctl(enc, OPUS_GET_VBR(&vbr));
    opus_encoder_ctl(enc, OPUS_GET_VBR_CONSTRAINT(&vbr_constraint));
    opus_encoder_ctl(enc, OPUS_GET_INBAND_FEC(&fec));
    opus_encoder_ctl(enc, OPUS_GET_PACKET_LOSS_PERC(&packet_loss));
    opus_encoder_ctl(enc, OPUS_GET_DTX(&dtx));
    opus_encoder_ctl(enc, OPUS_GET_IN_DTX(&in_dtx));
    opus_encoder_ctl(enc, OPUS_GET_FORCE_CHANNELS(&force_channels));
    opus_encoder_ctl(enc, OPUS_GET_SIGNAL(&signal));
    opus_encoder_ctl(enc, OPUS_GET_BANDWIDTH(&bandwidth));
    opus_encoder_ctl(enc, OPUS_GET_MAX_BANDWIDTH(&max_bandwidth));
    opus_encoder_ctl(enc, OPUS_GET_EXPERT_FRAME_DURATION(&expert_duration));
    opus_encoder_ctl(enc, OPUS_GET_LSB_DEPTH(&lsb_depth));
    opus_encoder_ctl(enc, OPUS_GET_PREDICTION_DISABLED(&prediction_disabled));
    opus_encoder_ctl(enc, OPUS_GET_PHASE_INVERSION_DISABLED(&phase_inversion_disabled));

    put_i32(frame_size);
    put_i32(channels);
    put_i32(n);
    put_i32(lookahead);
    put_u32(final_range);
    put_i32(bitrate);
    put_i32(complexity);
    put_i32(vbr);
    put_i32(vbr_constraint);
    put_i32(fec);
    put_i32(packet_loss);
    put_i32(dtx);
    put_i32(in_dtx);
    put_i32(map_force_channels(force_channels));
    put_i32(signal);
    put_i32(map_bandwidth(bandwidth));
    put_i32(map_bandwidth(max_bandwidth));
    put_i32(expert_duration);
    put_i32(lsb_depth);
    put_i32(prediction_disabled);
    put_i32(phase_inversion_disabled);
    put_i32(n > 0 ? n : 0);
    if (n > 0) {
        fwrite(packet, 1, (size_t)n, stdout);
    }
}

static void begin_output(int steps) {
    fwrite(MAGIC, 1, 4, stdout);
    put_u32(1);
    put_u32((uint32_t)steps);
}

static int run_audio_controls(void) {
    int err = OPUS_OK;
    OpusEncoder *enc = opus_encoder_create(48000, 1, OPUS_APPLICATION_AUDIO, &err);
    if (err != OPUS_OK || enc == NULL) return 1;

    begin_output(2);

    opus_encoder_ctl(enc, OPUS_SET_BITRATE(64000));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(3));
    opus_encoder_ctl(enc, OPUS_SET_VBR(1));
    opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
    opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
    opus_encoder_ctl(enc, OPUS_SET_EXPERT_FRAME_DURATION(OPUS_FRAMESIZE_20_MS));
    opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(24));
    opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0));
    opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0));
    opus_encoder_ctl(enc, OPUS_SET_DTX(0));
    opus_encoder_ctl(enc, OPUS_SET_PREDICTION_DISABLED(0));
    opus_encoder_ctl(enc, OPUS_SET_PHASE_INVERSION_DISABLED(0));
    write_step(enc, 960, 1, 0);

    opus_encoder_ctl(enc, OPUS_SET_BITRATE(24000));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(8));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND));
    opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
    opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(1));
    opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(20));
    opus_encoder_ctl(enc, OPUS_SET_DTX(1));
    opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(16));
    write_step(enc, 960, 1, 1);

    opus_encoder_destroy(enc);
    return 0;
}

static int run_lowdelay_controls(void) {
    int err = OPUS_OK;
    OpusEncoder *enc = opus_encoder_create(48000, 1, OPUS_APPLICATION_RESTRICTED_LOWDELAY, &err);
    if (err != OPUS_OK || enc == NULL) return 1;

    begin_output(3);

    opus_encoder_ctl(enc, OPUS_SET_BITRATE(64000));
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(5));
    opus_encoder_ctl(enc, OPUS_SET_EXPERT_FRAME_DURATION(OPUS_FRAMESIZE_2_5_MS));
    opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_FULLBAND));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_MUSIC));
    write_step(enc, 120, 1, 0);

    opus_encoder_ctl(enc, OPUS_SET_BITRATE(96000));
    opus_encoder_ctl(enc, OPUS_SET_EXPERT_FRAME_DURATION(OPUS_FRAMESIZE_5_MS));
    opus_encoder_ctl(enc, OPUS_SET_PREDICTION_DISABLED(1));
    write_step(enc, 240, 1, 1);

    opus_encoder_ctl(enc, OPUS_SET_EXPERT_FRAME_DURATION(OPUS_FRAMESIZE_20_MS));
    opus_encoder_ctl(enc, OPUS_SET_PREDICTION_DISABLED(0));
    write_step(enc, 960, 1, 2);

    opus_encoder_destroy(enc);
    return 0;
}

static int run_force_channels(void) {
    int err = OPUS_OK;
    OpusEncoder *enc = opus_encoder_create(48000, 2, OPUS_APPLICATION_RESTRICTED_LOWDELAY, &err);
    if (err != OPUS_OK || enc == NULL) return 1;

    begin_output(3);

    opus_encoder_ctl(enc, OPUS_SET_BITRATE(128000));
    opus_encoder_ctl(enc, OPUS_SET_EXPERT_FRAME_DURATION(OPUS_FRAMESIZE_20_MS));
    opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2));
    write_step(enc, 960, 2, 0);

    opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(1));
    write_step(enc, 960, 2, 1);

    opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2));
    write_step(enc, 960, 2, 2);

    opus_encoder_destroy(enc);
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: %s <scenario>\n", argv[0]);
        return 2;
    }
    if (strcmp(argv[1], "audio_controls") == 0) return run_audio_controls();
    if (strcmp(argv[1], "lowdelay_controls") == 0) return run_lowdelay_controls();
    if (strcmp(argv[1], "force_channels") == 0) return run_force_channels();
    fprintf(stderr, "unknown scenario: %s\n", argv[1]);
    return 2;
}
