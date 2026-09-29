/* Bounded observers for the actual Hybrid range-coder boundaries. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"
#include "celt/celt.h"
#include "celt/entcode.h"
#include "silk/API.h"

#ifndef GOPUS_HYBRID_TRACE_FRAME
#define GOPUS_HYBRID_TRACE_FRAME 0
#endif

#define GOPUS_HYBRID_TRACE_RECORDS 3u
#define GOPUS_HYBRID_TRACE_BYTES 4096u

enum {
  GOPUS_HYBRID_SILK_EXIT = 1,
  GOPUS_HYBRID_CELT_ENTRY = 2,
  GOPUS_HYBRID_CELT_EXIT = 3
};

typedef struct {
  uint32_t frame;
  uint32_t stage;
  uint32_t call_index;
  uint32_t storage;
  uint32_t offs;
  uint32_t end_offs;
  uint32_t end_window;
  uint32_t rng;
  uint32_t val;
  uint32_t ext;
  int32_t nbits_total;
  int32_t nend_bits;
  int32_t rem;
  int32_t error;
  int32_t tell;
  int32_t tell_frac;
  uint32_t forward_len;
  uint32_t backward_len;
  unsigned char forward[GOPUS_HYBRID_TRACE_BYTES];
  unsigned char backward[GOPUS_HYBRID_TRACE_BYTES];
} gopus_hybrid_boundary_record;

static gopus_hybrid_boundary_record gopus_records[GOPUS_HYBRID_TRACE_RECORDS];
static uint32_t gopus_record_count;
static uint32_t gopus_overflow;
static uint32_t gopus_frame_calls;
static uint32_t gopus_silk_calls;
static uint32_t gopus_celt_calls;
static uint32_t gopus_target_silk_calls;
static uint32_t gopus_target_celt_calls;
static uint32_t gopus_target_public_calls;
static int32_t gopus_current_frame = -1;
static uint32_t gopus_target_silk_range_calls;
static uint32_t gopus_target_shared_celt_calls;
static ec_enc *gopus_target_ec;
static unsigned char *gopus_target_buffer;
static uint32_t gopus_same_ec = 1;
static uint32_t gopus_same_buffer = 1;

static void gopus_write_u32(uint32_t value) {
  unsigned char bytes[4];
  bytes[0] = (unsigned char)value;
  bytes[1] = (unsigned char)(value >> 8);
  bytes[2] = (unsigned char)(value >> 16);
  bytes[3] = (unsigned char)(value >> 24);
  (void)fwrite(bytes, sizeof(bytes), 1, stdout);
}

static void gopus_write_record(const gopus_hybrid_boundary_record *record) {
  gopus_write_u32(record->frame);
  gopus_write_u32(record->stage);
  gopus_write_u32(record->call_index);
  gopus_write_u32(record->storage);
  gopus_write_u32(record->offs);
  gopus_write_u32(record->end_offs);
  gopus_write_u32(record->end_window);
  gopus_write_u32(record->rng);
  gopus_write_u32(record->val);
  gopus_write_u32(record->ext);
  gopus_write_u32((uint32_t)record->nbits_total);
  gopus_write_u32((uint32_t)record->nend_bits);
  gopus_write_u32((uint32_t)record->rem);
  gopus_write_u32((uint32_t)record->error);
  gopus_write_u32((uint32_t)record->tell);
  gopus_write_u32((uint32_t)record->tell_frac);
  gopus_write_u32(record->forward_len);
  gopus_write_u32(record->backward_len);
  if (record->forward_len != 0) {
    (void)fwrite(record->forward, record->forward_len, 1, stdout);
  }
  if (record->backward_len != 0) {
    (void)fwrite(record->backward, record->backward_len, 1, stdout);
  }
}

static void gopus_emit_trace(void) {
  uint32_t i;
  (void)fwrite("GCHB", 4, 1, stdout);
  gopus_write_u32(1); /* wire version */
  gopus_write_u32(GOPUS_HYBRID_TRACE_FRAME);
  gopus_write_u32(gopus_frame_calls);
  gopus_write_u32(gopus_silk_calls);
  gopus_write_u32(gopus_celt_calls);
  gopus_write_u32(gopus_target_public_calls);
  gopus_write_u32(gopus_target_silk_calls);
  gopus_write_u32(gopus_target_silk_range_calls);
  gopus_write_u32(gopus_target_celt_calls);
  gopus_write_u32(gopus_target_shared_celt_calls);
  gopus_write_u32(gopus_record_count);
  gopus_write_u32(gopus_overflow);
  gopus_write_u32(gopus_same_ec);
  gopus_write_u32(gopus_same_buffer);
  for (i = 0; i < gopus_record_count; i++) {
    gopus_write_record(&gopus_records[i]);
  }
  (void)fflush(stdout);
}

__attribute__((constructor)) static void gopus_register_hybrid_trace(void) {
  if (atexit(gopus_emit_trace) != 0) {
    gopus_overflow = 1;
  }
}

