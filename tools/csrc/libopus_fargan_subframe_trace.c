/* Single-subframe FARGAN trace oracle. Re-implements run_fargan_subframe()
 * from dnn/fargan.c (libopus 1.6.1) verbatim, dumping every intermediate
 * layer output so a per-stage bit-exact comparison can localize FARGAN drift.
 * Inert additive oracle: it does not alter any shipped code path. */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <math.h>

#ifdef HAVE_CONFIG_H
#include "config.h"
#endif

#include "arch.h"
#include "os_support.h"
#include "nnet.h"
#include "fargan.h"

#undef HAVE_CONFIG_H
#ifdef USE_WEIGHTS_FILE
#undef USE_WEIGHTS_FILE
#endif
#include "fargan_data.c"

#define INPUT_MAGIC "GFTI"
#define OUTPUT_MAGIC "GFTO"

static int read_exact(void *dst, size_t size) { return fread(dst, 1, size, stdin) == size; }
static int write_exact(const void *src, size_t size) { return fwrite(src, 1, size, stdout) == size; }
static int read_bits(float *dst, int n){int i;for(i=0;i<n;i++){uint32_t b;if(!read_exact(&b,4))return 0;memcpy(&dst[i],&b,4);}return 1;}
static int write_bits(const float *src, int n){int i;for(i=0;i<n;i++){uint32_t b;memcpy(&b,&src[i],4);if(!write_exact(&b,4))return 0;}return 1;}

