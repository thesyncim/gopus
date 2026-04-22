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
#include "lpcnet_private.h"
#include "plc_data.h"
#include "fargan.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "plc_data.c"
#include "fargan_data.c"

#define INPUT_MAGIC "GPCI"
#define OUTPUT_MAGIC "GPCO"

static const float att_table[10] = {0, 0, -.2f, -.2f, -.4f, -.4f, -.8f, -.8f, -1.6f, -1.6f};

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

static int read_bits_array(float *dst, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    if (!read_exact(&bits, sizeof(bits))) return 0;
    memcpy(&dst[i], &bits, sizeof(bits));
  }
  return 1;
}

static int write_bits_array(const float *src, int count) {
  int i;
  for (i = 0; i < count; i++) {
    uint32_t bits;
    memcpy(&bits, &src[i], sizeof(bits));
    if (!write_exact(&bits, sizeof(bits))) return 0;
  }
  return 1;
}

static void compute_generic_dense_c(const LinearLayer *layer, float *output, const float *input, int activation) {
  compute_linear_c(layer, output, input);
  compute_activation_c(output, output, layer->nb_outputs, activation);
}

static void compute_generic_gru_c(const LinearLayer *input_weights, const LinearLayer *recurrent_weights, float *state, const float *in) {
  int i;
  int n;
  float zrh[3*PLC_GRU1_STATE_SIZE];
  float recur[3*PLC_GRU1_STATE_SIZE];
  float *z;
  float *r;
  float *h;

  n = recurrent_weights->nb_inputs;
  z = zrh;
  r = &zrh[n];
  h = &zrh[2*n];
  compute_linear_c(input_weights, zrh, in);
  compute_linear_c(recurrent_weights, recur, state);
  for (i = 0; i < 2*n; i++) zrh[i] += recur[i];
  compute_activation_c(zrh, zrh, 2*n, ACTIVATION_SIGMOID);
  for (i = 0; i < n; i++) h[i] += recur[2*n+i]*r[i];
  compute_activation_c(h, h, n, ACTIVATION_TANH);
  for (i = 0; i < n; i++) {
    h[i] = z[i]*state[i] + (1-z[i])*h[i];
    state[i] = h[i];
  }
}

static void compute_plc_pred_info(LPCNetPLCState *st, float *out, const float *in) {
  float tmp[PLC_DENSE_IN_OUT_SIZE];
  PLCModel *model = &st->model;
  PLCNetState *net = &st->plc_net;
  compute_generic_dense_c(&model->plc_dense_in, tmp, in, ACTIVATION_TANH);
  compute_generic_gru_c(&model->plc_gru1_input, &model->plc_gru1_recurrent, net->gru1_state, tmp);
  compute_generic_gru_c(&model->plc_gru2_input, &model->plc_gru2_recurrent, net->gru2_state, net->gru1_state);
  compute_generic_dense_c(&model->plc_dense_out, out, net->gru2_state, ACTIVATION_LINEAR);
}

static int get_fec_or_pred_info(LPCNetPLCState *st, float *out) {
  if (st->fec_read_pos != st->fec_fill_pos && st->fec_skip == 0) {
    float plc_features[2*NB_BANDS + NB_FEATURES + 1] = {0};
    float discard[NB_FEATURES];
    memcpy(out, &st->fec[st->fec_read_pos][0], NB_FEATURES * sizeof(float));
    st->fec_read_pos++;
    memcpy(&plc_features[2*NB_BANDS], out, NB_FEATURES * sizeof(float));
    plc_features[2*NB_BANDS + NB_FEATURES] = -1;
    compute_plc_pred_info(st, discard, plc_features);
    return 1;
  } else {
    float zeros[2*NB_BANDS + NB_FEATURES + 1] = {0};
    compute_plc_pred_info(st, out, zeros);
    if (st->fec_skip > 0) st->fec_skip--;
    return 0;
  }
}

static void queue_features_info(LPCNetPLCState *st, const float *features) {
  memmove(&st->cont_features[0], &st->cont_features[NB_FEATURES], (CONT_VECTORS - 1) * NB_FEATURES * sizeof(float));
  memcpy(&st->cont_features[(CONT_VECTORS - 1) * NB_FEATURES], features, NB_FEATURES * sizeof(float));
}

static void rotate_bak_info(LPCNetPLCState *st) {
  st->plc_bak[0] = st->plc_bak[1];
  st->plc_bak[1] = st->plc_net;
}

