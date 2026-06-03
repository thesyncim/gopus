/* libopus_silk_ctrl_info.c
 *
 * Frame-level SILK encoder-control oracle.
 *
 * Encodes a PCM stream with libopus 1.6.1 (VBR or CVBR) and, for every internal
 * SILK frame, dumps the fully-populated silk_encoder_control_FLP state that
 * drives NSQ + rate control, plus the chosen per-SILK-frame payload size.
 *
 * The dump is produced by linking tools/csrc/silk_encode_frame_FLP_dump.c (a
 * verbatim copy of silk/float/encode_frame_FLP.c with two callbacks) BEFORE
 * libopus.a, so this oracle's silk_encode_frame_FLP overrides the archived one
 * and all other libopus code is reused unchanged.
 *
 * Input wire format (little-endian), identical header to the VBR/CVBR oracle:
 *
 *   magic "GSCI" + u32(version=1) + u32(mode) + u32(application)
 *        + u32(sample_rate) + u32(channels) + u32(frame_size)
 *        + u32(bitrate) + u32(bandwidth) + u32(signal) + u32(n_frames)
 *        then n_frames * frame_size * channels float32 samples.
 *
 *   mode: 0 = VBR, 1 = CVBR. Other fields match libopus_vbr_cvbr_encode_info.c.
 *
 * Output wire format:
 *
 *   magic "GSCO" + u32(version=1) + u32(n_frames)
 *   then n_frames packet records: u32(packet_len) u32(final_range) bytes[len]
 *   then u32(n_ctrl)
 *   then n_ctrl control records, each:
 *     u32(opus_frame_index)   // which opus_encode call
 *     u32(channel)            // 0 = mid/mono, 1 = side
 *     u32(nb_subfr)
 *     i32(signalType)
 *     i32(quantOffsetType)
 *     i32(maxBits)
 *     i32(useCBR)
 *     i32(nBytes)             // chosen SILK-frame payload size (-1 if unknown)
 *     f32(predGain)
 *     f32(LTPredCodGain)
 *     f32(Lambda)
 *     f32(input_quality)
 *     f32(coding_quality)
 *     i32 GainsUnq_Q16[4]
 *     f32 Gains[4]            // post-quant gains (Q0)
 *     f32 AR[4*16]
 *     f32 LF_MA_shp[4]
 *     f32 LF_AR_shp[4]
 *     f32 Tilt[4]
 *     f32 HarmShapeGain[4]
 *     f32 LTPCoef[5*4]
 *     f32 LTP_scale
 *     i32 pitchL[4]
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float(); the dumped struct
 * is silk/float/structs_FLP.h silk_encoder_control_FLP, captured in
 * silk_encode_frame_FLP (silk/float/encode_frame_FLP.c) just after
 * silk_process_gains_FLP() and at the final payload-size computation.
 */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#include "opus.h"
#include "main_FLP.h"

#define INPUT_MAGIC  "GSCI"
#define OUTPUT_MAGIC "GSCO"
#define MAX_PACKET_BYTES 4000
#define MAX_CTRL_RECORDS 4096

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact(const void *src, size_t n) {
  const unsigned char *p = (const unsigned char *)src;
  size_t off = 0;
  while (off < n) {
    size_t w = fwrite(p + off, 1, n - off, stdout);
    if (w == 0) return 0;
    off += w;
  }
  return 1;
}

static int read_u32(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact(b, 4)) return 0;
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) |
         ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int write_u32(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xff);
  b[1] = (unsigned char)((v >> 8)  & 0xff);
  b[2] = (unsigned char)((v >> 16) & 0xff);
  b[3] = (unsigned char)((v >> 24) & 0xff);
  return write_exact(b, 4);
}

static int write_i32(int32_t v) { return write_u32((uint32_t)v); }
static int write_f32(float f) {
  uint32_t bits;
  memcpy(&bits, &f, 4);
  return write_u32(bits);
}

