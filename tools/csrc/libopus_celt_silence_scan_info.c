/* Raw CELT silence scan from pinned celt_encoder.c:1969-1975. The selected
 * float build's celt_maxabs_res and state update are used for each case. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "mathops.h"

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (fread(b, 1, 4, stdin) != 4) return 0;
  *v = (uint32_t)b[0] | (uint32_t)b[1] << 8 |
       (uint32_t)b[2] << 16 | (uint32_t)b[3] << 24;
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4] = {(unsigned char)v, (unsigned char)(v >> 8),
                        (unsigned char)(v >> 16), (unsigned char)(v >> 24)};
  return fwrite(b, 1, 4, stdout) == 4;
}

int main(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1 ||
      _setmode(_fileno(stdout), _O_BINARY) == -1) return 1;
#endif
  char magic[4];
  uint32_t version, count;
  if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GSSI", 4) ||
      !read_u32(&version) || version != 1 ||
      !read_u32(&count) || count == 0 || count > 64) return 2;
  if (fwrite("GSSO", 1, 4, stdout) != 4 || !write_u32(1) ||
      !write_u32(count)) return 3;

  for (uint32_t c = 0; c < count; c++) {
    uint32_t channels, coded, frame_size, overlap, upsample;
    uint32_t previous_bits, sample_count;
    opus_res pcm[2 * 5760];
    if (!read_u32(&channels) || !read_u32(&coded) ||
        !read_u32(&frame_size) || !read_u32(&overlap) ||
        !read_u32(&upsample) || !read_u32(&previous_bits) ||
        !read_u32(&sample_count) ||
        channels < 1 || channels > 2 || coded < 1 || coded > channels ||
        upsample < 1 || upsample > 6 || frame_size < overlap ||
        frame_size % upsample != 0 ||
        sample_count != channels * frame_size / upsample ||
        sample_count > 2 * 5760) return 4;
    for (uint32_t i = 0; i < sample_count; i++) {
      uint32_t bits;
      if (!read_u32(&bits)) return 5;
      memcpy(&pcm[i], &bits, 4);
    }

    opus_val32 previous;
    memcpy(&previous, &previous_bits, 4);
    int first_len = (int)(coded * (frame_size - overlap) / upsample);
    int overlap_len = (int)(coded * overlap / upsample);
    opus_val32 sample_max = MAX32(previous, celt_maxabs_res(pcm, first_len));
    opus_val32 overlap_max = celt_maxabs_res(pcm + first_len, overlap_len);
    sample_max = MAX32(sample_max, overlap_max);
    int silence = sample_max <= (opus_val16)1 / (1 << 24);
    uint32_t sample_bits, overlap_bits;
    memcpy(&sample_bits, &sample_max, 4);
    memcpy(&overlap_bits, &overlap_max, 4);
    if (!write_u32((uint32_t)silence) || !write_u32(sample_bits) ||
        !write_u32(overlap_bits)) return 6;
  }
  return ferror(stdin) || ferror(stdout) ? 7 : 0;
}
