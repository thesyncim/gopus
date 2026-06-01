/* libopus_codec_bench.c — self-timing libopus encode/decode microbenchmark for
 * the gopus-vs-libopus performance scoreboard.
 *
 * Generalizes the 48 kHz-only libopus_encoder_bench.c / libopus_testvector_bench.c
 * harnesses to ANY sample rate and channel count so a single helper can time the
 * full config space (CELT/SILK/Hybrid x mono/stereo x 8/16/24/48 kHz x
 * 2.5/10/20/60 ms). The benchmarked work runs inside one process: process
 * startup, file I/O, decoder/encoder construction, and the priming decode are all
 * excluded from the timed loop, so the reported ns/op is a clean codec cost with
 * no subprocess-spawn pollution. The encoder/decoder is reset once per timed pass
 * and the whole frame batch is driven statefully (one stream), matching how the
 * gopus benchmark drives its native Encoder/Decoder.
 *
 * Invocation (all flags required unless noted):
 *   --mode encode|decode
 *   --rate N            (8000|12000|16000|24000|48000)
 *   --channels 1|2
 *   --frame-size N      (per-channel samples at --rate; encode only)
 *   --bitrate N         (encode only)
 *   --application audio|voip|restricted-celt|restricted-silk (encode only)
 *   --bandwidth nb|mb|wb|swb|fb  (encode only; force the audio bandwidth)
 *   --force-mode auto|silk|hybrid|celt  (encode only; OPUS_SET_FORCE_MODE)
 *   --signal auto|voice|music    (encode only)
 *   --complexity N      (encode only; default 10)
 *   --vbr 0|1           (encode only; default 0 = CBR)
 *   --min-ns N          (minimum wall time per measured pass)
 *   --count N           (number of passes; the median by ns_per_sample is printed)
 *   --in PATH           (encode: raw interleaved float32 LE PCM;
 *                        decode: opus_demo .bit stream = BE u32 len + BE u32 range
 *                                + payload, repeated)
 *
 * Output (single TSV row, header first):
 *   implementation  mode  rate  channels  count  iterations  elapsed_ns
 *   packets_per_op  samples_per_op  ns_per_packet  ns_per_sample  x_realtime
 *
 * samples_per_op counts per-channel samples (so x_realtime = audio_seconds /
 * wall_seconds is channel-independent). ns_per_packet is the headline metric the
 * Go scoreboard pairs against gopus ns/op (gopus times one packet per b.N op).
 *
 * Reference: libopus src/opus_encoder.c opus_encode_float(),
 *            src/opus_decoder.c opus_decode_float().
 */

#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "opus.h"
#include "opus_private.h"

#define MAX_PACKET_BYTES 4000
#define MAX_FRAME_SAMPLES 5760 /* 120 ms at 48 kHz, per channel */

typedef struct {
  unsigned char *data;
  int len;
} Packet;

typedef struct {
  uint64_t elapsed_ns;
  uint64_t iterations;
  int64_t packets_per_op;   /* frames per pass */
  int64_t samples_per_op;   /* per-channel samples per pass */
  double ns_per_packet;
  double ns_per_sample;
  double x_realtime;
} BenchRun;

static void usage(const char *argv0) {
  fprintf(stderr,
          "usage: %s --mode encode|decode --rate N --channels N [encode opts] "
          "--min-ns N --count N --in PATH\n",
          argv0);
}

static uint64_t now_ns(void) {
  struct timespec ts;
  if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
    return 0;
  }
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

static uint32_t read_be32(const unsigned char *p) {
  return ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) | ((uint32_t)p[2] << 8) |
         (uint32_t)p[3];
}

static int parse_application(const char *s) {
  if (strcmp(s, "audio") == 0) return OPUS_APPLICATION_AUDIO;
  if (strcmp(s, "voip") == 0) return OPUS_APPLICATION_VOIP;
  if (strcmp(s, "restricted-celt") == 0) return OPUS_APPLICATION_RESTRICTED_CELT;
  if (strcmp(s, "restricted-silk") == 0) return OPUS_APPLICATION_RESTRICTED_SILK;
  return 0;
}

