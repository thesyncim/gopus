/*
 * Frame-scoped CELT band-quantization trace. This wraps the public VBR
 * entropy-trace helper once, so packet, stage, entropy, and GQTR records all
 * come from the same two-frame encode sequence.
 */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "libopus_encode_diff_celt_entropy_trace_included.c"

#define GQTR_MAX_EVENTS 64u
#define GQTR_MAX_WIDTH 256u
#define GQTR_NO_THETA UINT32_MAX

enum {
  GQTR_THETA = 1,
  GQTR_PVQ = 2,
  GQTR_STEREO_MERGE = 3,
  GQTR_BAND_OUTPUT = 4,
  GQTR_RDO_SELECT = 5
};

typedef struct {
  uint32_t stage;
  uint32_t ordinal;
  uint32_t theta_ordinal;
  uint32_t band;
  uint32_t n;
  uint32_t B;
  uint32_t B0;
  int32_t lm;
  uint32_t channels;
  uint32_t encode;
  uint32_t stereo;
  int32_t theta_round;
  uint32_t range_before;
  uint32_t range_after;
  uint32_t tell_frac_before;
  uint32_t tell_frac_after;
} gqtr_header;

typedef struct {
  int32_t b_before, b_after;
  int32_t fill_before, fill_after;
  int32_t qn, pulse_cap, offset, raw_itheta_q30;
  int32_t itheta, itheta_q30, inv, imid, iside, delta, qalloc;
  int32_t remaining_before, remaining_after;
  float energy_l, energy_r;
  float x_before[GQTR_MAX_WIDTH];
  float y_before[GQTR_MAX_WIDTH];
  float x_after[GQTR_MAX_WIDTH];
  float y_after[GQTR_MAX_WIDTH];
} gqtr_theta;

typedef struct {
  int32_t k, spread, resynth;
  uint32_t collapse;
  float gain;
  float x_before[GQTR_MAX_WIDTH];
  float x_after[GQTR_MAX_WIDTH];
} gqtr_pvq;

typedef struct {
  float mid;
  float x_before[GQTR_MAX_WIDTH];
  float y_before[GQTR_MAX_WIDTH];
  float x_after[GQTR_MAX_WIDTH];
  float y_after[GQTR_MAX_WIDTH];
} gqtr_merge;

typedef struct {
  uint32_t collapse;
  float x[GQTR_MAX_WIDTH];
  float y[GQTR_MAX_WIDTH];
} gqtr_band_output;

typedef struct {
  float dist0, dist1;
  int32_t selected_round;
  float x[GQTR_MAX_WIDTH];
  float y[GQTR_MAX_WIDTH];
} gqtr_rdo;

typedef struct {
  gqtr_header header;
  union {
    gqtr_theta theta;
    gqtr_pvq pvq;
    gqtr_merge merge;
    gqtr_band_output band_output;
    gqtr_rdo rdo;
  } payload;
} gqtr_event;

static struct {
  uint32_t count;
  uint32_t overflow;
  uint32_t channels;
  int32_t current_theta;
  int32_t last_band_output_theta;
  int32_t pvq_b0;
  int32_t pvq_lm;
  uint32_t pvq_context_valid;
  gqtr_event events[GQTR_MAX_EVENTS];
} gqtr = {
  .current_theta = -1,
  .last_band_output_theta = -1,
  .pvq_b0 = -1,
  .pvq_lm = -1
};

static int gqtr_copy(float *dst, const celt_norm *src, int n) {
  if (n < 0 || (uint32_t)n > GQTR_MAX_WIDTH || src == NULL) {
    gqtr.overflow = 1;
    return 0;
  }
  memcpy(dst, src, (size_t)n * sizeof(*dst));
  return 1;
}

static int gqtr_copy_optional(float *dst, const celt_norm *src, int n) {
  if (n < 0 || (uint32_t)n > GQTR_MAX_WIDTH) {
    gqtr.overflow = 1;
    return 0;
  }
  if (src != NULL) memcpy(dst, src, (size_t)n * sizeof(*dst));
  return 1;
}

