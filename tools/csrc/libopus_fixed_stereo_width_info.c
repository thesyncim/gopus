/* Probe the selected FIXED_POINT opus_encoder.c stereo-width state and mode threshold. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus_encoder.c"

#ifndef FIXED_POINT
#error "fixed-point stereo-width reference required"
#endif
#ifndef ENABLE_RES24
#error "24-bit opus_res reference required"
#endif

static int read_u32(uint32_t *value) {
  unsigned char b[4];
  if (fread(b, 1, sizeof(b), stdin) != sizeof(b)) return 0;
  *value = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
           (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4] = {(unsigned char)value, (unsigned char)(value >> 8),
                        (unsigned char)(value >> 16), (unsigned char)(value >> 24)};
  return fwrite(b, 1, sizeof(b), stdout) == sizeof(b);
}

int main(void) {
  unsigned char magic[4];
  uint32_t version, count;
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  if (fread(magic, 1, sizeof(magic), stdin) != sizeof(magic) ||
      memcmp(magic, "GFWI", sizeof(magic)) != 0 ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 64) return 2;
  if (fwrite("GFWO", 1, 4, stdout) != 4 ||
      !write_u32(1) || !write_u32(count)) return 3;

  for (uint32_t c = 0; c < count; c++) {
    uint32_t sample_rate, frame_size, frames, voice_est;
    StereoWidthState mem = {0};
    opus_res pcm[11520];
    if (!read_u32(&sample_rate) || !read_u32(&frame_size) ||
        !read_u32(&frames) || !read_u32(&voice_est) ||
        sample_rate == 0 || frame_size < 4 || frame_size > 5760 ||
        frames == 0 || frames > 32 || voice_est > 127) return 4;
    if (!write_u32(frames)) return 5;
    for (uint32_t f = 0; f < frames; f++) {
      uint32_t sample;
      for (uint32_t i = 0; i < 2 * frame_size; i++) {
        if (!read_u32(&sample)) return 6;
        pcm[i] = (int32_t)sample;
      }
      opus_val16 width = compute_stereo_width(pcm, (int)frame_size,
                                               (opus_int32)sample_rate, &mem);
      opus_int32 mode_voice = MULT16_32_Q15(Q15ONE-width, mode_thresholds[0][0]) +
                              MULT16_32_Q15(width, mode_thresholds[1][0]);
      opus_int32 mode_music = MULT16_32_Q15(Q15ONE-width, mode_thresholds[1][1]) +
                              MULT16_32_Q15(width, mode_thresholds[1][1]);
      opus_int32 threshold = mode_music +
          ((voice_est*voice_est*(mode_voice-mode_music)) >> 14);
      if (!write_u32((uint32_t)(int32_t)width) ||
          !write_u32((uint32_t)mem.XX) || !write_u32((uint32_t)mem.XY) ||
          !write_u32((uint32_t)mem.YY) ||
          !write_u32((uint32_t)(int32_t)mem.smoothed_width) ||
          !write_u32((uint32_t)(int32_t)mem.max_follower) ||
          !write_u32((uint32_t)threshold)) return 7;
    }
  }
  return ferror(stdin) || ferror(stdout) ? 8 : 0;
}
