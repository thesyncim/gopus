#include <stdint.h>
#include <stdlib.h>
#include <stdio.h>
#include "bands.c"

static int read_exact(void *dst, size_t size)
{
   return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size)
{
   return fwrite(src, 1, size, stdout) == size;
}

int main(void)
{
   uint32_t header[3];
   celt_norm *x;
   celt_norm *y;
   uint32_t i;

   if (!read_exact(header, sizeof(header)) || header[0] != 0x49535347u || header[1] != 1)
      return 2;
   if (header[2] == 0 || header[2] > 4096)
      return 3;
   x = (celt_norm *)malloc((size_t)header[2] * sizeof(*x));
   y = (celt_norm *)malloc((size_t)header[2] * sizeof(*y));
   if (x == NULL || y == NULL)
      return 4;
   if (!read_exact(x, (size_t)header[2] * sizeof(*x)) || !read_exact(y, (size_t)header[2] * sizeof(*y)))
      return 5;
   if (fgetc(stdin) != EOF)
      return 6;
   stereo_split(x, y, (int)header[2]);
   if (!write_exact("GSSO", 4))
      return 7;
   header[0] = 1;
   if (!write_exact(header, sizeof(header[0])) || !write_exact(&header[2], sizeof(header[2])))
      return 8;
   for (i = 0; i < header[2]; ++i)
      if (!write_exact(&x[i], sizeof(x[i])))
         return 9;
   for (i = 0; i < header[2]; ++i)
      if (!write_exact(&y[i], sizeof(y[i])))
         return 10;
   free(x);
   free(y);
   return 0;
}