static int parse_bandwidth(const char *s) {
  if (strcmp(s, "nb") == 0) return OPUS_BANDWIDTH_NARROWBAND;
  if (strcmp(s, "mb") == 0) return OPUS_BANDWIDTH_MEDIUMBAND;
  if (strcmp(s, "wb") == 0) return OPUS_BANDWIDTH_WIDEBAND;
  if (strcmp(s, "swb") == 0) return OPUS_BANDWIDTH_SUPERWIDEBAND;
  if (strcmp(s, "fb") == 0) return OPUS_BANDWIDTH_FULLBAND;
  return 0;
}

static int parse_force_mode(const char *s) {
  if (strcmp(s, "auto") == 0) return 0;
  if (strcmp(s, "silk") == 0) return MODE_SILK_ONLY;
  if (strcmp(s, "hybrid") == 0) return MODE_HYBRID;
  if (strcmp(s, "celt") == 0) return MODE_CELT_ONLY;
  return -1;
}

static int parse_signal(const char *s) {
  if (strcmp(s, "auto") == 0) return OPUS_AUTO;
  if (strcmp(s, "voice") == 0) return OPUS_SIGNAL_VOICE;
  if (strcmp(s, "music") == 0) return OPUS_SIGNAL_MUSIC;
  return -2; /* sentinel: OPUS_AUTO is a valid negative value */
}

static int64_t file_size(FILE *f) {
  if (fseek(f, 0, SEEK_END) != 0) return -1;
  long end = ftell(f);
  if (end < 0 || fseek(f, 0, SEEK_SET) != 0) return -1;
  return (int64_t)end;
}

static unsigned char *read_file(const char *path, int64_t *out_size) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) {
    fprintf(stderr, "open %s: %s\n", path, strerror(errno));
    return NULL;
  }
  int64_t size = file_size(f);
  if (size < 0) {
    fprintf(stderr, "stat %s failed\n", path);
    fclose(f);
    return NULL;
  }
  unsigned char *buf = (unsigned char *)malloc((size_t)(size > 0 ? size : 1));
  if (buf == NULL) {
    fprintf(stderr, "malloc %s failed\n", path);
    fclose(f);
    return NULL;
  }
  if (size > 0 && fread(buf, 1, (size_t)size, f) != (size_t)size) {
    fprintf(stderr, "read %s failed\n", path);
    free(buf);
    fclose(f);
    return NULL;
  }
  fclose(f);
  *out_size = size;
  return buf;
}

static int compare_run(const void *a, const void *b) {
  const BenchRun *ra = (const BenchRun *)a;
  const BenchRun *rb = (const BenchRun *)b;
  if (ra->ns_per_sample < rb->ns_per_sample) return -1;
  if (ra->ns_per_sample > rb->ns_per_sample) return 1;
  return 0;
}

typedef struct {
  int rate;
  int channels;
  int frame_size;
  int bitrate;
  int application;
  int bandwidth;
  int force_mode;
  int signal;
  int complexity;
  int vbr;
} EncodeConfig;

/* encode_pass drives one stateful encode of the whole PCM batch. The encoder is
 * reset at the start so each pass is independent. Returns total per-channel
 * samples consumed, or -1 on error. */
static int64_t encode_pass(OpusEncoder *enc, const EncodeConfig *cfg, const float *pcm,
                           int frame_count, unsigned char *packet) {
  if (opus_encoder_ctl(enc, OPUS_RESET_STATE) != OPUS_OK) {
    fprintf(stderr, "OPUS_RESET_STATE failed\n");
    return -1;
  }
  int samples_per_frame = cfg->frame_size * cfg->channels;
  int64_t samples = 0;
  for (int i = 0; i < frame_count; i++) {
    if (cfg->force_mode != 0) {
      /* opus_encode clears OPUS_SET_FORCE_MODE each call; reassert it. */
      opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(cfg->force_mode));
    }
    int n = opus_encode_float(enc, pcm + (int64_t)i * samples_per_frame, cfg->frame_size,
                              packet, MAX_PACKET_BYTES);
    if (n < 0) {
      fprintf(stderr, "opus_encode_float frame %d failed: %d\n", i, n);
      return -1;
    }
    samples += cfg->frame_size;
  }
  return samples;
}