static void gopus_capture_boundary(uint32_t stage, uint32_t call_index,
                                   ec_enc *ec) {
  gopus_hybrid_boundary_record *record;
  uint64_t occupied;
  uint32_t backward_start;
  if (gopus_current_frame != (int32_t)GOPUS_HYBRID_TRACE_FRAME) {
    return;
  }
  if (ec == NULL || gopus_record_count >= GOPUS_HYBRID_TRACE_RECORDS) {
    gopus_overflow = 1;
    return;
  }
  if (stage != gopus_record_count + 1) {
    gopus_overflow = 1;
    return;
  }
  if (gopus_target_ec == NULL) {
    gopus_target_ec = ec;
    gopus_target_buffer = ec->buf;
  } else {
    if (gopus_target_ec != ec) {
      gopus_same_ec = 0;
    }
    if (gopus_target_buffer != ec->buf) {
      gopus_same_buffer = 0;
    }
  }
  occupied = (uint64_t)ec->offs + (uint64_t)ec->end_offs;
  if (ec->storage > GOPUS_HYBRID_TRACE_BYTES || occupied > ec->storage ||
      ec->rng == 0) {
    gopus_overflow = 1;
    return;
  }
  record = &gopus_records[gopus_record_count];
  record->frame = (uint32_t)gopus_current_frame;
  record->stage = stage;
  record->call_index = call_index;
  record->storage = ec->storage;
  record->offs = ec->offs;
  record->end_offs = ec->end_offs;
  record->end_window = ec->end_window;
  record->rng = ec->rng;
  record->val = ec->val;
  record->ext = ec->ext;
  record->nbits_total = ec->nbits_total;
  record->nend_bits = ec->nend_bits;
  record->rem = ec->rem;
  record->error = ec->error;
  record->tell = ec_tell(ec);
  record->tell_frac = (int32_t)ec_tell_frac(ec);
  record->forward_len = ec->offs;
  record->backward_len = ec->end_offs;
  backward_start = ec->storage - ec->end_offs;
  if (record->forward_len != 0) {
    memcpy(record->forward, ec->buf, record->forward_len);
  }
  if (record->backward_len != 0) {
    memcpy(record->backward, ec->buf + backward_start, record->backward_len);
  }
  gopus_record_count++;
}

int __real_opus_encode_float(OpusEncoder *st, const float *pcm,
                             int frame_size, unsigned char *data,
                             int max_data_bytes);
int __wrap_opus_encode_float(OpusEncoder *st, const float *pcm,
                             int frame_size, unsigned char *data,
                             int max_data_bytes) {
  int32_t previous_frame = gopus_current_frame;
  uint32_t frame = gopus_frame_calls++;
  int result;
  if (frame == GOPUS_HYBRID_TRACE_FRAME) {
    gopus_target_public_calls++;
  }
  gopus_current_frame = (int32_t)frame;
  result = __real_opus_encode_float(st, pcm, frame_size, data, max_data_bytes);
  gopus_current_frame = previous_frame;
  return result;
}

opus_int __real_silk_Encode(void *enc_state,
                            silk_EncControlStruct *enc_control,
                            const opus_res *samples_in,
                            opus_int n_samples_in, ec_enc *range_encoder,
                            opus_int32 *n_bytes_out,
                            const opus_int prefill_flag, int activity);
opus_int __wrap_silk_Encode(void *enc_state,
                            silk_EncControlStruct *enc_control,
                            const opus_res *samples_in,
                            opus_int n_samples_in, ec_enc *range_encoder,
                            opus_int32 *n_bytes_out,
                            const opus_int prefill_flag, int activity) {
  opus_int result = __real_silk_Encode(enc_state, enc_control, samples_in,
                                       n_samples_in, range_encoder, n_bytes_out,
                                       prefill_flag, activity);
  gopus_silk_calls++;
  if (gopus_current_frame == (int32_t)GOPUS_HYBRID_TRACE_FRAME) {
    gopus_target_silk_calls++;
    if (prefill_flag == 0 && range_encoder != NULL) {
      uint32_t call_index = ++gopus_target_silk_range_calls;
      gopus_capture_boundary(GOPUS_HYBRID_SILK_EXIT, call_index, range_encoder);
    }
  }
  return result;
}

int __real_celt_encode_with_ec(OpusCustomEncoder *st, const opus_res *pcm,
                               int frame_size, unsigned char *compressed,
                               int nb_bytes, ec_enc *enc);
int __wrap_celt_encode_with_ec(OpusCustomEncoder *st, const opus_res *pcm,
                               int frame_size, unsigned char *compressed,
                               int nb_bytes, ec_enc *enc) {
  int result;
  uint32_t call_index;
  gopus_celt_calls++;
  call_index = 0;
  if (gopus_current_frame == (int32_t)GOPUS_HYBRID_TRACE_FRAME) {
    gopus_target_celt_calls++;
    if (enc != NULL && enc == gopus_target_ec) {
      call_index = ++gopus_target_shared_celt_calls;
      gopus_capture_boundary(GOPUS_HYBRID_CELT_ENTRY, call_index, enc);
    }
  }
  result = __real_celt_encode_with_ec(st, pcm, frame_size, compressed,
                                      nb_bytes, enc);
  if (call_index != 0) {
    gopus_capture_boundary(GOPUS_HYBRID_CELT_EXIT, call_index, enc);
  }
  return result;
}