static int gqtr_reserve(uint32_t stage, uint32_t theta_ordinal,
    const gqtr_header *base, int n) {
  gqtr_event *event;
  if (n < 0 || (uint32_t)n > GQTR_MAX_WIDTH || gqtr.count >= GQTR_MAX_EVENTS) {
    gqtr.overflow = 1;
    return -1;
  }
  event = &gqtr.events[gqtr.count];
  memset(event, 0, sizeof(*event));
  event->header = *base;
  event->header.stage = stage;
  event->header.ordinal = gqtr.count;
  event->header.theta_ordinal = theta_ordinal;
  event->header.n = (uint32_t)n;
  gqtr.count++;
  return (int)event->header.ordinal;
}

static int gqtr_header_from_theta(gqtr_header *dst, int32_t theta_ordinal,
    uint32_t stage, int n, int B) {
  if (theta_ordinal < 0 || (uint32_t)theta_ordinal >= gqtr.count ||
      gqtr.events[theta_ordinal].header.stage != GQTR_THETA) {
    gqtr.overflow = 1;
    return 0;
  }
  *dst = gqtr.events[theta_ordinal].header;
  dst->stage = stage;
  dst->theta_ordinal = (uint32_t)theta_ordinal;
  dst->n = (uint32_t)n;
  if (B >= 0) dst->B = (uint32_t)B;
  return 1;
}

static void gqtr_set_range_after(gqtr_event *event, ec_enc *ec) {
  if (ec == NULL) {
    gqtr.overflow = 1;
    return;
  }
  event->header.range_after = ec->rng;
  event->header.tell_frac_after = ec_tell_frac(ec);
}

void gopus_celt_quant_set_channels(int channels) {
  if (trace_selected_frame()) {
    if (channels < 1 || channels > 2) {
      gqtr.overflow = 1;
      return;
    }
    gqtr.channels = (uint32_t)channels;
  }
}

int gopus_celt_quant_theta_begin(int band, int n, int B, int B0, int LM,
    int encode, int stereo, int theta_round, int b, int fill, int remaining,
    const celt_ener *bandE, int nbEBands, ec_enc *ec,
    const celt_norm *X, const celt_norm *Y) {
  gqtr_header header;
  int ordinal;
  if (!trace_selected_frame()) return -1;
  gqtr.current_theta = -1;
  if (band != 17) return -1;
  if (ec == NULL || bandE == NULL || nbEBands <= band || X == NULL || gqtr.channels == 0) {
    gqtr.overflow = 1;
    return -1;
  }
  memset(&header, 0, sizeof(header));
  header.stage = GQTR_THETA;
  header.band = (uint32_t)band;
  header.B = (uint32_t)B;
  header.B0 = (uint32_t)B0;
  header.lm = (int32_t)LM;
  header.channels = gqtr.channels;
  header.encode = encode != 0;
  header.stereo = stereo != 0;
  header.theta_round = (int32_t)theta_round;
  header.range_before = ec->rng;
  header.tell_frac_before = ec_tell_frac(ec);
  ordinal = gqtr_reserve(GQTR_THETA, GQTR_NO_THETA, &header, n);
  if (ordinal < 0) return -1;
  gqtr.events[ordinal].header.theta_ordinal = (uint32_t)ordinal;
  gqtr.current_theta = ordinal;
  gqtr_theta *payload = &gqtr.events[ordinal].payload.theta;
  payload->b_before = b;
  payload->fill_before = fill;
  payload->remaining_before = remaining;
  payload->energy_l = bandE[band];
  payload->energy_r = gqtr.channels > 1 ? bandE[band + nbEBands] : 0;
  if (!gqtr_copy(payload->x_before, X, n)) return -1;
  (void)gqtr_copy_optional(payload->y_before, Y, n);
  return ordinal;
}

void gopus_celt_quant_theta_meta(int ordinal, int qn, int pulse_cap,
    int offset, int raw_itheta_q30) {
  if (ordinal < 0 || (uint32_t)ordinal >= gqtr.count) return;
  if (gqtr.events[ordinal].header.stage != GQTR_THETA) {
    gqtr.overflow = 1;
    return;
  }
  gqtr_theta *payload = &gqtr.events[ordinal].payload.theta;
  payload->qn = qn;
  payload->pulse_cap = pulse_cap;
  payload->offset = offset;
  payload->raw_itheta_q30 = raw_itheta_q30;
}