static int run_encode(const EncodeConfig *cfg, const char *in_path, uint64_t min_ns, int count) {
  int64_t size = 0;
  unsigned char *raw = read_file(in_path, &size);
  if (raw == NULL) return 1;
  if (size <= 0 || size % 4 != 0) {
    fprintf(stderr, "%s: PCM size not a float32 multiple\n", in_path);
    free(raw);
    return 1;
  }
  const float *pcm = (const float *)raw;
  int64_t sample_count = size / 4;
  int samples_per_frame = cfg->frame_size * cfg->channels;
  if (samples_per_frame <= 0 || sample_count % samples_per_frame != 0) {
    fprintf(stderr, "%s: PCM is not a whole number of frames\n", in_path);
    free(raw);
    return 1;
  }
  int frame_count = (int)(sample_count / samples_per_frame);
  if (frame_count <= 0) {
    fprintf(stderr, "%s: no frames\n", in_path);
    free(raw);
    return 1;
  }

  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(cfg->rate, cfg->channels, cfg->application, &err);
  if (enc == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_encoder_create failed: %d\n", err);
    free(raw);
    return 1;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(cfg->bitrate)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(cfg->vbr)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(cfg->complexity)) != OPUS_OK) {
    fprintf(stderr, "encoder ctl setup failed\n");
    opus_encoder_destroy(enc);
    free(raw);
    return 1;
  }
  if (cfg->bandwidth != 0) {
    opus_encoder_ctl(enc, OPUS_SET_MAX_BANDWIDTH(cfg->bandwidth));
    opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(cfg->bandwidth));
  }
  if (cfg->signal != OPUS_AUTO) {
    opus_encoder_ctl(enc, OPUS_SET_SIGNAL(cfg->signal));
  }

  unsigned char *packet = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (packet == NULL) {
    opus_encoder_destroy(enc);
    free(raw);
    return 1;
  }

  /* Prime once outside the timed loop so first-frame allocations / mode
   * hysteresis warmup are not charged to the measurement. */
  if (encode_pass(enc, cfg, pcm, frame_count, packet) < 0) {
    free(packet);
    opus_encoder_destroy(enc);
    free(raw);
    return 1;
  }

  BenchRun *runs = (BenchRun *)calloc((size_t)count, sizeof(BenchRun));
  if (runs == NULL) {
    free(packet);
    opus_encoder_destroy(enc);
    free(raw);
    return 1;
  }

  for (int r = 0; r < count; r++) {
    uint64_t start = now_ns();
    uint64_t elapsed = 0;
    uint64_t iterations = 0;
    do {
      if (encode_pass(enc, cfg, pcm, frame_count, packet) < 0) {
        free(runs);
        free(packet);
        opus_encoder_destroy(enc);
        free(raw);
        return 1;
      }
      iterations++;
      elapsed = now_ns() - start;
    } while (elapsed < min_ns);
    runs[r].elapsed_ns = elapsed;
    runs[r].iterations = iterations;
    runs[r].packets_per_op = frame_count;
    runs[r].samples_per_op = (int64_t)frame_count * cfg->frame_size;
    double total_packets = (double)frame_count * (double)iterations;
    double total_samples = (double)runs[r].samples_per_op * (double)iterations;
    runs[r].ns_per_packet = (double)elapsed / total_packets;
    runs[r].ns_per_sample = (double)elapsed / total_samples;
    runs[r].x_realtime = (total_samples / (double)cfg->rate) / ((double)elapsed / 1e9);
  }

  qsort(runs, (size_t)count, sizeof(BenchRun), compare_run);
  BenchRun m = runs[count / 2];
  printf("libopus\tencode\t%d\t%d\t%d\t%" PRIu64 "\t%" PRIu64 "\t%" PRId64 "\t%" PRId64
         "\t%.6f\t%.6f\t%.6f\n",
         cfg->rate, cfg->channels, count, m.iterations, m.elapsed_ns, m.packets_per_op,
         m.samples_per_op, m.ns_per_packet, m.ns_per_sample, m.x_realtime);

  free(runs);
  free(packet);
  opus_encoder_destroy(enc);
  free(raw);
  return 0;
}