int main(void){
  char magic[4];
  uint32_t version;
  int32_t period;
  FARGANState st;
  float cond[FARGAN_COND_SIZE];
  float pcm[FARGAN_SUBFRAME_SIZE];

  /* mirror run_fargan_subframe locals */
  int i, pos;
  float fwc0_in[SIG_NET_INPUT_SIZE];
  float gru1_in[SIG_NET_FWC0_CONV_OUT_SIZE+2*FARGAN_SUBFRAME_SIZE];
  float gru2_in[SIG_NET_GRU1_OUT_SIZE+2*FARGAN_SUBFRAME_SIZE];
  float gru3_in[SIG_NET_GRU2_OUT_SIZE+2*FARGAN_SUBFRAME_SIZE];
  float pred[FARGAN_SUBFRAME_SIZE+4];
  float prev[FARGAN_SUBFRAME_SIZE];
  float pitch_gate[4];
  float skip_cat[SIG_NET_GRU1_OUT_SIZE+SIG_NET_GRU2_OUT_SIZE+SIG_NET_GRU3_OUT_SIZE+SIG_NET_FWC0_CONV_OUT_SIZE+2*FARGAN_SUBFRAME_SIZE];
  float skip_out[SIG_NET_SKIP_DENSE_OUT_SIZE];
  float fwc0_conv_out[SIG_NET_FWC0_CONV_OUT_SIZE];
  float gain, gain_1;
  FARGAN *model;

  if (!read_exact(magic,4) || memcmp(magic,INPUT_MAGIC,4)!=0){fprintf(stderr,"bad magic\n");return 1;}
  if (!read_exact(&version,4) || version!=1){fprintf(stderr,"bad version\n");return 1;}
  fargan_init(&st);
  if (!read_exact(&period,4) ||
      !read_bits(&st.deemph_mem,1) ||
      !read_bits(st.pitch_buf,PITCH_MAX_PERIOD) ||
      !read_bits(st.fwc0_mem,SIG_NET_FWC0_STATE_SIZE) ||
      !read_bits(st.gru1_state,SIG_NET_GRU1_STATE_SIZE) ||
      !read_bits(st.gru2_state,SIG_NET_GRU2_STATE_SIZE) ||
      !read_bits(st.gru3_state,SIG_NET_GRU3_STATE_SIZE) ||
      !read_bits(cond,FARGAN_COND_SIZE)){fprintf(stderr,"bad payload\n");return 1;}

  model = &st.model;

  compute_generic_dense(&model->sig_net_cond_gain_dense, &gain, cond, ACTIVATION_LINEAR, st.arch);
  gain = exp(gain);
  gain_1 = 1.f/(1e-5f + gain);
  pos = PITCH_MAX_PERIOD-period-2;
  for (i=0;i<FARGAN_SUBFRAME_SIZE+4;i++){pred[i]=MIN32(1.f,MAX32(-1.f,gain_1*st.pitch_buf[IMAX(0,pos)]));pos++;if(pos==PITCH_MAX_PERIOD)pos-=period;}
  for (i=0;i<FARGAN_SUBFRAME_SIZE;i++) prev[i]=MAX32(-1.f,MIN16(1.f,gain_1*st.pitch_buf[PITCH_MAX_PERIOD-FARGAN_SUBFRAME_SIZE+i]));

  OPUS_COPY(&fwc0_in[0],&cond[0],FARGAN_COND_SIZE);
  OPUS_COPY(&fwc0_in[FARGAN_COND_SIZE],pred,FARGAN_SUBFRAME_SIZE+4);
  OPUS_COPY(&fwc0_in[FARGAN_COND_SIZE+FARGAN_SUBFRAME_SIZE+4],prev,FARGAN_SUBFRAME_SIZE);

  compute_generic_conv1d(&model->sig_net_fwc0_conv, gru1_in, st.fwc0_mem, fwc0_in, SIG_NET_INPUT_SIZE, ACTIVATION_TANH, st.arch);
  OPUS_COPY(fwc0_conv_out, gru1_in, SIG_NET_FWC0_CONV_OUT_SIZE);
  compute_glu(&model->sig_net_fwc0_glu_gate, gru1_in, gru1_in, st.arch);
  compute_generic_dense(&model->sig_net_gain_dense_out, pitch_gate, gru1_in, ACTIVATION_SIGMOID, st.arch);

  for (i=0;i<FARGAN_SUBFRAME_SIZE;i++) gru1_in[SIG_NET_FWC0_GLU_GATE_OUT_SIZE+i]=pitch_gate[0]*pred[i+2];
  OPUS_COPY(&gru1_in[SIG_NET_FWC0_GLU_GATE_OUT_SIZE+FARGAN_SUBFRAME_SIZE],prev,FARGAN_SUBFRAME_SIZE);
  compute_generic_gru(&model->sig_net_gru1_input,&model->sig_net_gru1_recurrent,st.gru1_state,gru1_in,st.arch);
  compute_glu(&model->sig_net_gru1_glu_gate,gru2_in,st.gru1_state,st.arch);

  for (i=0;i<FARGAN_SUBFRAME_SIZE;i++) gru2_in[SIG_NET_GRU1_OUT_SIZE+i]=pitch_gate[1]*pred[i+2];
  OPUS_COPY(&gru2_in[SIG_NET_GRU1_OUT_SIZE+FARGAN_SUBFRAME_SIZE],prev,FARGAN_SUBFRAME_SIZE);
  compute_generic_gru(&model->sig_net_gru2_input,&model->sig_net_gru2_recurrent,st.gru2_state,gru2_in,st.arch);
  compute_glu(&model->sig_net_gru2_glu_gate,gru3_in,st.gru2_state,st.arch);

  for (i=0;i<FARGAN_SUBFRAME_SIZE;i++) gru3_in[SIG_NET_GRU2_OUT_SIZE+i]=pitch_gate[2]*pred[i+2];
  OPUS_COPY(&gru3_in[SIG_NET_GRU2_OUT_SIZE+FARGAN_SUBFRAME_SIZE],prev,FARGAN_SUBFRAME_SIZE);
  compute_generic_gru(&model->sig_net_gru3_input,&model->sig_net_gru3_recurrent,st.gru3_state,gru3_in,st.arch);
  compute_glu(&model->sig_net_gru3_glu_gate,&skip_cat[SIG_NET_GRU1_OUT_SIZE+SIG_NET_GRU2_OUT_SIZE],st.gru3_state,st.arch);

  OPUS_COPY(skip_cat,gru2_in,SIG_NET_GRU1_OUT_SIZE);
  OPUS_COPY(&skip_cat[SIG_NET_GRU1_OUT_SIZE],gru3_in,SIG_NET_GRU2_OUT_SIZE);
  OPUS_COPY(&skip_cat[SIG_NET_GRU1_OUT_SIZE+SIG_NET_GRU2_OUT_SIZE+SIG_NET_GRU3_OUT_SIZE],gru1_in,SIG_NET_FWC0_CONV_OUT_SIZE);
  for (i=0;i<FARGAN_SUBFRAME_SIZE;i++) skip_cat[SIG_NET_GRU1_OUT_SIZE+SIG_NET_GRU2_OUT_SIZE+SIG_NET_GRU3_OUT_SIZE+SIG_NET_FWC0_CONV_OUT_SIZE+i]=pitch_gate[3]*pred[i+2];
  OPUS_COPY(&skip_cat[SIG_NET_GRU1_OUT_SIZE+SIG_NET_GRU2_OUT_SIZE+SIG_NET_GRU3_OUT_SIZE+SIG_NET_FWC0_CONV_OUT_SIZE+FARGAN_SUBFRAME_SIZE],prev,FARGAN_SUBFRAME_SIZE);

  compute_generic_dense(&model->sig_net_skip_dense,skip_out,skip_cat,ACTIVATION_TANH,st.arch);
  compute_glu(&model->sig_net_skip_glu_gate,skip_out,skip_out,st.arch);
  compute_generic_dense(&model->sig_net_sig_dense_out,pcm,skip_out,ACTIVATION_TANH,st.arch);
  for (i=0;i<FARGAN_SUBFRAME_SIZE;i++) pcm[i]*=gain;

  if (!write_exact(OUTPUT_MAGIC,4) || !write_exact(&version,4)){fprintf(stderr,"hdr\n");return 1;}
  /* dump intermediates in fixed order */
  {
    float gainv[1]; gainv[0]=gain;
    if (!write_bits(gainv,1) ||
        !write_bits(pred,FARGAN_SUBFRAME_SIZE+4) ||
        !write_bits(prev,FARGAN_SUBFRAME_SIZE) ||
        !write_bits(fwc0_conv_out,SIG_NET_FWC0_CONV_OUT_SIZE) ||
        !write_bits(pitch_gate,4) ||
        !write_bits(st.gru1_state,SIG_NET_GRU1_STATE_SIZE) ||
        !write_bits(st.gru2_state,SIG_NET_GRU2_STATE_SIZE) ||
        !write_bits(st.gru3_state,SIG_NET_GRU3_STATE_SIZE) ||
        !write_bits(skip_out,SIG_NET_SKIP_DENSE_OUT_SIZE) ||
        !write_bits(pcm,FARGAN_SUBFRAME_SIZE)){fprintf(stderr,"out\n");return 1;}
  }
  return 0;
}
