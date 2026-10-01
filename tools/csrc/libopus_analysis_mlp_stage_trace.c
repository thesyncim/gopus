/* Bounded live-call trace wrappers for libopus' analysis MLP stages. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "mlp.h"

#ifndef GOPUS_ANALYSIS_SOURCE_SHA256
#error "GOPUS_ANALYSIS_SOURCE_SHA256 must identify selected src/analysis.c"
#endif
#ifndef GOPUS_MLP_SOURCE_SHA256
#error "GOPUS_MLP_SOURCE_SHA256 must identify selected src/mlp.c"
#endif

enum {
  DENSE0_INPUTS = 25,
  DENSE0_OUTPUTS = 32,
  GRU_INPUTS = 32,
  GRU_STATE = 24,
  DENSE2_INPUTS = 24,
  DENSE2_OUTPUTS = 2,
  TRACE_STAGES = 3
};

static uint32_t dense_calls;
static uint32_t gru_calls;
static uint32_t next_stage;
static uint32_t overflow;
static float dense0_input[DENSE0_INPUTS];
static float dense0_output[DENSE0_OUTPUTS];
static float gru_input[GRU_INPUTS];
static float gru_state_before[GRU_STATE];
static float gru_state_after[GRU_STATE];
static float dense2_input[DENSE2_INPUTS];
static float dense2_output[DENSE2_OUTPUTS];

void __real_analysis_compute_dense(const AnalysisDenseLayer *layer,
                                  float *output, const float *input);
void __real_analysis_compute_gru(const AnalysisGRULayer *gru, float *state,
                                 const float *input);

void __wrap_analysis_compute_dense(const AnalysisDenseLayer *layer,
                                   float *output, const float *input) {
  const uint32_t ordinal = dense_calls++;
  int capture_dense0 = ordinal == 0;
  int capture_dense2 = ordinal == 1;

  if (capture_dense0) {
    if (next_stage != 0 || layer->nb_inputs != DENSE0_INPUTS ||
        layer->nb_neurons != DENSE0_OUTPUTS) {
      overflow = 1;
    } else {
      memcpy(dense0_input, input, sizeof(dense0_input));
    }
  } else if (capture_dense2) {
    if (next_stage != 2 || layer->nb_inputs != DENSE2_INPUTS ||
        layer->nb_neurons != DENSE2_OUTPUTS) {
      overflow = 1;
    } else {
      memcpy(dense2_input, input, sizeof(dense2_input));
    }
  }

  __real_analysis_compute_dense(layer, output, input);

  if (capture_dense0) {
    if (layer->nb_inputs == DENSE0_INPUTS &&
        layer->nb_neurons == DENSE0_OUTPUTS) {
      memcpy(dense0_output, output, sizeof(dense0_output));
    }
    next_stage = 1;
  } else if (capture_dense2) {
    if (layer->nb_inputs == DENSE2_INPUTS &&
        layer->nb_neurons == DENSE2_OUTPUTS) {
      memcpy(dense2_output, output, sizeof(dense2_output));
    }
    next_stage = 3;
  }
}

void __wrap_analysis_compute_gru(const AnalysisGRULayer *gru, float *state,
                                 const float *input) {
  const uint32_t ordinal = gru_calls++;
  const int capture = ordinal == 0;

  if (capture) {
    if (next_stage != 1 || gru->nb_inputs != GRU_INPUTS ||
        gru->nb_neurons != GRU_STATE) {
      overflow = 1;
    } else {
      memcpy(gru_input, input, sizeof(gru_input));
      memcpy(gru_state_before, state, sizeof(gru_state_before));
    }
  }

  __real_analysis_compute_gru(gru, state, input);

  if (capture) {
    if (gru->nb_inputs == GRU_INPUTS && gru->nb_neurons == GRU_STATE) {
      memcpy(gru_state_after, state, sizeof(gru_state_after));
    }
    next_stage = 2;
  }
}

static int write_exact(const void *data, size_t size) {
  return fwrite(data, 1, size, stdout) == size;
}

static int write_u32(uint32_t value) {
  const unsigned char bytes[4] = {
      (unsigned char)value,
      (unsigned char)(value >> 8),
      (unsigned char)(value >> 16),
      (unsigned char)(value >> 24),
  };
  return write_exact(bytes, sizeof(bytes));
}

static int write_float32s(const float *values, size_t count) {
  size_t i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &values[i], sizeof(bits));
    if (!write_u32(bits)) return 0;
  }
  return 1;
}

static int write_stage_header(uint32_t stage, uint32_t inputs,
                              uint32_t state_before, uint32_t state_after,
                              uint32_t outputs) {
  return write_u32(stage) && write_u32(inputs) && write_u32(state_before) &&
         write_u32(state_after) && write_u32(outputs);
}

int gopus_analysis_mlp_trace_write(void) {
  static const char magic[4] = {'G', 'A', 'M', 'L'};
  if (!write_exact(magic, sizeof(magic)) || !write_u32(1) ||
      !write_u32(0) || !write_u32(dense_calls) || !write_u32(gru_calls) ||
      !write_u32(TRACE_STAGES) || !write_u32(overflow) ||
      !write_exact(GOPUS_ANALYSIS_SOURCE_SHA256, 64) ||
      !write_exact(GOPUS_MLP_SOURCE_SHA256, 64)) {
    return 0;
  }

  if (!write_stage_header(1, DENSE0_INPUTS, 0, 0, DENSE0_OUTPUTS) ||
      !write_float32s(dense0_input, DENSE0_INPUTS) ||
      !write_float32s(dense0_output, DENSE0_OUTPUTS) ||
      !write_stage_header(2, GRU_INPUTS, GRU_STATE, GRU_STATE, 0) ||
      !write_float32s(gru_input, GRU_INPUTS) ||
      !write_float32s(gru_state_before, GRU_STATE) ||
      !write_float32s(gru_state_after, GRU_STATE) ||
      !write_stage_header(3, DENSE2_INPUTS, 0, 0, DENSE2_OUTPUTS) ||
      !write_float32s(dense2_input, DENSE2_INPUTS) ||
      !write_float32s(dense2_output, DENSE2_OUTPUTS)) {
    return 0;
  }
  return 1;
}
