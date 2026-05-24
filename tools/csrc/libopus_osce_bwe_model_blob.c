/*
 * libopus_osce_bwe_model_blob.c
 *
 * Extracts the OSCE BWE (blind bandwidth-extension) neural-network weights
 * shipped in libopus 1.6.1 and writes a USE_WEIGHTS_FILE-compatible blob to
 * stdout.  The libopus generated source `dnn/bbwenet_data.c` exposes the
 * `bbwenetlayers_arrays` table when compiled without `USE_WEIGHTS_FILE`; this
 * helper includes that source directly so the resulting binary contains the
 * full set of records named by `osceBWERequiredRecordNames` in
 * internal/dnnblob/model_manifests_generated.go.
 *
 * NOTE: libopus must be configured with `--enable-osce` (or
 * `--enable-osce-training-data`) for `bbwenet_data.c` to be part of the
 * tree's compilation environment AND for `linear_init` / the BBWENETLayers
 * struct's dependencies to be available at link time.  The helper itself
 * does not invoke `init_bbwenetlayers`; it only emits the weight blob.
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
#include "libopus_dnn_blob_io.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "bbwenet_data.c"

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdout mode\n");
    return 1;
  }
  if (!write_weights(stdout, bbwenetlayers_arrays)) {
    fprintf(stderr, "failed to write bbwenetlayers_arrays blob\n");
    return 1;
  }
  return 0;
}
