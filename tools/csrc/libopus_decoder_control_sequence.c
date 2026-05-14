#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"

#define MAGIC "GODC"
#define MAX_PACKET 4000
#define MAX_FRAME 5760
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
            double freq = 170.0 + 130.0 * (double)ch + 29.0 * (double)frame_index;
            double t = (double)(start + i) / 48000.0;
            double carrier = 0.28 * sin(2.0 * PI * freq * t);
            double wobble = 0.07 * sin(2.0 * PI * (double)(5 + ch) * t);
            pcm[i * channels + ch] = (float)(carrier + wobble);
        }
    }
}

static void begin_output(int steps) {
    fwrite(MAGIC, 1, 4, stdout);
    put_u32(1);
    put_u32((uint32_t)steps);
}

static void write_state(OpusDecoder *dec, int ret, int channels, const unsigned char *packet, int packet_len) {
    int sample_rate = 0;
    int gain = 0;
    int ignore_extensions = 0;
    int bandwidth = 0;
    int last_packet_duration = 0;
    int pitch = 0;
    opus_uint32 final_range = 0;

    opus_decoder_ctl(dec, OPUS_GET_SAMPLE_RATE(&sample_rate));
    opus_decoder_ctl(dec, OPUS_GET_GAIN(&gain));
    opus_decoder_ctl(dec, OPUS_GET_IGNORE_EXTENSIONS(&ignore_extensions));
    opus_decoder_ctl(dec, OPUS_GET_BANDWIDTH(&bandwidth));
    opus_decoder_ctl(dec, OPUS_GET_LAST_PACKET_DURATION(&last_packet_duration));
    opus_decoder_ctl(dec, OPUS_GET_PITCH(&pitch));
    opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(&final_range));

    put_i32(ret);
    put_i32(sample_rate);
    put_i32(channels);
    put_i32(gain);
    put_i32(ignore_extensions);
    put_i32(map_bandwidth(bandwidth));
    put_i32(last_packet_duration);
    put_i32(pitch);
    put_u32(final_range);
    put_i32(packet_len);
    if (packet_len > 0) {
        fwrite(packet, 1, (size_t)packet_len, stdout);
    }
}

static int encode_packet(int application, int channels, int bandwidth, int bitrate, int signal,
        int frame_size, int frame_index, unsigned char *packet) {
    int err = OPUS_OK;
    float pcm[MAX_FRAME * 2];
    OpusEncoder *enc = opus_encoder_create(48000, channels, application, &err);
    if (err != OPUS_OK || enc == NULL) {
        return -1;
    }
    opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10));
    opus_encoder_ctl(enc, OPUS_SET_VBR(0));
    opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
    opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(bandwidth));
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(signal));
    fill_pcm(pcm, frame_size, channels, frame_index);
    int n = opus_encode_float(enc, pcm, frame_size, packet, MAX_PACKET);
    opus_encoder_destroy(enc);
    return n;
}

static int decode_packet_step(OpusDecoder *dec, int channels, const unsigned char *packet, int packet_len) {
    float pcm[MAX_FRAME * 2];
    int ret = opus_decode_float(dec, packet, packet_len, pcm, MAX_FRAME, 0);
    write_state(dec, ret, channels, packet, packet_len);
    return ret < 0 ? 1 : 0;
}

static int run_defaults(void) {
    const int rates[] = {8000, 12000, 16000, 24000, 48000};
    begin_output(10);
    for (int ri = 0; ri < 5; ri++) {
        for (int channels = 1; channels <= 2; channels++) {
            int err = OPUS_OK;
            OpusDecoder *dec = opus_decoder_create(rates[ri], channels, &err);
            if (err != OPUS_OK || dec == NULL) return 1;
            write_state(dec, 0, channels, NULL, 0);
            opus_decoder_destroy(dec);
        }
    }
    return 0;
}

