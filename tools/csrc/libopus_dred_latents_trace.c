/*
 * Diagnostic helper: run libopus dred_compute_latents directly with the same
 * voiced-music probe input the gopus tests use, and dump the latents buffer
 * contents after each frame. This bypasses the full opus_encode_float path
 * so we can isolate stereo-vs-mono behavior of the DRED encoder itself.
 *
 * Args: <channels> [<frame_size>]. Defaults: channels=1, frame_size=1920.
 */
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opus.h"
#include "dred_encoder.h"
#include "dred_rdovae_constants.h"
#include "cpu_support.h"

#define OUTPUT_MAGIC "GDLT"

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

int main(int argc, char **argv) {
  const int sample_rate = 48000;
  int frame_size = 1920;
  const int total_buffer = sample_rate / 250;
  const int frames_to_run = 4;
  int channels = 1;
  int frame_idx;
  DREDEnc enc;
  float pcm[2880 * 2];
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
    dred_compute_latents(&enc, pcm, frame_size, total_buffer, arch);

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
  return 0;
}
