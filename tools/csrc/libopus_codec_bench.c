/* libopus_codec_bench.c — self-timing libopus encode/decode microbenchmark for
 * the gopus-vs-libopus performance scoreboard.
 *
 * Generalizes the 48 kHz-only libopus_encoder_bench.c / libopus_testvector_bench.c
 * harnesses to ANY sample rate and channel count so a single helper can time the
 * full config space (CELT/SILK/Hybrid x mono/stereo x 8/16/24/48 kHz x
 * 2.5/10/20/60 ms). The benchmarked work runs inside one process: process
 * startup, file I/O, decoder/encoder construction, and the priming decode are all
 * excluded from the timed loop, so the reported ns/op is a clean codec cost with
 * no subprocess-spawn pollution. The encoder/decoder is reset before every pass
 * and the whole frame batch is driven statefully (one stream), matching how the
 * gopus benchmark drives its native Encoder/Decoder. The reset runs outside the
 * timed region: only the per-frame opus_encode_float/opus_decode_float calls are
 * timed, exactly the work the gopus side times.
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
 *   --serve             (optional; interleaved mode, see below)
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
 * Serve mode (--serve): after construction and one priming pass the helper
 * prints "ready" and then reads commands from stdin, one per line:
 *   pass N   run one untimed warm pass, then N timed passes (reset untimed
 *            before each), and print "ns" followed by the N per-pass
 *            ns_per_packet values;
 *   quit     exit.
 * The Go interleaved scoreboard drives it so gopus and libopus passes alternate
 * in lockstep on the same machine state.
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
          "--min-ns N --count N [--serve] --in PATH\n",
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

/* Workload is one prepared encode or decode batch. Exactly one of enc/dec is
 * set. */
typedef struct {
  OpusEncoder *enc;
  OpusDecoder *dec;
  const EncodeConfig *cfg;
  const float *pcm_in;     /* encode input */
  unsigned char *packet;   /* encode output */
  Packet *packets;         /* decode input */
  float *pcm_out;          /* decode output */
  int packet_count;        /* frames per pass */
  int64_t samples_per_op;  /* per-channel samples per pass */
} Workload;

/* workload_reset resets the codec before a pass. OPUS_RESET_STATE leaves the
 * encoder's user-forced mode in place (it lives before
 * OPUS_ENCODER_RESET_START), so the forced mode is set once at construction. */
static int workload_reset(Workload *w) {
  int ret = w->enc != NULL ? opus_encoder_ctl(w->enc, OPUS_RESET_STATE)
                           : opus_decoder_ctl(w->dec, OPUS_RESET_STATE);
  if (ret != OPUS_OK) {
    fprintf(stderr, "OPUS_RESET_STATE failed: %d\n", ret);
    return -1;
  }
  return 0;
}

/* workload_pass drives one stateful encode or decode of the whole batch from
 * the current codec state. Returns total per-channel samples, or -1 on error. */
static int64_t workload_pass(Workload *w) {
  int64_t samples = 0;
  if (w->enc != NULL) {
    const EncodeConfig *cfg = w->cfg;
    int samples_per_frame = cfg->frame_size * cfg->channels;
    for (int i = 0; i < w->packet_count; i++) {
      int n = opus_encode_float(w->enc, w->pcm_in + (int64_t)i * samples_per_frame,
                                cfg->frame_size, w->packet, MAX_PACKET_BYTES);
      if (n < 0) {
        fprintf(stderr, "opus_encode_float frame %d failed: %d\n", i, n);
        return -1;
      }
      samples += cfg->frame_size;
    }
    return samples;
  }
  for (int i = 0; i < w->packet_count; i++) {
    Packet *p = &w->packets[i];
    int n = opus_decode_float(w->dec, p->data, p->len, w->pcm_out, MAX_FRAME_SAMPLES, 0);
    if (n < 0) {
      fprintf(stderr, "opus_decode_float packet %d failed: %d\n", i, n);
      return -1;
    }
    samples += n;
  }
  return samples;
}