void gopus_celt_quant_theta_end(int ordinal, int b, int fill, int itheta,
    int itheta_q30, int inv, int imid, int iside, int delta, int qalloc,
    int remaining, const celt_norm *X, const celt_norm *Y, ec_enc *ec) {
  if (ordinal < 0 || (uint32_t)ordinal >= gqtr.count) return;
  if (gqtr.events[ordinal].header.stage != GQTR_THETA) {
    gqtr.overflow = 1;
    return;
  }
  gqtr_event *event = &gqtr.events[ordinal];
  gqtr_theta *payload = &event->payload.theta;
  payload->b_after = b;
  payload->fill_after = fill;
  payload->itheta = itheta;
  payload->itheta_q30 = itheta_q30;
  payload->inv = inv;
  payload->imid = imid;
  payload->iside = iside;
  payload->delta = delta;
  payload->qalloc = qalloc;
  payload->remaining_after = remaining;
  (void)gqtr_copy(payload->x_after, X, (int)event->header.n);
  (void)gqtr_copy_optional(payload->y_after, Y, (int)event->header.n);
  gqtr_set_range_after(event, ec);
}

int gopus_celt_quant_current_theta_ordinal(void) {
  if (!trace_selected_frame()) return -1;
  return gqtr.current_theta;
}

/* A quant_partition split keeps its theta in a local split_ctx. Recursive
 * children may emit nested theta events, but a sibling resumes with the
 * enclosing split's local state. The top-level quant_band caller also keeps
 * its enclosing stereo theta active between the mid and side trees. Save and
 * restore the diagnostic context at each quant_partition call boundary. */
int gopus_celt_quant_theta_push(void) {
  if (!trace_selected_frame()) return -1;
  return gqtr.current_theta;
}

void gopus_celt_quant_theta_pop(int previous_theta) {
  if (!trace_selected_frame()) return;
  if (previous_theta < -1 ||
      (previous_theta >= 0 &&
       ((uint32_t)previous_theta >= gqtr.count ||
        gqtr.events[previous_theta].header.stage != GQTR_THETA ||
        gqtr.events[previous_theta].header.band != 17))) {
    gqtr.overflow = 1;
    return;
  }
  gqtr.current_theta = previous_theta;
}

int gopus_celt_quant_last_band_output_theta_ordinal(void) {
  if (!trace_selected_frame()) return -1;
  return gqtr.last_band_output_theta;
}

void gopus_celt_quant_pvq_context(int B0, int LM) {
  gqtr.pvq_context_valid = 0;
  if (!trace_selected_frame() || gqtr.current_theta < 0 ||
      (uint32_t)gqtr.current_theta >= gqtr.count ||
      gqtr.events[gqtr.current_theta].header.band != 17) return;
  if (B0 <= 0 || LM < -1) {
    gqtr.overflow = 1;
    return;
  }
  gqtr.pvq_b0 = (int32_t)B0;
  gqtr.pvq_lm = (int32_t)LM;
  gqtr.pvq_context_valid = 1;
}

extern unsigned __real_alg_quant(celt_norm *X, int N, int K, int spread, int B,
    ec_enc *enc, opus_val32 gain, int resynth
    ARG_QEXT(ec_enc *ext_enc) ARG_QEXT(int extra_bits), int arch);
unsigned __wrap_alg_quant(celt_norm *X, int N, int K, int spread, int B,
    ec_enc *enc, opus_val32 gain, int resynth
    ARG_QEXT(ec_enc *ext_enc) ARG_QEXT(int extra_bits), int arch) {
  int theta_ordinal = gqtr.current_theta;
  int ordinal = -1;
  unsigned collapse;
  gqtr_header header;
  if (trace_selected_frame() && theta_ordinal >= 0 &&
      (uint32_t)theta_ordinal < gqtr.count &&
      gqtr.events[theta_ordinal].header.band == 17 &&
      gqtr_header_from_theta(&header, theta_ordinal, GQTR_PVQ, N, B)) {
    if (!gqtr.pvq_context_valid) {
      gqtr.overflow = 1;
    } else {
      header.B0 = (uint32_t)gqtr.pvq_b0;
      header.lm = gqtr.pvq_lm;
    }
    ordinal = gqtr_reserve(GQTR_PVQ, (uint32_t)theta_ordinal, &header, N);
    if (ordinal >= 0) {
      gqtr_event *event = &gqtr.events[ordinal];
      gqtr_pvq *payload = &event->payload.pvq;
      if (enc == NULL) {
        gqtr.overflow = 1;
      } else {
        event->header.range_before = enc->rng;
        event->header.tell_frac_before = ec_tell_frac(enc);
      }
      payload->k = K;
      payload->spread = spread;
      payload->resynth = resynth;
      payload->gain = gain;
      (void)gqtr_copy(payload->x_before, X, N);
    }
  }
  collapse = __real_alg_quant(X, N, K, spread, B, enc, gain, resynth
      ARG_QEXT(ext_enc) ARG_QEXT(extra_bits), arch);
  gqtr.pvq_context_valid = 0;
  if (ordinal >= 0) {
    gqtr_event *event = &gqtr.events[ordinal];
    event->payload.pvq.collapse = (uint32_t)collapse;
    (void)gqtr_copy(event->payload.pvq.x_after, X, N);
    gqtr_set_range_after(event, enc);
  }
  return collapse;
}

