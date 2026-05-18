/*
 * libopus_osce_lace_model_blob.c
 *
 * Extracts the OSCE LACE/NoLACE postfilter neural-network weights shipped in
 * libopus 1.6.1 and writes a USE_WEIGHTS_FILE-compatible blob to stdout.  The
 * libopus generated sources `dnn/lace_data.c` and `dnn/nolace_data.c` expose
 * the `lacelayers_arrays` and `nolacelayers_arrays` tables when compiled
 * without `USE_WEIGHTS_FILE`; this helper includes both sources directly so
 * the resulting binary contains the full set of records named by
 * `osceLACERequiredRecordNames` and `osceNoLACERequiredRecordNames` in
 * internal/dnnblob/model_manifests_generated.go.
 *
 * NOTE: libopus must be configured with `--enable-osce` (or
 * `--enable-osce-training-data`) for `lace_data.c` / `nolace_data.c` to be
 * part of the tree's compilation environment AND for `linear_init` /
 * the (NO)LACELayers struct's dependencies to be available at link time.  The
 * helper itself does not invoke `init_lacelayers` / `init_nolacelayers`; it
 * only emits the weight blobs.
 */

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
#include "lace_data.c"
#include "nolace_data.c"

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
  if (!write_weights(stdout, lacelayers_arrays)) {
    fprintf(stderr, "failed to write lacelayers_arrays blob\n");
    return 1;
  }
  if (!write_weights(stdout, nolacelayers_arrays)) {
    fprintf(stderr, "failed to write nolacelayers_arrays blob\n");
    return 1;
  }
  return 0;
}