/* ---- control record collection ---- */

typedef struct {
  int32_t  opus_frame_index;
  int32_t  channel;
  int32_t  nb_subfr;
  int32_t  signalType;
  int32_t  quantOffsetType;
  int32_t  maxBits;
  int32_t  useCBR;
  int32_t  nBytes;
  float    predGain;
  float    LTPredCodGain;
  float    Lambda;
  float    input_quality;
  float    coding_quality;
  int32_t  SNR_dB_Q7;
  int32_t  input_quality_bands_Q15[4];
  int32_t  speech_activity_Q8;
  int32_t  GainsUnq_Q16[MAX_NB_SUBFR];
  float    Gains[MAX_NB_SUBFR];
  float    AR[MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER];
  float    LF_MA_shp[MAX_NB_SUBFR];
  float    LF_AR_shp[MAX_NB_SUBFR];
  float    Tilt[MAX_NB_SUBFR];
  float    HarmShapeGain[MAX_NB_SUBFR];
  float    LTPCoef[LTP_ORDER * MAX_NB_SUBFR];
  float    LTP_scale;
  int32_t  pitchL[MAX_NB_SUBFR];
} ctrl_record;

static ctrl_record g_ctrl[MAX_CTRL_RECORDS];
static int         g_ctrl_count = 0;
static int32_t     g_cur_opus_frame = 0;
/* The two state_Fxx encoder pointers, used to recover the channel index. */
static const void *g_state_ptr[2] = { NULL, NULL };

void gopus_silk_ctrl_dump(
    const silk_encoder_state_FLP   *psEnc,
    const silk_encoder_control_FLP *psEncCtrl,
    opus_int                        maxBits,
    opus_int                        useCBR )
{
  ctrl_record *r;
  int i, ch = 0;
  if (g_ctrl_count >= MAX_CTRL_RECORDS) return;
  r = &g_ctrl[g_ctrl_count++];
  memset(r, 0, sizeof(*r));

  /* Lazily register the (up to two) distinct SILK encoder-state pointers so we
   * can label channels: state_Fxx[0] = mid/mono, state_Fxx[1] = side. */
  if (g_state_ptr[0] == NULL) g_state_ptr[0] = (const void *)psEnc;
  if ((const void *)psEnc != g_state_ptr[0] && g_state_ptr[1] == NULL)
    g_state_ptr[1] = (const void *)psEnc;
  if ((const void *)psEnc == g_state_ptr[1]) ch = 1;
  else ch = 0;

  r->opus_frame_index = g_cur_opus_frame;
  r->channel          = ch;
  r->nb_subfr         = psEnc->sCmn.nb_subfr;
  r->signalType       = psEnc->sCmn.indices.signalType;
  r->quantOffsetType  = psEnc->sCmn.indices.quantOffsetType;
  r->maxBits          = maxBits;
  r->useCBR           = useCBR;
  r->nBytes           = -1;
  r->predGain         = psEncCtrl->predGain;
  r->LTPredCodGain    = psEncCtrl->LTPredCodGain;
  r->Lambda           = psEncCtrl->Lambda;
  r->input_quality    = psEncCtrl->input_quality;
  r->coding_quality   = psEncCtrl->coding_quality;
  r->SNR_dB_Q7        = psEnc->sCmn.SNR_dB_Q7;
  for (i = 0; i < 4; i++) r->input_quality_bands_Q15[i] = psEnc->sCmn.input_quality_bands_Q15[i];
  r->speech_activity_Q8 = psEnc->sCmn.speech_activity_Q8;
  for (i = 0; i < MAX_NB_SUBFR; i++) {
    r->GainsUnq_Q16[i] = psEncCtrl->GainsUnq_Q16[i];
    r->Gains[i]        = psEncCtrl->Gains[i];
    r->LF_MA_shp[i]    = psEncCtrl->LF_MA_shp[i];
    r->LF_AR_shp[i]    = psEncCtrl->LF_AR_shp[i];
    r->Tilt[i]         = psEncCtrl->Tilt[i];
    r->HarmShapeGain[i]= psEncCtrl->HarmShapeGain[i];
    r->pitchL[i]       = psEncCtrl->pitchL[i];
  }
  for (i = 0; i < MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER; i++)
    r->AR[i] = psEncCtrl->AR[i];
  for (i = 0; i < LTP_ORDER * MAX_NB_SUBFR; i++)
    r->LTPCoef[i] = psEncCtrl->LTPCoef[i];
  r->LTP_scale = psEncCtrl->LTP_scale;
}