int gopus_celt_quant_merge_begin(int theta_ordinal, int N, opus_val32 mid,
    const celt_norm *X, const celt_norm *Y, ec_enc *ec) {
  gqtr_header header;
  int ordinal;
  if (!trace_selected_frame() || theta_ordinal < 0 ||
      !gqtr_header_from_theta(&header, theta_ordinal, GQTR_STEREO_MERGE, N, -1)) return -1;
  ordinal = gqtr_reserve(GQTR_STEREO_MERGE, (uint32_t)theta_ordinal, &header, N);
  if (ordinal < 0) return -1;
  gqtr_event *event = &gqtr.events[ordinal];
  if (ec == NULL) {
    gqtr.overflow = 1;
  } else {
    event->header.range_before = ec->rng;
    event->header.tell_frac_before = ec_tell_frac(ec);
  }
  event->payload.merge.mid = mid;
  (void)gqtr_copy(event->payload.merge.x_before, X, N);
  (void)gqtr_copy(event->payload.merge.y_before, Y, N);
  return ordinal;
}

void gopus_celt_quant_merge_end(int ordinal, const celt_norm *X,
    const celt_norm *Y, ec_enc *ec) {
  if (ordinal < 0 || (uint32_t)ordinal >= gqtr.count) return;
  gqtr_event *event = &gqtr.events[ordinal];
  if (event->header.stage != GQTR_STEREO_MERGE) {
    gqtr.overflow = 1;
    return;
  }
  (void)gqtr_copy(event->payload.merge.x_after, X, (int)event->header.n);
  (void)gqtr_copy(event->payload.merge.y_after, Y, (int)event->header.n);
  gqtr_set_range_after(event, ec);
}

void gopus_celt_quant_band_output(int theta_ordinal, int N, unsigned collapse,
    uint32_t range_before, uint32_t tell_frac_before,
    const celt_norm *X, const celt_norm *Y, ec_enc *ec) {
  gqtr_header header;
  int ordinal;
  if (!trace_selected_frame() || theta_ordinal < 0 ||
      !gqtr_header_from_theta(&header, theta_ordinal, GQTR_BAND_OUTPUT, N, -1)) return;
  ordinal = gqtr_reserve(GQTR_BAND_OUTPUT, (uint32_t)theta_ordinal, &header, N);
  if (ordinal < 0) return;
  gqtr_event *event = &gqtr.events[ordinal];
  event->payload.band_output.collapse = (uint32_t)collapse;
  event->header.range_before = range_before;
  event->header.tell_frac_before = tell_frac_before;
  (void)gqtr_copy(event->payload.band_output.x, X, N);
  (void)gqtr_copy(event->payload.band_output.y, Y, N);
  gqtr_set_range_after(event, ec);
  gqtr.last_band_output_theta = theta_ordinal;
}

void gopus_celt_quant_rdo_select(int band, int N, int B, int B0, int LM,
    int encode, int theta_round, int selected_round, int selected_theta_ordinal,
    opus_val32 dist0, opus_val32 dist1, uint32_t range_before,
    uint32_t tell_frac_before, ec_enc *ec, const celt_norm *X,
    const celt_norm *Y) {
  gqtr_header header;
  int ordinal;
  if (!trace_selected_frame() || band != 17 || selected_theta_ordinal < 0 ||
      !gqtr_header_from_theta(&header, selected_theta_ordinal, GQTR_RDO_SELECT, N, B)) return;
  header.band = (uint32_t)band;
  header.B0 = (uint32_t)B0;
  header.lm = (int32_t)LM;
  header.encode = encode != 0;
  header.stereo = 1;
  header.theta_round = (int32_t)theta_round;
  header.range_before = range_before;
  header.tell_frac_before = tell_frac_before;
  ordinal = gqtr_reserve(GQTR_RDO_SELECT, (uint32_t)selected_theta_ordinal, &header, N);
  if (ordinal < 0) return;
  gqtr_event *event = &gqtr.events[ordinal];
  event->payload.rdo.dist0 = dist0;
  event->payload.rdo.dist1 = dist1;
  event->payload.rdo.selected_round = selected_round;
  (void)gqtr_copy(event->payload.rdo.x, X, N);
  (void)gqtr_copy(event->payload.rdo.y, Y, N);
  gqtr_set_range_after(event, ec);
}

