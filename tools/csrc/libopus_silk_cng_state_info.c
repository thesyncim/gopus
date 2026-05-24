#include <stdint.h>
#include <stdio.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "silk/main.h"

#define INPUT_MAGIC "GCNI"
#define OUTPUT_MAGIC "GCNO"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}

static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}

static int read_u32(uint32_t *out) {
  return read_exact(out, sizeof(*out));
}

static int write_u32(uint32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_i32(int32_t value) {
  return write_exact(&value, sizeof(value));
}

static int write_cng_state_sizes(void) {
  silk_CNG_struct state;
  return write_i32((int32_t)sizeof(state.CNG_exc_buf_Q14[0])) &&
         write_i32((int32_t)sizeof(state.CNG_smth_NLSF_Q15[0])) &&
         write_i32((int32_t)sizeof(state.CNG_synth_state[0])) &&
         write_i32((int32_t)sizeof(state.CNG_smth_Gain_Q16)) &&
         write_i32((int32_t)sizeof(state.rand_seed)) &&
         write_i32((int32_t)sizeof(state.fs_kHz)) &&
         write_i32((int32_t)sizeof(state)) &&
         write_i32((int32_t)MAX_LPC_ORDER);
}

int main(void) {
  char magic[4];
  uint32_t version;
  uint32_t mode;
  uint32_t count;
  uint32_t i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) return 1;
  if (!read_u32(&version) || version != 1 || !read_u32(&mode) || !read_u32(&count)) return 1;
  if (mode != 0) return 1;

  if (!write_exact(OUTPUT_MAGIC, sizeof(magic)) || !write_u32(1) || !write_u32(count)) return 1;
  for (i = 0; i < count; i++) {
    if (!write_cng_state_sizes()) return 1;
  }
  return 0;
}
