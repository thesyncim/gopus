/* Build the public fixed encoder helper with ENABLE_QEXT and turn QEXT on for
 * every probe before it performs its first opus_encode call. */

#include "config.h"

#if !defined(FIXED_POINT) || !defined(ENABLE_RES24) || !defined(ENABLE_QEXT)
#error "fixed-QEXT encoder oracle requires FIXED_POINT ENABLE_RES24 ENABLE_QEXT"
#endif

#include "opus.h"
#include "opus_defines.h"
#include "opus_private.h"

static OpusEncoder *gopus_fixed_qext_encoder_create(opus_int32 sample_rate,
    int channels, int application, int *error) {
  OpusEncoder *enc = opus_encoder_create(sample_rate, channels, application, error);
  if (enc != NULL && opus_encoder_ctl(enc, OPUS_SET_QEXT(1)) != OPUS_OK) {
    opus_encoder_destroy(enc);
    if (error != NULL) *error = OPUS_INTERNAL_ERROR;
    return NULL;
  }
  return enc;
}

/* Reuse the public input protocol and frame loop, but replace encoder creation
 * so the fixed-QEXT oracle starts with OPUS_SET_QEXT(1) applied. */
#define opus_encoder_create gopus_fixed_qext_encoder_create
#include "libopus_opus_encode_fixed_info.c"
#undef opus_encoder_create