static int gqtr_write_header(const gqtr_header *h) {
  return write_u32(h->stage) && write_u32(h->ordinal) && write_u32(h->theta_ordinal) &&
      write_u32(h->band) && write_u32(h->n) && write_u32(h->B) && write_u32(h->B0) &&
      write_u32((uint32_t)h->lm) && write_u32(h->channels) && write_u32(h->encode) &&
      write_u32(h->stereo) && write_u32((uint32_t)h->theta_round) &&
      write_u32(h->range_before) && write_u32(h->range_after) &&
      write_u32(h->tell_frac_before) && write_u32(h->tell_frac_after);
}

static int gqtr_write_event(const gqtr_event *event) {
  const gqtr_header *h = &event->header;
  uint32_t n = h->n;
  if (!gqtr_write_header(h)) return 0;
  switch (h->stage) {
    case GQTR_THETA: {
      const gqtr_theta *p = &event->payload.theta;
      const int32_t ints[] = {p->b_before, p->b_after, p->fill_before, p->fill_after,
        p->qn, p->pulse_cap, p->offset, p->raw_itheta_q30, p->itheta, p->itheta_q30,
        p->inv, p->imid, p->iside, p->delta, p->qalloc, p->remaining_before,
        p->remaining_after};
      for (uint32_t i = 0; i < sizeof(ints) / sizeof(ints[0]); i++)
        if (!write_u32((uint32_t)ints[i])) return 0;
      if (!trace_write_float32(&p->energy_l, 2) || !trace_write_float32(p->x_before, n) ||
          !trace_write_float32(p->y_before, n) || !trace_write_float32(p->x_after, n) ||
          !trace_write_float32(p->y_after, n)) return 0;
      return 1;
    }
    case GQTR_PVQ: {
      const gqtr_pvq *p = &event->payload.pvq;
      return write_u32((uint32_t)p->k) && write_u32((uint32_t)p->spread) &&
          write_u32((uint32_t)p->resynth) && write_u32(p->collapse) &&
          trace_write_float32(&p->gain, 1) && trace_write_float32(p->x_before, n) &&
          trace_write_float32(p->x_after, n);
    }
    case GQTR_STEREO_MERGE: {
      const gqtr_merge *p = &event->payload.merge;
      return trace_write_float32(&p->mid, 1) && trace_write_float32(p->x_before, n) &&
          trace_write_float32(p->y_before, n) && trace_write_float32(p->x_after, n) &&
          trace_write_float32(p->y_after, n);
    }
    case GQTR_BAND_OUTPUT: {
      const gqtr_band_output *p = &event->payload.band_output;
      return write_u32(p->collapse) && trace_write_float32(p->x, n) &&
          trace_write_float32(p->y, n);
    }
    case GQTR_RDO_SELECT: {
      const gqtr_rdo *p = &event->payload.rdo;
      return trace_write_float32(&p->dist0, 2) &&
          write_u32((uint32_t)p->selected_round) && trace_write_float32(p->x, n) &&
          trace_write_float32(p->y, n);
    }
    default:
      return 0;
  }
}

static int write_gqtr(void) {
  if (!write_exact("GQTR", 4) || !write_u32(1) || !write_u32(TRACE_FRAME) ||
      !write_u32(gqtr.count) || !write_u32(gqtr.overflow) ||
      !write_u32(GQTR_MAX_EVENTS) || !write_u32(GQTR_MAX_WIDTH)) return 0;
  for (uint32_t i = 0; i < gqtr.count; i++)
    if (!gqtr_write_event(&gqtr.events[i])) return 0;
  return 1;
}

int main(void) {
  int result = gopus_encode_diff_celt_entropy_trace_main();
  if (result != 0) return result;
  if (!write_gqtr()) {
    fprintf(stderr, "write CELT GQTR trace failed\n");
    return 1;
  }
  return 0;
}
