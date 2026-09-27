/* Selected FIXED_POINT+ENABLE_RES24 input high-pass oracles. This translation
 * unit includes the pinned opus_encoder.c so both calls reach its actual static
 * dc_reject/hp_cutoff implementations and the fixed silk_biquad_res path.
 * Wire fields are explicitly little-endian signed32 opus_res/hp_mem words.
 * Version 1 is the existing dc_reject protocol. Version 2 runs sequential
 * hp_cutoff frames with per-frame cutoffs while retaining all hp_mem state.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus_encoder.c"

#if !defined(FIXED_POINT) || !defined(ENABLE_RES24)
#error "fixed dc_reject oracle requires FIXED_POINT and ENABLE_RES24"
#endif

static int read_exact(void *p, size_t n) { return fread(p, 1, n, stdin) == n; }
static int write_exact(const void *p, size_t n) { return fwrite(p, 1, n, stdout) == n; }

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t value) {
  unsigned char b[4];
  b[0] = (unsigned char)value;
  b[1] = (unsigned char)(value >> 8);
  b[2] = (unsigned char)(value >> 16);
  b[3] = (unsigned char)(value >> 24);
  return write_exact(b, 4);
}

static int write_i32(int32_t value) { return write_u32((uint32_t)value); }

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  unsigned char magic[4];
  uint32_t version;
  if (!read_exact(magic, 4) || memcmp(magic, "GOFD", 4) != 0 ||
      !read_u32(&version) || (version != 1 && version != 2)) return 1;

  uint32_t fs, channels, frame_size;
  if (!read_u32(&fs) || !read_u32(&channels) || !read_u32(&frame_size) ||
      (channels != 1 && channels != 2) || frame_size == 0 ||
      frame_size > 5760 ||
      (fs != 8000 && fs != 12000 && fs != 16000 &&
       fs != 24000 && fs != 48000)) return 1;
  size_t count = (size_t)frame_size * channels;

  opus_val32 hp_mem[4];
  if (version == 1) {
    uint32_t cutoff;
    if (!read_u32(&cutoff) || cutoff != 3) return 1;
    for (int i = 0; i < 4; i++) {
      uint32_t word;
      if (!read_u32(&word)) return 1;
      hp_mem[i] = (opus_val32)(int32_t)word;
    }
    opus_res *in = (opus_res *)malloc(count * sizeof(opus_res));
    opus_res *out = (opus_res *)malloc(count * sizeof(opus_res));
    if (in == NULL || out == NULL) {
      free(in); free(out);
      return 1;
    }
    for (size_t i = 0; i < count; i++) {
      uint32_t word;
      if (!read_u32(&word)) { free(in); free(out); return 1; }
      in[i] = (opus_res)(int32_t)word;
    }
    dc_reject(in, (opus_int32)cutoff, out, hp_mem,
              (int)frame_size, (int)channels, (opus_int32)fs);

    if (!write_exact("GOFO", 4) || !write_u32(1) || !write_u32((uint32_t)count)) {
      free(in); free(out); return 1;
    }
    for (int i = 0; i < 4; i++) {
      if (!write_u32((uint32_t)hp_mem[i])) { free(in); free(out); return 1; }
    }
    for (size_t i = 0; i < count; i++) {
      if (!write_u32((uint32_t)out[i])) { free(in); free(out); return 1; }
    }
    free(in); free(out);
    return 0;
  }

  uint32_t frames;
  if (!read_u32(&frames) || frames == 0 || frames > 32) return 1;
  for (int i = 0; i < 4; i++) {
    uint32_t word;
    if (!read_u32(&word)) return 1;
    hp_mem[i] = (opus_val32)(int32_t)word;
  }
  opus_res *in = (opus_res *)malloc(count * sizeof(opus_res));
  opus_res *out = (opus_res *)malloc(count * sizeof(opus_res));
  if (in == NULL || out == NULL) {
    free(in); free(out);
    return 1;
  }
  if (!write_exact("GOFO", 4) || !write_u32(2) || !write_u32(frames)) {
    free(in); free(out);
    return 1;
  }
  for (uint32_t frame = 0; frame < frames; frame++) {
    uint32_t cutoff;
    if (!read_u32(&cutoff) || cutoff < 60 || cutoff > 100) {
      free(in); free(out);
      return 1;
    }
    for (size_t i = 0; i < count; i++) {
      uint32_t word;
      if (!read_u32(&word)) { free(in); free(out); return 1; }
      in[i] = (opus_res)(int32_t)word;
    }
    hp_cutoff(in, (opus_int32)cutoff, out, hp_mem,
              (int)frame_size, (int)channels, (opus_int32)fs, opus_select_arch());
    if (!write_u32((uint32_t)count)) { free(in); free(out); return 1; }
    for (int i = 0; i < 4; i++) {
      if (!write_i32((int32_t)hp_mem[i])) { free(in); free(out); return 1; }
    }
    for (size_t i = 0; i < count; i++) {
      if (!write_i32((int32_t)out[i])) { free(in); free(out); return 1; }
    }
  }
  free(in); free(out);
  return 0;
}