static int run_control_lifecycle(void) {
    int err = OPUS_OK;
    OpusDecoder *dec = opus_decoder_create(48000, 1, &err);
    if (err != OPUS_OK || dec == NULL) return 1;

    begin_output(4);
    write_state(dec, 0, 1, NULL, 0);

    opus_decoder_ctl(dec, OPUS_SET_GAIN(512));
    opus_decoder_ctl(dec, OPUS_SET_IGNORE_EXTENSIONS(1));
    write_state(dec, 0, 1, NULL, 0);

    opus_decoder_ctl(dec, OPUS_RESET_STATE);
    write_state(dec, 0, 1, NULL, 0);

    opus_decoder_ctl(dec, OPUS_SET_GAIN(-768));
    opus_decoder_ctl(dec, OPUS_SET_IGNORE_EXTENSIONS(0));
    write_state(dec, 0, 1, NULL, 0);

    opus_decoder_destroy(dec);
    return 0;
}

static int run_packet_modes_mono(void) {
    unsigned char packets[4][MAX_PACKET];
    int packet_lens[4];

    packet_lens[0] = encode_packet(OPUS_APPLICATION_RESTRICTED_SILK, 1,
            OPUS_BANDWIDTH_NARROWBAND, 12000, OPUS_SIGNAL_VOICE, 960, 0, packets[0]);
    packet_lens[1] = encode_packet(OPUS_APPLICATION_RESTRICTED_SILK, 1,
            OPUS_BANDWIDTH_WIDEBAND, 24000, OPUS_SIGNAL_VOICE, 960, 1, packets[1]);
    packet_lens[2] = encode_packet(OPUS_APPLICATION_VOIP, 1,
            OPUS_BANDWIDTH_SUPERWIDEBAND, 32000, OPUS_SIGNAL_VOICE, 960, 2, packets[2]);
    packet_lens[3] = encode_packet(OPUS_APPLICATION_RESTRICTED_CELT, 1,
            OPUS_BANDWIDTH_FULLBAND, 96000, OPUS_SIGNAL_MUSIC, 480, 3, packets[3]);
    for (int i = 0; i < 4; i++) {
        if (packet_lens[i] <= 0) return 1;
    }

    int err = OPUS_OK;
    OpusDecoder *dec = opus_decoder_create(48000, 1, &err);
    if (err != OPUS_OK || dec == NULL) return 1;

    begin_output(4);
    for (int i = 0; i < 4; i++) {
        if (decode_packet_step(dec, 1, packets[i], packet_lens[i])) {
            opus_decoder_destroy(dec);
            return 1;
        }
    }
    opus_decoder_destroy(dec);
    return 0;
}

static int run_packet_modes_stereo(void) {
    unsigned char packets[3][MAX_PACKET];
    int packet_lens[3];

    packet_lens[0] = encode_packet(OPUS_APPLICATION_RESTRICTED_SILK, 2,
            OPUS_BANDWIDTH_WIDEBAND, 36000, OPUS_SIGNAL_VOICE, 960, 0, packets[0]);
    packet_lens[1] = encode_packet(OPUS_APPLICATION_VOIP, 2,
            OPUS_BANDWIDTH_SUPERWIDEBAND, 48000, OPUS_SIGNAL_VOICE, 960, 1, packets[1]);
    packet_lens[2] = encode_packet(OPUS_APPLICATION_RESTRICTED_CELT, 2,
            OPUS_BANDWIDTH_FULLBAND, 128000, OPUS_SIGNAL_MUSIC, 480, 2, packets[2]);
    for (int i = 0; i < 3; i++) {
        if (packet_lens[i] <= 0) return 1;
    }

    int err = OPUS_OK;
    OpusDecoder *dec = opus_decoder_create(48000, 2, &err);
    if (err != OPUS_OK || dec == NULL) return 1;

    begin_output(3);
    for (int i = 0; i < 3; i++) {
        if (decode_packet_step(dec, 2, packets[i], packet_lens[i])) {
            opus_decoder_destroy(dec);
            return 1;
        }
    }
    opus_decoder_destroy(dec);
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        return 2;
    }
    if (strcmp(argv[1], "defaults") == 0) {
        return run_defaults();
    }
    if (strcmp(argv[1], "control_lifecycle") == 0) {
        return run_control_lifecycle();
    }
    if (strcmp(argv[1], "packet_modes_mono") == 0) {
        return run_packet_modes_mono();
    }
    if (strcmp(argv[1], "packet_modes_stereo") == 0) {
        return run_packet_modes_stereo();
    }
    return 2;
}
