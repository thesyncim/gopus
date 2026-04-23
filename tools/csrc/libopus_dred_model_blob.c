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

#include "nnet.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "dred_rdovae_dec_data.c"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

static int write_weights(FILE *out, const WeightArray *list) {
  unsigned char zeros[WEIGHT_BLOCK_SIZE] = {0};
  int i = 0;
  while (list[i].name != NULL) {
    WeightHead h;
    memset(&h, 0, sizeof(h));
    memcpy(h.head, "DNNw", 4);
    h.version = WEIGHT_BLOB_VERSION;
    h.type = list[i].type;
    h.size = list[i].size;
    h.block_size = (h.size + WEIGHT_BLOCK_SIZE - 1) / WEIGHT_BLOCK_SIZE * WEIGHT_BLOCK_SIZE;
    strncpy(h.name, list[i].name, sizeof(h.name) - 1);
    if (fwrite(&h, 1, sizeof(h), out) != sizeof(h)) {
      return 0;
    }
    if (h.size > 0 && fwrite(list[i].data, 1, h.size, out) != (size_t)h.size) {
      return 0;
    }
    if (h.block_size > h.size &&
        fwrite(zeros, 1, (size_t)(h.block_size - h.size), out) != (size_t)(h.block_size - h.size)) {
      return 0;
    }
    i++;
  }
  return 1;
}

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdout mode\n");
    return 1;
  }
  if (!write_weights(stdout, rdovaedec_arrays)) {
    fprintf(stderr, "failed to write rdovaedec_arrays blob\n");
    return 1;
  }
  return 0;
}