int main(void) {
  char magic[4];
  uint32_t version;
  int32_t blend;
  int32_t loss_count;
  int32_t analysis_gap;
  int32_t analysis_pos;
  int32_t predict_pos;
  int32_t fec_read_pos;
  int32_t fec_fill_pos;
  int32_t fec_skip;
  int32_t fargan_cont_initialized;
  int32_t got_fec;
  float fec0[NB_FEATURES];
  float fec1[NB_FEATURES];
  float pcmf[FRAME_SIZE];
  opus_int16 pcm16[FRAME_SIZE];
  LPCNetPLCState st;

  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  if (!read_exact(magic, sizeof(magic)) || memcmp(magic, INPUT_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid input magic\n");
    return 1;
  }
  if (!read_exact(&version, sizeof(version)) || version != 1) {
    fprintf(stderr, "unsupported input version\n");
    return 1;
  }
  if (!read_exact(&blend, sizeof(blend)) ||
      !read_exact(&loss_count, sizeof(loss_count)) ||
      !read_exact(&analysis_gap, sizeof(analysis_gap)) ||
      !read_exact(&analysis_pos, sizeof(analysis_pos)) ||
      !read_exact(&predict_pos, sizeof(predict_pos)) ||
      !read_exact(&fec_read_pos, sizeof(fec_read_pos)) ||
      !read_exact(&fec_fill_pos, sizeof(fec_fill_pos)) ||
      !read_exact(&fec_skip, sizeof(fec_skip))) {
    fprintf(stderr, "failed to read conceal control header\n");
    return 1;
  }

  memset(&st, 0, sizeof(st));
  if (init_plcmodel(&st.model, plcmodel_arrays)) {
    fprintf(stderr, "failed to init plc model\n");
    return 1;
  }
  fargan_init(&st.fargan);
  st.loaded = 1;
  st.blend = blend;
  st.loss_count = loss_count;
  st.analysis_gap = analysis_gap;
  st.analysis_pos = analysis_pos;
  st.predict_pos = predict_pos;
  st.fec_read_pos = fec_read_pos;
  st.fec_fill_pos = fec_fill_pos < 0 ? 0 : (fec_fill_pos > 2 ? 2 : fec_fill_pos);
  st.fec_skip = fec_skip < 0 ? 0 : fec_skip;

  if (!read_bits_array(st.features, NB_TOTAL_FEATURES) ||
      !read_bits_array(st.cont_features, CONT_VECTORS * NB_FEATURES) ||
      !read_bits_array(st.pcm, PLC_BUF_SIZE) ||
      !read_bits_array(st.plc_net.gru1_state, PLC_GRU1_STATE_SIZE) ||
      !read_bits_array(st.plc_net.gru2_state, PLC_GRU2_STATE_SIZE) ||
      !read_bits_array(st.plc_bak[0].gru1_state, PLC_GRU1_STATE_SIZE) ||
      !read_bits_array(st.plc_bak[0].gru2_state, PLC_GRU2_STATE_SIZE) ||
      !read_bits_array(st.plc_bak[1].gru1_state, PLC_GRU1_STATE_SIZE) ||
      !read_bits_array(st.plc_bak[1].gru2_state, PLC_GRU2_STATE_SIZE) ||
      !read_exact(&fargan_cont_initialized, sizeof(fargan_cont_initialized)) ||
      !read_exact(&st.fargan.last_period, sizeof(st.fargan.last_period)) ||
      !read_bits_array(&st.fargan.deemph_mem, 1) ||
      !read_bits_array(st.fargan.pitch_buf, PITCH_MAX_PERIOD) ||
      !read_bits_array(st.fargan.cond_conv1_state, COND_NET_FCONV1_STATE_SIZE) ||
      !read_bits_array(st.fargan.fwc0_mem, SIG_NET_FWC0_STATE_SIZE) ||
      !read_bits_array(st.fargan.gru1_state, SIG_NET_GRU1_STATE_SIZE) ||
      !read_bits_array(st.fargan.gru2_state, SIG_NET_GRU2_STATE_SIZE) ||
      !read_bits_array(st.fargan.gru3_state, SIG_NET_GRU3_STATE_SIZE) ||
      !read_bits_array(fec0, NB_FEATURES) ||
      !read_bits_array(fec1, NB_FEATURES)) {
    fprintf(stderr, "failed to read conceal state payload\n");
    return 1;
  }
  st.fargan.cont_initialized = fargan_cont_initialized;

  if (st.fec_fill_pos > 0) memcpy(&st.fec[0][0], fec0, NB_FEATURES * sizeof(float));
  if (st.fec_fill_pos > 1) memcpy(&st.fec[1][0], fec1, NB_FEATURES * sizeof(float));

  if (st.blend == 0) {
    st.plc_net = st.plc_bak[0];
    rotate_bak_info(&st);
    get_fec_or_pred_info(&st, st.features);
    queue_features_info(&st, st.features);
    rotate_bak_info(&st);
    get_fec_or_pred_info(&st, st.features);
    queue_features_info(&st, st.features);
    fargan_cont(&st.fargan, &st.pcm[PLC_BUF_SIZE-FARGAN_CONT_SAMPLES], st.cont_features);
    st.analysis_gap = 0;
  }

  rotate_bak_info(&st);
  got_fec = get_fec_or_pred_info(&st, st.features);
  if (got_fec) st.loss_count = 0;
  else st.loss_count++;
  if (st.loss_count >= 10) st.features[0] = MAX16(-15, st.features[0] + att_table[9] - 2 * (st.loss_count - 9));
  else st.features[0] = MAX16(-15, st.features[0] + att_table[st.loss_count]);

  fargan_synthesize_int(&st.fargan, pcm16, &st.features[0]);
  for (int i = 0; i < FRAME_SIZE; i++) pcmf[i] = (1.f / 32768.f) * pcm16[i];
  queue_features_info(&st, st.features);
  if (st.analysis_pos - FRAME_SIZE >= 0) st.analysis_pos -= FRAME_SIZE;
  else st.analysis_gap = 1;
  st.predict_pos = PLC_BUF_SIZE;
  memmove(st.pcm, &st.pcm[FRAME_SIZE], (PLC_BUF_SIZE - FRAME_SIZE) * sizeof(float));
  memcpy(&st.pcm[PLC_BUF_SIZE-FRAME_SIZE], pcmf, FRAME_SIZE * sizeof(float));
  st.blend = 1;
  fargan_cont_initialized = st.fargan.cont_initialized;

  if (!write_exact(OUTPUT_MAGIC, 4) ||
      !write_exact(&version, sizeof(version)) ||
      !write_exact(&got_fec, sizeof(got_fec)) ||
      !write_exact(&st.blend, sizeof(st.blend)) ||
      !write_exact(&st.loss_count, sizeof(st.loss_count)) ||
      !write_exact(&st.analysis_gap, sizeof(st.analysis_gap)) ||
      !write_exact(&st.analysis_pos, sizeof(st.analysis_pos)) ||
      !write_exact(&st.predict_pos, sizeof(st.predict_pos)) ||
      !write_exact(&st.fec_read_pos, sizeof(st.fec_read_pos)) ||
      !write_exact(&st.fec_skip, sizeof(st.fec_skip)) ||
      !write_exact(&fargan_cont_initialized, sizeof(fargan_cont_initialized)) ||
      !write_exact(&st.fargan.last_period, sizeof(st.fargan.last_period))) {
    fprintf(stderr, "failed to write conceal header\n");
    return 1;
  }
  if (!write_bits_array(pcmf, FRAME_SIZE) ||
      !write_bits_array(st.features, NB_TOTAL_FEATURES) ||
      !write_bits_array(st.cont_features, CONT_VECTORS * NB_FEATURES) ||
      !write_bits_array(st.pcm, PLC_BUF_SIZE) ||
      !write_bits_array(st.plc_net.gru1_state, PLC_GRU1_STATE_SIZE) ||
      !write_bits_array(st.plc_net.gru2_state, PLC_GRU2_STATE_SIZE) ||
      !write_bits_array(st.plc_bak[0].gru1_state, PLC_GRU1_STATE_SIZE) ||
      !write_bits_array(st.plc_bak[0].gru2_state, PLC_GRU2_STATE_SIZE) ||
      !write_bits_array(st.plc_bak[1].gru1_state, PLC_GRU1_STATE_SIZE) ||
      !write_bits_array(st.plc_bak[1].gru2_state, PLC_GRU2_STATE_SIZE) ||
      !write_bits_array(&st.fargan.deemph_mem, 1) ||
      !write_bits_array(st.fargan.pitch_buf, PITCH_MAX_PERIOD) ||
      !write_bits_array(st.fargan.cond_conv1_state, COND_NET_FCONV1_STATE_SIZE) ||
      !write_bits_array(st.fargan.fwc0_mem, SIG_NET_FWC0_STATE_SIZE) ||
      !write_bits_array(st.fargan.gru1_state, SIG_NET_GRU1_STATE_SIZE) ||
      !write_bits_array(st.fargan.gru2_state, SIG_NET_GRU2_STATE_SIZE) ||
      !write_bits_array(st.fargan.gru3_state, SIG_NET_GRU3_STATE_SIZE)) {
    fprintf(stderr, "failed to write conceal payload\n");
    return 1;
  }
  return 0;
}
