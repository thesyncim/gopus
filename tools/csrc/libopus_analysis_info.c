/* libopus_analysis_info.c - live oracle for the Opus tonality analysis.
 *
 * Runs libopus run_analysis() (src/analysis.c) over an interleaved PCM stream
 * exactly as opus_encode_native() calls it: one call per encoded frame, with
 * the frame's PCM as analysis_pcm, analysis_frame_size == frame_size, and the
 * caller's c1/c2/channel layout and downmix function. After every call it
 * reports the returned AnalysisInfo, the analyzer's scalar state, the newest
 * per-chunk AnalysisInfo, and bit hashes of the bulk state arrays, so a Go port
 * can be compared field by field and bisected.
 *
 * Wire format (little-endian):
 *   IN:  "GANI" u32(version=1)
 *              u32(sample_rate) u32(channels) u32(frame_size) u32(num_frames)
 *              u32(lsb_depth) i32(c1) i32(c2) u32(downmix: 0=float, 1=int16, 2=int24)
 *              u32(nsamples) then nsamples samples: float32 (downmix 0),
 *              int16 (downmix 1, padded to 4 bytes) or int32 (downmix 2).
 *   OUT: "GANO" u32(version=1) u32(num_frames)
 *              num_frames x record:
 *                info(ret)      : 12 x u32 + 20 bytes leak_boost (see put_info)
 *                info(latest)   : same layout, tonal->info[write_pos-1]
 *                scalars        : u32 x 12 (see below)
 *                hashes         : u32 x 12 (see below)
 *
 * Reference: libopus src/analysis.c run_analysis(), src/opus_encoder.c
 * opus_encode_native(), downmix_float()/downmix_int()/downmix_int24().
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
#include "opus_private.h"
#include "analysis.h"
#include "modes.h"

#define INPUT_MAGIC  "GANI"
#define OUTPUT_MAGIC "GANO"

static int read_exact(void *dst, size_t n) { return fread(dst, 1, n, stdin) == n; }
static int write_exact(const void *src, size_t n) { return fwrite(src, 1, n, stdout) == n; }
static int put_u32(uint32_t v) { return write_exact(&v, 4); }
static uint32_t fbits(float v) { uint32_t b; memcpy(&b, &v, 4); return b; }

static uint32_t hash_floats(const float *v, size_t n) {
  uint32_t h = 2166136261u;
  size_t i;
  for (i = 0; i < n; i++) { uint32_t b; memcpy(&b, &v[i], 4); h = (h ^ b) * 16777619u; }
  return h;
}

static int put_info(const AnalysisInfo *in) {
  unsigned char leak[20];
  memset(leak, 0, sizeof(leak));
  memcpy(leak, in->leak_boost, LEAK_BANDS);
  return put_u32((uint32_t)in->valid) && put_u32(fbits(in->tonality)) &&
         put_u32(fbits(in->tonality_slope)) && put_u32(fbits(in->noisiness)) &&
         put_u32(fbits(in->activity)) && put_u32(fbits(in->music_prob)) &&
         put_u32(fbits(in->music_prob_min)) && put_u32(fbits(in->music_prob_max)) &&
         put_u32((uint32_t)in->bandwidth) && put_u32(fbits(in->activity_probability)) &&
         put_u32(fbits(in->max_pitch_ratio)) && put_u32(0) && write_exact(leak, sizeof(leak));
}

int main(void) {
  char magic[4];
  uint32_t version, fs, channels, frame_size, num_frames, lsb_depth, downmix_kind, nsamples;
  int32_t c1, c2;
  void *pcm = NULL;
  size_t sample_bytes;
  const CELTMode *mode;
  TonalityAnalysisState *st;
  downmix_func downmix;
  uint32_t f;
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 2;
  if (!read_exact(&version, 4) || version != 1) return 2;
  if (!read_exact(&fs, 4) || !read_exact(&channels, 4) || !read_exact(&frame_size, 4) ||
      !read_exact(&num_frames, 4) || !read_exact(&lsb_depth, 4) || !read_exact(&c1, 4) ||
      !read_exact(&c2, 4) || !read_exact(&downmix_kind, 4) || !read_exact(&nsamples, 4))
    return 2;
  if (channels < 1 || channels > 255 || nsamples != frame_size * channels * num_frames) return 3;
  switch (downmix_kind) {
    case 0: sample_bytes = 4; downmix = downmix_float; break;
    case 1: sample_bytes = 2; downmix = downmix_int; break;
    case 2: sample_bytes = 4; downmix = downmix_int24; break;
    default: return 3;
  }
  pcm = malloc((size_t)nsamples * sample_bytes + 4);
  if (pcm == NULL) return 4;
  if (!read_exact(pcm, (size_t)nsamples * sample_bytes)) return 2;
  if (downmix_kind == 1 && (nsamples & 1)) { char pad[2]; if (!read_exact(pad, 2)) return 2; }

  mode = opus_custom_mode_create(48000, 960, NULL);
  st = (TonalityAnalysisState *)calloc(1, sizeof(*st));
  if (mode == NULL || st == NULL) return 4;
  tonality_analysis_init(st, (opus_int32)fs);

  if (!write_exact(OUTPUT_MAGIC, 4) || !put_u32(1) || !put_u32(num_frames)) return 5;
  for (f = 0; f < num_frames; f++) {
    AnalysisInfo info;
    int latest;
    const unsigned char *frame = (const unsigned char *)pcm + (size_t)f * frame_size * channels * sample_bytes;
    memset(&info, 0, sizeof(info));
    run_analysis(st, mode, frame, (int)frame_size, (int)frame_size, c1, c2, (int)channels,
                 (opus_int32)fs, (int)lsb_depth, downmix, &info);
    latest = st->write_pos - 1;
    if (latest < 0) latest += DETECT_SIZE;
    if (!put_info(&info) || !put_info(&st->info[latest])) return 5;
    /* scalars */
    if (!put_u32(fbits(st->Etracker)) || !put_u32(fbits(st->lowECount)) ||
        !put_u32(fbits(st->hp_ener_accum)) || !put_u32(fbits(st->prev_tonality)) ||
        !put_u32((uint32_t)st->E_count) || !put_u32((uint32_t)st->count) ||
        !put_u32((uint32_t)st->analysis_offset) || !put_u32((uint32_t)st->write_pos) ||
        !put_u32((uint32_t)st->read_pos) || !put_u32((uint32_t)st->read_subframe) ||
        !put_u32((uint32_t)st->mem_fill) || !put_u32((uint32_t)st->prev_bandwidth))
      return 5;
    /* hashes */
    if (!put_u32(hash_floats(st->angle, 240)) || !put_u32(hash_floats(st->d_angle, 240)) ||
        !put_u32(hash_floats(st->d2_angle, 240)) || !put_u32(hash_floats(&st->E[0][0], NB_FRAMES * NB_TBANDS)) ||
        !put_u32(hash_floats(&st->logE[0][0], NB_FRAMES * NB_TBANDS)) || !put_u32(hash_floats(st->lowE, NB_TBANDS)) ||
        !put_u32(hash_floats(st->highE, NB_TBANDS)) || !put_u32(hash_floats(st->meanE, NB_TBANDS + 1)) ||
        !put_u32(hash_floats(st->mem, 32)) || !put_u32(hash_floats(st->cmean, 8)) ||
        !put_u32(hash_floats(st->std, 9)) || !put_u32(hash_floats(st->rnn_state, MAX_NEURONS)))
      return 5;
  }
  fflush(stdout);
  free(pcm);
  free(st);
  return 0;
}