/* timed_pass resets the codec (untimed) and returns the wall time of one pass,
 * or 0 on error. */
static uint64_t timed_pass(Workload *w) {
  if (workload_reset(w) < 0) return 0;
  uint64_t start = now_ns();
  int64_t got = workload_pass(w);
  uint64_t elapsed = now_ns() - start;
  if (got != w->samples_per_op) {
    fprintf(stderr, "pass samples mismatch: got %" PRId64 " want %" PRId64 "\n", got,
            w->samples_per_op);
    return 0;
  }
  return elapsed > 0 ? elapsed : 1;
}

/* run_timed runs count measurement windows of at least min_ns of timed pass
 * time each and prints the median window. */
static int run_timed(Workload *w, const char *mode, int rate, int channels, uint64_t min_ns,
                     int count) {
  BenchRun *runs = (BenchRun *)calloc((size_t)count, sizeof(BenchRun));
  if (runs == NULL) return 1;
  for (int r = 0; r < count; r++) {
    uint64_t elapsed = 0;
    uint64_t iterations = 0;
    do {
      uint64_t ns = timed_pass(w);
      if (ns == 0) {
        free(runs);
        return 1;
      }
      elapsed += ns;
      iterations++;
    } while (elapsed < min_ns);
    runs[r].elapsed_ns = elapsed;
    runs[r].iterations = iterations;
    runs[r].packets_per_op = w->packet_count;
    runs[r].samples_per_op = w->samples_per_op;
    double total_packets = (double)w->packet_count * (double)iterations;
    double total_samples = (double)w->samples_per_op * (double)iterations;
    runs[r].ns_per_packet = (double)elapsed / total_packets;
    runs[r].ns_per_sample = (double)elapsed / total_samples;
    runs[r].x_realtime = (total_samples / (double)rate) / ((double)elapsed / 1e9);
  }
  qsort(runs, (size_t)count, sizeof(BenchRun), compare_run);
  BenchRun m = runs[count / 2];
  printf("libopus\t%s\t%d\t%d\t%d\t%" PRIu64 "\t%" PRIu64 "\t%" PRId64 "\t%" PRId64
         "\t%.6f\t%.6f\t%.6f\n",
         mode, rate, channels, count, m.iterations, m.elapsed_ns, m.packets_per_op,
         m.samples_per_op, m.ns_per_packet, m.ns_per_sample, m.x_realtime);
  free(runs);
  return 0;
}

/* serve answers "pass N" commands on stdin until "quit" or EOF. */
static int serve(Workload *w) {
  char line[64];
  printf("ready\n");
  fflush(stdout);
  while (fgets(line, sizeof(line), stdin) != NULL) {
    int n = 0;
    if (strncmp(line, "quit", 4) == 0) return 0;
    if (sscanf(line, "pass %d", &n) != 1 || n < 1) {
      fprintf(stderr, "bad serve command: %s", line);
      return 1;
    }
    if (workload_reset(w) < 0 || workload_pass(w) != w->samples_per_op) return 1;
    printf("ns");
    for (int i = 0; i < n; i++) {
      uint64_t ns = timed_pass(w);
      if (ns == 0) return 1;
      printf(" %.3f", (double)ns / (double)w->packet_count);
    }
    printf("\n");
    fflush(stdout);
  }
  return 0;
}

/* run_workload primes the codec with one untimed pass and then either serves
 * interleaved commands or runs the self-timed measurement. */
static int run_workload(Workload *w, const char *mode, int rate, int channels, uint64_t min_ns,
                        int count, int serve_mode) {
  if (workload_reset(w) < 0) return 1;
  w->samples_per_op = workload_pass(w);
  if (w->samples_per_op <= 0) return 1;
  if (serve_mode) return serve(w);
  printf("implementation\tmode\trate\tchannels\tcount\titerations\telapsed_ns\tpackets_per_op\t"
         "samples_per_op\tns_per_packet\tns_per_sample\tx_realtime\n");
  return run_timed(w, mode, rate, channels, min_ns, count);
}

