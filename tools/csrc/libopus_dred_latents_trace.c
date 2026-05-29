/*
 * Diagnostic helper: run libopus dred_compute_latents directly with the same
 * voiced-music probe input the gopus tests use, and dump the latents buffer
 * contents after each frame. This bypasses the full opus_encode_float path
 * so we can isolate stereo-vs-mono behavior of the DRED encoder itself.
 *
 * It additionally replays libopus's dred_compute_latents() inner 16 kHz
 * conversion loop -- using the *real* static dred_convert_to_16k() -- and dumps
 * the exact ordered sequence of 16 kHz mono samples that feed the RDOVAE
 * encoder. That lets the Go side assert byte parity of the stereo downmix +
 * channel-blind `pcm += process_size` advance against the libopus oracle.
 *
 * Args: <channels> [<frame_size>] [<chunk_size>].
 * Defaults: channels=1, frame_size=1920, chunk_size=frame_size.
 */
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"
#include "dred_encoder.h"
#include "dred_encoder.c"
#include "dred_rdovae_constants.h"
#include "cpu_support.h"

#define OUTPUT_MAGIC "GDLT"
#define CONVERT_MAGIC "GDLC"

static float voiced_sample(int frame_idx, int sample_idx, int frame_size, int sample_rate) {
  int n = frame_idx * frame_size + sample_idx;
  double t = (double)n / (double)sample_rate;
  double env = 0.82 + 0.18 * sin(2.0 * 3.14159265358979323846 * 1.3 * t);
  double s = 0.0;
  s += 0.28 * sin(2.0 * 3.14159265358979323846 * 110.0 * t);
  s += 0.17 * sin(2.0 * 3.14159265358979323846 * 220.0 * t + 0.11);
  s += 0.09 * sin(2.0 * 3.14159265358979323846 * 330.0 * t + 0.23);
  s += 0.05 * sin(2.0 * 3.14159265358979323846 * 440.0 * t + 0.37);
  return (float)(env * s);
}

/* Replay the exact dred_compute_latents() 16 kHz conversion loop using the real
 * static dred_convert_to_16k(). Mirrors dred_encoder.c lines 219-242 verbatim,
 * including the channel-blind `pcm += process_size` advance, and records every
 * 16 kHz mono sample produced (in order) into `out16k`. Uses a private
 * DREDEnc so it does not perturb the latents-trace encoder state. */
static int replay_convert_sequence(opus_int32 Fs, int channels, const float *pcm,
                                   int frame_size, float *out16k, int max16k) {
  DREDEnc tmp;
  int frame_size16k = frame_size * 16000 / (int)Fs;
  int produced = 0;
  memset(&tmp, 0, sizeof(tmp));
  tmp.Fs = Fs;
  tmp.channels = channels;
  while (frame_size16k > 0) {
    int process_size16k = IMIN(2 * DRED_FRAME_SIZE, frame_size16k);
    int process_size = process_size16k * (int)Fs / 16000;
    if (produced + process_size16k > max16k) {
      return -1;
    }
    dred_convert_to_16k(&tmp, pcm, process_size, &out16k[produced], process_size16k);
    produced += process_size16k;
    pcm += process_size;
    frame_size16k -= process_size16k;
  }
  return produced;
}

int main(int argc, char **argv) {
  const int sample_rate = 48000;
  int frame_size = 1920;
  const int total_buffer = sample_rate / 250;
  const int frames_to_run = 4;
  int channels = 1;
  int chunk_size = 0;
  int frame_idx;
  DREDEnc enc;
  float pcm[2880 * 2];
  float convert16k[2880];
  int arch;

  if (argc >= 2) {
    channels = atoi(argv[1]);
    if (channels != 1 && channels != 2) {
      fprintf(stderr, "channels must be 1 or 2\n");
      return 1;
    }
  }
  if (argc >= 3) {
    frame_size = atoi(argv[2]);
  }
  if (argc >= 4) {
    chunk_size = atoi(argv[3]);
  }
  if (chunk_size <= 0) chunk_size = frame_size;
  if (frame_size <= 0 || chunk_size <= 0 || frame_size % chunk_size != 0) {
    fprintf(stderr, "frame_size must be a positive multiple of chunk_size\n");
    return 1;
  }

  arch = opus_select_arch();

  memset(&enc, 0, sizeof(enc));
  dred_encoder_init(&enc, sample_rate, channels);
  if (!enc.loaded) {
    fprintf(stderr, "dred encoder not loaded\n");
    return 1;
  }

  for (frame_idx = 0; frame_idx < frames_to_run; frame_idx++) {
    int i;
    for (i = 0; i < frame_size; i++) {
      float sample = voiced_sample(frame_idx, i, frame_size, sample_rate);
      int ch;
      for (ch = 0; ch < channels; ch++) {
        pcm[i * channels + ch] = sample;
      }
    }
    for (i = 0; i < frame_size; i += chunk_size) {
      dred_compute_latents(&enc, &pcm[i * channels], chunk_size, total_buffer, arch);
    }

    {
      int pos;
      int dump = enc.latents_buffer_fill;
      uint32_t fidx, fill, doff, loff;
      fwrite(OUTPUT_MAGIC, 1, 4, stdout);
      fidx = (uint32_t)frame_idx;
      fill = (uint32_t)enc.latents_buffer_fill;
      doff = (uint32_t)enc.dred_offset;
      loff = (uint32_t)enc.latent_offset;
      fwrite(&fidx, 4, 1, stdout);
      fwrite(&fill, 4, 1, stdout);
      fwrite(&doff, 4, 1, stdout);
      fwrite(&loff, 4, 1, stdout);
      if (dump > 4) dump = 4;
      {
        uint32_t pcount = (uint32_t)dump;
        fwrite(&pcount, 4, 1, stdout);
      }
      for (pos = 0; pos < dump; pos++) {
        int j;
        for (j = 0; j < DRED_LATENT_DIM; j++) {
          float v = enc.latents_buffer[pos*DRED_LATENT_DIM + j];
          fwrite(&v, 4, 1, stdout);
        }
      }
    }
  }

  /* Emit the libopus 16 kHz conversion sequence for one fresh chunk-sized call,
   * isolating the stereo downmix + channel-blind pcm advance. */
  {
    int i;
    int produced;
    uint32_t count;
    for (i = 0; i < chunk_size; i++) {
      float sample = voiced_sample(0, i, chunk_size, sample_rate);
      int ch;
      for (ch = 0; ch < channels; ch++) {
        pcm[i * channels + ch] = sample;
      }
    }
    produced = replay_convert_sequence(sample_rate, channels, pcm, chunk_size,
                                       convert16k, (int)(sizeof(convert16k) / sizeof(convert16k[0])));
    if (produced < 0) {
      fprintf(stderr, "convert sequence overflow\n");
      return 1;
    }
    fwrite(CONVERT_MAGIC, 1, 4, stdout);
    count = (uint32_t)produced;
    fwrite(&count, 4, 1, stdout);
    for (i = 0; i < produced; i++) {
      fwrite(&convert16k[i], 4, 1, stdout);
    }
  }
  return 0;
}
