/* Reports the pinned libopus FARGAN gain layer before and after exp(). */
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif
#include "arch.h"
#include "nnet.h"
#include "fargan.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "fargan_data.c"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }

static int read_u32(uint32_t *v) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *v = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)v;
  b[1] = (unsigned char)(v >> 8);
  b[2] = (unsigned char)(v >> 16);
  b[3] = (unsigned char)(v >> 24);
  return write_exact(b, 4);
}

int main(void) {
  char magic[4];
  uint32_t version, bits;
  FARGANState state;
  float cond[FARGAN_COND_SIZE];
  float gain;
  int i;

#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  if (!read_exact(magic, 4) || memcmp(magic, "GFGI", 4) != 0 ||
      !read_u32(&version) || version != 1) return 1;
  for (i = 0; i < FARGAN_COND_SIZE; i++) {
    if (!read_u32(&bits)) return 1;
    memcpy(&cond[i], &bits, sizeof(bits));
  }
  if (getchar() != EOF) return 1;

  fargan_init(&state);
  compute_generic_dense(&state.model.sig_net_cond_gain_dense, &gain,
                        cond, ACTIVATION_LINEAR, state.arch);
  if (!write_exact("GFGO", 4) || !write_u32(1)) return 1;
  memcpy(&bits, &gain, sizeof(bits));
  if (!write_u32(bits)) return 1;
  gain = exp(gain);
  memcpy(&bits, &gain, sizeof(bits));
  return write_u32(bits) ? 0 : 1;
}