static int run_encode(const EncodeConfig *cfg, const char *in_path, uint64_t min_ns, int count,
                      int serve_mode) {
  int64_t size = 0;
  unsigned char *raw = read_file(in_path, &size);
  if (raw == NULL) return 1;
  if (size <= 0 || size % 4 != 0) {
    fprintf(stderr, "%s: PCM size not a float32 multiple\n", in_path);
    free(raw);
    return 1;
  }
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
  if (cfg->force_mode != 0) {
    opus_encoder_ctl(enc, OPUS_SET_FORCE_MODE(cfg->force_mode));
  }

  unsigned char *packet = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (packet == NULL) {
    opus_encoder_destroy(enc);
    free(raw);
    return 1;
  }

  Workload w;
  memset(&w, 0, sizeof(w));
  w.enc = enc;
  w.cfg = cfg;
  w.pcm_in = (const float *)raw;
  w.packet = packet;
  w.packet_count = frame_count;
  int ret = run_workload(&w, "encode", cfg->rate, cfg->channels, min_ns, count, serve_mode);

  free(packet);
  opus_encoder_destroy(enc);
  free(raw);
  return ret;
}

static int run_decode(int rate, int channels, const char *in_path, uint64_t min_ns, int count,
                      int serve_mode) {
  int64_t size = 0;
  unsigned char *raw = read_file(in_path, &size);
  if (raw == NULL) return 1;

  Packet *packets = NULL;
  int packet_count = 0;
  int packet_cap = 0;
  int64_t offset = 0;
  int ret = 1;
  while (offset < size) {
    if (offset + 8 > size) {
      fprintf(stderr, "%s: truncated packet header\n", in_path);
      goto done;
    }
    uint32_t plen = read_be32(raw + offset);
    offset += 8; /* length + final range */
    if ((int64_t)plen > size - offset) {
      fprintf(stderr, "%s: truncated packet payload\n", in_path);
      goto done;
    }
    if (packet_count == packet_cap) {
      int next = packet_cap == 0 ? 1024 : packet_cap * 2;
      Packet *grown = (Packet *)realloc(packets, (size_t)next * sizeof(Packet));
      if (grown == NULL) {
        fprintf(stderr, "packet table realloc failed\n");
        goto done;
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
    goto done;
  }

  int err = OPUS_OK;
  OpusDecoder *dec = opus_decoder_create(rate, channels, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    goto done;
  }
  float *pcm = (float *)malloc((size_t)MAX_FRAME_SAMPLES * channels * sizeof(float));
  if (pcm == NULL) {
    opus_decoder_destroy(dec);
    goto done;
  }

  Workload w;
  memset(&w, 0, sizeof(w));
  w.dec = dec;
  w.packets = packets;
  w.pcm_out = pcm;
  w.packet_count = packet_count;
  ret = run_workload(&w, "decode", rate, channels, min_ns, count, serve_mode);

  free(pcm);
  opus_decoder_destroy(dec);

done:
  free(packets);
  free(raw);
  return ret;
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
  int serve_mode = 0;

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
    } else if (strcmp(a, "--serve") == 0) {
      serve_mode = 1;
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

  if (strcmp(mode, "encode") == 0) {
    if (cfg.frame_size <= 0 || cfg.bitrate <= 0) {
      usage(argv[0]);
      return 2;
    }
    return run_encode(&cfg, in_path, min_ns, count, serve_mode);
  }
  if (strcmp(mode, "decode") == 0) {
    return run_decode(cfg.rate, cfg.channels, in_path, min_ns, count, serve_mode);
  }
  usage(argv[0]);
  return 2;
}