void gopus_silk_nbytes_dump(
    const silk_encoder_state_FLP   *psEnc,
    opus_int32                      nBytesOut )
{
  /* Attach to the most recent ctrl record for the matching channel. */
  int ch = 0, i;
  if ((const void *)psEnc == g_state_ptr[1]) ch = 1;
  for (i = g_ctrl_count - 1; i >= 0; i--) {
    if (g_ctrl[i].opus_frame_index == g_cur_opus_frame &&
        g_ctrl[i].channel == ch && g_ctrl[i].nBytes < 0) {
      g_ctrl[i].nBytes = nBytesOut;
      return;
    }
  }
}

int main(void) {
  char magic[4];
  uint32_t version, mode, application, sample_rate, channels, frame_size;
  uint32_t bitrate, bandwidth, signal, n_frames, i;
  int err;
  OpusEncoder *enc = NULL;
  float     *pcm    = NULL;
  unsigned char *packet = NULL;
  uint32_t final_range = 0;

  if (!set_binary_stdio()) { fprintf(stderr, "set binary stdio failed\n"); return 1; }

  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) {
    fprintf(stderr, "bad input magic\n"); return 1;
  }
  if (!read_u32(&version) || version != 1) { fprintf(stderr, "unsupported version\n"); return 1; }
  if (!read_u32(&mode)        || mode > 1     ||
      !read_u32(&application) ||
      !read_u32(&sample_rate) || sample_rate == 0 ||
      !read_u32(&channels)    || channels < 1 || channels > 2 ||
      !read_u32(&frame_size)  || frame_size == 0 ||
      !read_u32(&bitrate)     ||
      !read_u32(&bandwidth)   ||
      !read_u32(&signal)      ||
      !read_u32(&n_frames)) {
    fprintf(stderr, "truncated header\n"); return 1;
  }

  pcm = (float *)malloc(sizeof(float) * frame_size * channels);
  packet = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (!pcm || !packet) { fprintf(stderr, "malloc failed\n"); free(pcm); free(packet); return 1; }

  enc = opus_encoder_create((opus_int32)sample_rate, (int)channels, (int)application, &err);
  if (!enc || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(pcm); free(packet); return 1;
  }

  if (opus_encoder_ctl(enc, OPUS_SET_VBR(1)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT((int)mode)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BITRATE((opus_int32)bitrate)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH((int)bandwidth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL((int)signal)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_DTX(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0)) != OPUS_OK) {
    fprintf(stderr, "opus_encoder_ctl setup failed\n");
    opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
  }
  if (channels == 2) {
    if (opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(2)) != OPUS_OK) {
      fprintf(stderr, "OPUS_SET_FORCE_CHANNELS failed\n");
      opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
    }
  }

  /* Recover the two SILK encoder-state pointers so the dump hook can identify
   * the channel. The OpusEncoder layout starts with the silk_encoder, whose
   * first field is the stereo state; state_Fxx[] follows the leading scalars.
   * Rather than depend on opaque offsets, derive the pointers lazily inside the
   * hook by remembering the first two distinct psEnc values seen. */

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(n_frames)) {
    fprintf(stderr, "write output header failed\n");
    opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
  }

  /* Collect packet records into memory so the ctrl block can follow. */
  {
    uint32_t *pkt_len = (uint32_t *)malloc(sizeof(uint32_t) * n_frames);
    uint32_t *pkt_fr  = (uint32_t *)malloc(sizeof(uint32_t) * n_frames);
    unsigned char **pkt_data = (unsigned char **)malloc(sizeof(unsigned char *) * n_frames);
    if (!pkt_len || !pkt_fr || !pkt_data) {
      fprintf(stderr, "malloc records failed\n");
      opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
    }

    for (i = 0; i < n_frames; i++) {
      uint32_t n_samples = frame_size * channels, j;
      int ret;
      for (j = 0; j < n_samples; j++) {
        uint32_t bits;
        if (!read_u32(&bits)) { fprintf(stderr, "truncated PCM\n"); return 1; }
        memcpy(&pcm[j], &bits, 4);
      }
      g_cur_opus_frame = (int32_t)i;
      ret = opus_encode_float(enc, pcm, (int)frame_size, packet, MAX_PACKET_BYTES);
      if (ret < 0) {
        fprintf(stderr, "opus_encode_float frame %u: %d (%s)\n", i, ret, opus_strerror(ret));
        opus_encoder_destroy(enc); free(pcm); free(packet); return 1;
      }
      opus_encoder_ctl(enc, OPUS_GET_FINAL_RANGE(&final_range));
      pkt_len[i]  = (uint32_t)ret;
      pkt_fr[i]   = final_range;
      pkt_data[i] = (unsigned char *)malloc((size_t)ret > 0 ? (size_t)ret : 1);
      memcpy(pkt_data[i], packet, (size_t)ret);
    }

    for (i = 0; i < n_frames; i++) {
      if (!write_u32(pkt_len[i]) || !write_u32(pkt_fr[i]) ||
          !write_exact(pkt_data[i], pkt_len[i])) {
        fprintf(stderr, "write packet record failed\n"); return 1;
      }
    }

    /* ctrl block */
    if (!write_u32((uint32_t)g_ctrl_count)) { fprintf(stderr, "write ctrl count failed\n"); return 1; }
    for (i = 0; i < (uint32_t)g_ctrl_count; i++) {
      ctrl_record *r = &g_ctrl[i];
      int k;
      if (!write_i32(r->opus_frame_index) || !write_i32(r->channel) ||
          !write_i32(r->nb_subfr) || !write_i32(r->signalType) ||
          !write_i32(r->quantOffsetType) || !write_i32(r->maxBits) ||
          !write_i32(r->useCBR) || !write_i32(r->nBytes) ||
          !write_f32(r->predGain) || !write_f32(r->LTPredCodGain) ||
          !write_f32(r->Lambda) || !write_f32(r->input_quality) ||
          !write_f32(r->coding_quality) || !write_i32(r->SNR_dB_Q7) ||
          !write_i32(r->input_quality_bands_Q15[0]) || !write_i32(r->input_quality_bands_Q15[1]) ||
          !write_i32(r->input_quality_bands_Q15[2]) || !write_i32(r->input_quality_bands_Q15[3]) ||
          !write_i32(r->speech_activity_Q8)) { fprintf(stderr, "write ctrl head failed\n"); return 1; }
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_i32(r->GainsUnq_Q16[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->Gains[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR * MAX_SHAPE_LPC_ORDER; k++) if (!write_f32(r->AR[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->LF_MA_shp[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->LF_AR_shp[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->Tilt[k])) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_f32(r->HarmShapeGain[k])) return 1;
      for (k = 0; k < LTP_ORDER * MAX_NB_SUBFR; k++) if (!write_f32(r->LTPCoef[k])) return 1;
      if (!write_f32(r->LTP_scale)) return 1;
      for (k = 0; k < MAX_NB_SUBFR; k++) if (!write_i32(r->pitchL[k])) return 1;
    }
  }

  opus_encoder_destroy(enc);
  free(pcm); free(packet);
  return 0;
}