/* decode_pass streams every packet through one decoder, reset at the start of the
 * pass. Returns total per-channel samples decoded, or -1 on error. */
static int64_t decode_pass(OpusDecoder *dec, int rate, Packet *packets, int packet_count,
                           float *pcm) {
  (void)rate;
  if (opus_decoder_ctl(dec, OPUS_RESET_STATE) != OPUS_OK) {
    fprintf(stderr, "decoder OPUS_RESET_STATE failed\n");
    return -1;
  }
  int64_t samples = 0;
  for (int i = 0; i < packet_count; i++) {
    Packet *p = &packets[i];
    int n = opus_decode_float(dec, p->data, p->len, pcm, MAX_FRAME_SAMPLES, 0);
    if (n < 0) {
      fprintf(stderr, "opus_decode_float packet %d failed: %d\n", i, n);
      return -1;
    }
    samples += n;
  }
  return samples;
}

static int run_decode(int rate, int channels, const char *in_path, uint64_t min_ns, int count) {
  int64_t size = 0;
  unsigned char *raw = read_file(in_path, &size);
  if (raw == NULL) return 1;

  Packet *packets = NULL;
  int packet_count = 0;
  int packet_cap = 0;
  int64_t offset = 0;
  while (offset < size) {
    if (offset + 8 > size) {
      fprintf(stderr, "%s: truncated packet header\n", in_path);
      goto fail;
    }
    uint32_t plen = read_be32(raw + offset);
    offset += 8; /* length + final range */
    if ((int64_t)plen > size - offset) {
      fprintf(stderr, "%s: truncated packet payload\n", in_path);
      goto fail;
    }
    if (packet_count == packet_cap) {
      int next = packet_cap == 0 ? 1024 : packet_cap * 2;
      Packet *grown = (Packet *)realloc(packets, (size_t)next * sizeof(Packet));
      if (grown == NULL) {
        fprintf(stderr, "packet table realloc failed\n");
        goto fail;
      }
      packets = grown;
      packet_cap = next;
    }
    Packet *p = &packets[packet_count++];
    p->len = (int)plen;
    p->data = plen > 0 ? raw + offset : NULL;
    offset += plen;
  }
  if (packet_count == 0) {
    fprintf(stderr, "%s: no packets\n", in_path);
    goto fail;
  }

  int err = OPUS_OK;
  OpusDecoder *dec = opus_decoder_create(rate, channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    goto fail;
  }
  float *pcm = (float *)malloc((size_t)MAX_FRAME_SAMPLES * channels * sizeof(float));
  if (pcm == NULL) {
    opus_decoder_destroy(dec);
    goto fail;
  }

  int64_t expected = decode_pass(dec, rate, packets, packet_count, pcm);
  if (expected <= 0) {
    free(pcm);
    opus_decoder_destroy(dec);
    goto fail;
  }

  BenchRun *runs = (BenchRun *)calloc((size_t)count, sizeof(BenchRun));
  if (runs == NULL) {
    free(pcm);
    opus_decoder_destroy(dec);
    goto fail;
  }

  for (int r = 0; r < count; r++) {
    uint64_t start = now_ns();
    uint64_t elapsed = 0;
    uint64_t iterations = 0;
    do {
      int64_t got = decode_pass(dec, rate, packets, packet_count, pcm);
      if (got != expected) {
        fprintf(stderr, "decode samples mismatch: got %" PRId64 " want %" PRId64 "\n", got,
                expected);
        free(runs);
        free(pcm);
        opus_decoder_destroy(dec);
        goto fail;
      }
      iterations++;
      elapsed = now_ns() - start;
    } while (elapsed < min_ns);
    runs[r].elapsed_ns = elapsed;
    runs[r].iterations = iterations;
    runs[r].packets_per_op = packet_count;
    runs[r].samples_per_op = expected;
    double total_packets = (double)packet_count * (double)iterations;
    double total_samples = (double)expected * (double)iterations;
    runs[r].ns_per_packet = (double)elapsed / total_packets;
    runs[r].ns_per_sample = (double)elapsed / total_samples;
    runs[r].x_realtime = (total_samples / (double)rate) / ((double)elapsed / 1e9);
  }

  qsort(runs, (size_t)count, sizeof(BenchRun), compare_run);
  BenchRun m = runs[count / 2];
  printf("libopus\tdecode\t%d\t%d\t%d\t%" PRIu64 "\t%" PRIu64 "\t%" PRId64 "\t%" PRId64
         "\t%.6f\t%.6f\t%.6f\n",
         rate, channels, count, m.iterations, m.elapsed_ns, m.packets_per_op, m.samples_per_op,
         m.ns_per_packet, m.ns_per_sample, m.x_realtime);

  free(runs);
  free(pcm);
  opus_decoder_destroy(dec);
  free(packets);
  free(raw);
  return 0;

fail:
  free(packets);
  free(raw);
  return 1;
}

