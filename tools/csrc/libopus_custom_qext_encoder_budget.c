/* Drives selected libopus fixed+QEXT custom encoding with changing frame sizes
 * and byte budgets on one encoder instance. Input PCM is little-endian int16;
 * its conversion to float is exact for the custom float API.
 * GQBI: Fs, mode frame, channels, records; each record has frame, budget,
 * reset flag, then frame*channels i16 samples.
 * GQBO: records; each record has signed packet length, final range, bytes.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "config.h"
#include "opus_custom.h"
#include "opus_defines.h"
#include "celt.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT) || !defined(CUSTOM_MODES)
#error "custom QEXT budget oracle requires matching fixed/QEXT/custom C features"
#endif

static uint32_t read_u32(void) {
    unsigned char b[4];
    if (fread(b, 1, 4, stdin) != 4) exit(2);
    return (uint32_t)b[0] | (uint32_t)b[1]<<8 | (uint32_t)b[2]<<16 | (uint32_t)b[3]<<24;
}
static int16_t read_i16(void) {
    unsigned char b[2];
    if (fread(b, 1, 2, stdin) != 2) exit(2);
    return (int16_t)((uint16_t)b[0] | (uint16_t)b[1]<<8);
}
static void write_u32(uint32_t v) {
    unsigned char b[4] = {v, v>>8, v>>16, v>>24};
    if (fwrite(b, 1, 4, stdout) != 4) exit(3);
}
static void check(int status) {
    if (status != OPUS_OK) exit(4);
}

int main(void) {
#ifdef _WIN32
    if (_setmode(_fileno(stdin), _O_BINARY)==-1 ||
        _setmode(_fileno(stdout), _O_BINARY)==-1) return 2;
#endif
    char magic[4];
    if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GQBI", 4)) return 2;
    int fs = (int)read_u32(), mode_frame = (int)read_u32();
    int channels = (int)read_u32();
    uint32_t count = read_u32();
    if (count < 1 || count > 64 || channels < 1 || channels > 2) return 2;
    int err = OPUS_OK;
    OpusCustomMode *mode = opus_custom_mode_create(fs, mode_frame, &err);
    if (mode == NULL || err != OPUS_OK) return 4;
    OpusCustomEncoder *enc = opus_custom_encoder_create(mode, channels, &err);
    if (enc == NULL || err != OPUS_OK) return 4;
    check(opus_custom_encoder_ctl(enc, OPUS_SET_VBR(0)));
    check(opus_custom_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0)));
    check(opus_custom_encoder_ctl(enc, OPUS_SET_COMPLEXITY(9)));
    check(opus_custom_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(16)));
    check(opus_custom_encoder_ctl(enc, CELT_SET_SIGNALLING(0)));

    if (fwrite("GQBO", 1, 4, stdout) != 4) return 3;
    write_u32(count);
    for (uint32_t record = 0; record < count; record++) {
        int frame = (int)read_u32(), budget = (int)read_u32();
        int reset = (int)read_u32();
        if (frame < 1 || frame > 2048 || budget < 2 || budget > 1275 ||
            (reset != 0 && reset != 1)) return 2;
        float pcm[4096];
        unsigned char packet[1275];
        for (int i = 0; i < frame*channels; i++)
            pcm[i] = (float)read_i16() * (1.f/32768.f);
        if (reset) check(opus_custom_encoder_ctl(enc, OPUS_RESET_STATE));
        int n = opus_custom_encode_float(enc, pcm, frame, packet, budget);
        opus_uint32 range = 0;
        if (n >= 0) check(opus_custom_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&range)));
        write_u32((uint32_t)n);
        write_u32(range);
        if (n > 0 && fwrite(packet, 1, (size_t)n, stdout) != (size_t)n) return 3;
    }
    opus_custom_encoder_destroy(enc);
    opus_custom_mode_destroy(mode);
    return 0;
}
