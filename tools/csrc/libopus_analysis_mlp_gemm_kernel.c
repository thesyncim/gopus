#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "mlp.h"

#ifndef GOPUS_MLP_SOURCE_SHA256
#error "GOPUS_MLP_SOURCE_SHA256 must identify the selected libopus src/mlp.c"
#endif

#define MAX_CASES 64u

static int read_exact(void *dst, size_t n)
{
   return fread(dst, 1, n, stdin) == n;
}

static int read_u32(uint32_t *value)
{
   unsigned char b[4];
   if (!read_exact(b, sizeof(b))) return 0;
   *value = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
            ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
   return 1;
}

static int read_f32(float *value)
{
   uint32_t bits;
   if (!read_u32(&bits)) return 0;
   memcpy(value, &bits, sizeof(bits));
   return isfinite(*value);
}

static int write_exact(const void *src, size_t n)
{
   return fwrite(src, 1, n, stdout) == n;
}

static int write_u32(uint32_t value)
{
   unsigned char b[4];
   b[0] = (unsigned char)value;
   b[1] = (unsigned char)(value >> 8);
   b[2] = (unsigned char)(value >> 16);
   b[3] = (unsigned char)(value >> 24);
   return write_exact(b, sizeof(b));
}

static int write_f32s(const float *values, uint32_t n)
{
   uint32_t i;
   for (i = 0; i < n; ++i) {
      uint32_t bits;
      memcpy(&bits, &values[i], sizeof(bits));
      if (!write_u32(bits)) return 0;
   }
   return 1;
}

int main(void)
{
   unsigned char magic[4];
   uint32_t version, count, c;
   static const char output_magic[4] = {'G', 'M', 'K', 'O'};
   static const char source_hash[] = GOPUS_MLP_SOURCE_SHA256;

   if (sizeof(source_hash) != 65 || !read_exact(magic, sizeof(magic)) ||
       memcmp(magic, "GMKI", 4) != 0 || !read_u32(&version) || version != 1 ||
       !read_u32(&count) || count == 0 || count > MAX_CASES)
      return 2;
   if (!write_exact(output_magic, sizeof(output_magic)) || !write_u32(1) ||
       !write_u32(count) || !write_exact(source_hash, 64))
      return 3;

   for (c = 0; c < count; ++c) {
      float features[25];
      float state[24];
      float dense0[32];
      float dense2[2];
      uint32_t i;
      for (i = 0; i < 25; ++i)
         if (!read_f32(&features[i])) return 4;
      for (i = 0; i < 24; ++i)
         if (!read_f32(&state[i])) return 5;

      analysis_compute_dense(&layer0, dense0, features);
      if (!write_f32s(dense0, 32)) return 6;
      analysis_compute_gru(&layer1, state, dense0);
      if (!write_f32s(state, 24)) return 7;
      analysis_compute_dense(&layer2, dense2, state);
      if (!write_f32s(dense2, 2)) return 8;
   }
   if (fgetc(stdin) != EOF || ferror(stdin)) return 9;
   return fflush(stdout) == 0 ? 0 : 10;
}