int main(int argc, char **argv) {
  const char *mode = NULL;
  const char *in_path = NULL;
  EncodeConfig cfg;
  memset(&cfg, 0, sizeof(cfg));
  cfg.channels = 1;
  cfg.application = OPUS_APPLICATION_AUDIO;
  cfg.signal = OPUS_AUTO;
  cfg.complexity = 10;
  cfg.vbr = 0;
  uint64_t min_ns = 200000000ULL;
  int count = 5;

  for (int i = 1; i < argc; i++) {
    const char *a = argv[i];
    if (strcmp(a, "--mode") == 0 && i + 1 < argc) {
      mode = argv[++i];
    } else if (strcmp(a, "--rate") == 0 && i + 1 < argc) {
      cfg.rate = atoi(argv[++i]);
    } else if (strcmp(a, "--channels") == 0 && i + 1 < argc) {
      cfg.channels = atoi(argv[++i]);
    } else if (strcmp(a, "--frame-size") == 0 && i + 1 < argc) {
      cfg.frame_size = atoi(argv[++i]);
    } else if (strcmp(a, "--bitrate") == 0 && i + 1 < argc) {
      cfg.bitrate = atoi(argv[++i]);
    } else if (strcmp(a, "--application") == 0 && i + 1 < argc) {
      cfg.application = parse_application(argv[++i]);
    } else if (strcmp(a, "--bandwidth") == 0 && i + 1 < argc) {
      cfg.bandwidth = parse_bandwidth(argv[++i]);
    } else if (strcmp(a, "--force-mode") == 0 && i + 1 < argc) {
      cfg.force_mode = parse_force_mode(argv[++i]);
    } else if (strcmp(a, "--signal") == 0 && i + 1 < argc) {
      cfg.signal = parse_signal(argv[++i]);
    } else if (strcmp(a, "--complexity") == 0 && i + 1 < argc) {
      cfg.complexity = atoi(argv[++i]);
    } else if (strcmp(a, "--vbr") == 0 && i + 1 < argc) {
      cfg.vbr = atoi(argv[++i]);
    } else if (strcmp(a, "--min-ns") == 0 && i + 1 < argc) {
      min_ns = (uint64_t)strtoull(argv[++i], NULL, 10);
    } else if (strcmp(a, "--count") == 0 && i + 1 < argc) {
      count = atoi(argv[++i]);
    } else if (strcmp(a, "--in") == 0 && i + 1 < argc) {
      in_path = argv[++i];
    } else {
      usage(argv[0]);
      return 2;
    }
  }

  if (mode == NULL || in_path == NULL || cfg.rate == 0 || cfg.channels < 1 ||
      cfg.channels > 2 || count < 1 || min_ns == 0 || cfg.application == 0 ||
      cfg.force_mode < 0 || cfg.signal == -2) {
    usage(argv[0]);
    return 2;
  }

  printf("implementation\tmode\trate\tchannels\tcount\titerations\telapsed_ns\tpackets_per_op\t"
         "samples_per_op\tns_per_packet\tns_per_sample\tx_realtime\n");

  if (strcmp(mode, "encode") == 0) {
    if (cfg.frame_size <= 0 || cfg.bitrate <= 0) {
      usage(argv[0]);
      return 2;
    }
    return run_encode(&cfg, in_path, min_ns, count);
  }
  if (strcmp(mode, "decode") == 0) {
    return run_decode(cfg.rate, cfg.channels, in_path, min_ns, count);
  }
  usage(argv[0]);
  return 2;
}
