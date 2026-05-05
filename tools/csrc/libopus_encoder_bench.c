#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "opus.h"

#define SAMPLE_RATE 48000
#define MAX_PACKET_BYTES 4000

typedef struct {
  char *name;
  int application;
  int bandwidth;
  int frame_size;
  int channels;
  int bitrate;
  int signal;
  float *pcm;
  int frame_count;
} Workload;

typedef struct {
  uint64_t elapsed_ns;
  uint64_t iterations;
  int64_t samples_per_op;
  int64_t packets_per_op;
  int64_t bytes_per_op;
  double ns_per_sample;
  double ns_per_packet;
  double x_realtime;
} BenchRun;

static void usage(const char *argv0) {
  fprintf(stderr,
          "usage: %s --min-ns N --count N --cases aggregate|per-case|all "
          "--case name:application:bandwidth:frame_size:channels:bitrate:signal:path ...\n",
          argv0);
}

static char *dup_string(const char *s) {
  size_t n = strlen(s) + 1;
  char *out = (char *)malloc(n);
  if (out != NULL) {
    memcpy(out, s, n);
  }
  return out;
}

static int64_t file_size(FILE *f) {
  long cur = ftell(f);
  if (cur < 0) {
    return -1;
  }
  if (fseek(f, 0, SEEK_END) != 0) {
    return -1;
  }
  long end = ftell(f);
  if (end < 0 || fseek(f, cur, SEEK_SET) != 0) {
    return -1;
  }
  return (int64_t)end;
}

static int parse_application(const char *s) {
  if (strcmp(s, "audio") == 0) {
    return OPUS_APPLICATION_AUDIO;
  }
  if (strcmp(s, "voip") == 0) {
    return OPUS_APPLICATION_VOIP;
  }
  if (strcmp(s, "restricted-silk") == 0) {
    return OPUS_APPLICATION_RESTRICTED_SILK;
  }
  if (strcmp(s, "restricted-celt") == 0) {
    return OPUS_APPLICATION_RESTRICTED_CELT;
  }
  return 0;
}

static int parse_bandwidth(const char *s) {
  if (strcmp(s, "nb") == 0) {
    return OPUS_BANDWIDTH_NARROWBAND;
  }
  if (strcmp(s, "mb") == 0) {
    return OPUS_BANDWIDTH_MEDIUMBAND;
  }
  if (strcmp(s, "wb") == 0) {
    return OPUS_BANDWIDTH_WIDEBAND;
  }
  if (strcmp(s, "swb") == 0) {
    return OPUS_BANDWIDTH_SUPERWIDEBAND;
  }
  if (strcmp(s, "fb") == 0) {
    return OPUS_BANDWIDTH_FULLBAND;
  }
  return 0;
}

static int parse_signal(const char *s) {
  if (strcmp(s, "auto") == 0) {
    return OPUS_AUTO;
  }
  if (strcmp(s, "voice") == 0) {
    return OPUS_SIGNAL_VOICE;
  }
  if (strcmp(s, "music") == 0) {
    return OPUS_SIGNAL_MUSIC;
  }
  return 0;
}

static int load_pcm(const char *path, Workload *w) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) {
    fprintf(stderr, "open %s: %s\n", path, strerror(errno));
    return 0;
  }
  int64_t size = file_size(f);
  if (size <= 0 || size % 4 != 0) {
    fprintf(stderr, "%s: invalid float32 PCM size\n", path);
    fclose(f);
    return 0;
  }
  int64_t sample_count = size / 4;
  int samples_per_frame = w->frame_size * w->channels;
  if (samples_per_frame <= 0 || sample_count % samples_per_frame != 0) {
    fprintf(stderr, "%s: PCM sample count is not a whole number of frames\n", path);
    fclose(f);
    return 0;
  }
  w->frame_count = (int)(sample_count / samples_per_frame);
  if (w->frame_count <= 0) {
    fprintf(stderr, "%s: no frames\n", path);
    fclose(f);
    return 0;
  }
  w->pcm = (float *)malloc((size_t)size);
  if (w->pcm == NULL) {
    fprintf(stderr, "%s: PCM malloc failed\n", path);
    fclose(f);
    return 0;
  }
  if (fread(w->pcm, 1, (size_t)size, f) != (size_t)size) {
    fprintf(stderr, "read %s failed\n", path);
    fclose(f);
    return 0;
  }
  fclose(f);
  return 1;
}

static int parse_int_field(const char *raw, const char *name, int *out) {
  char *end = NULL;
  long v = strtol(raw, &end, 10);
  if (end == raw || *end != '\0' || v < 0 || v > INT32_MAX) {
    fprintf(stderr, "invalid %s: %s\n", name, raw);
    return 0;
  }
  *out = (int)v;
  return 1;
}

static int parse_case_spec(const char *spec, Workload *w) {
  char *copy = dup_string(spec);
  if (copy == NULL) {
    return 0;
  }
  char *fields[8];
  char *save = NULL;
  char *tok = strtok_r(copy, ":", &save);
  int n = 0;
  while (tok != NULL && n < 8) {
    fields[n++] = tok;
    tok = strtok_r(NULL, ":", &save);
  }
  if (n != 8 || tok != NULL) {
    fprintf(stderr, "invalid --case spec: %s\n", spec);
    free(copy);
    return 0;
  }

  w->name = dup_string(fields[0]);
  w->application = parse_application(fields[1]);
  w->bandwidth = parse_bandwidth(fields[2]);
  w->signal = parse_signal(fields[6]);
  if (w->name == NULL || w->application == 0 || w->bandwidth == 0 || w->signal == 0 ||
      !parse_int_field(fields[3], "frame_size", &w->frame_size) ||
      !parse_int_field(fields[4], "channels", &w->channels) ||
      !parse_int_field(fields[5], "bitrate", &w->bitrate) ||
      !load_pcm(fields[7], w)) {
    free(copy);
    return 0;
  }
  free(copy);
  return 1;
}

static void free_workload(Workload *w) {
  if (w == NULL) {
    return;
  }
  free(w->name);
  free(w->pcm);
}

static uint64_t now_ns(void) {
  struct timespec ts;
  if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
    return 0;
  }
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

static OpusEncoder *create_encoder(const Workload *w) {
  int err = OPUS_OK;
  OpusEncoder *enc = opus_encoder_create(SAMPLE_RATE, w->channels, w->application, &err);
  if (err != OPUS_OK || enc == NULL) {
    fprintf(stderr, "%s: opus_encoder_create failed: %d\n", w->name, err);
    return NULL;
  }
  if (opus_encoder_ctl(enc, OPUS_SET_BITRATE(w->bitrate)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_VBR(0)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(w->bandwidth)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_SIGNAL(w->signal)) != OPUS_OK ||
      opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10)) != OPUS_OK) {
    fprintf(stderr, "%s: opus_encoder_ctl setup failed\n", w->name);
    opus_encoder_destroy(enc);
    return NULL;
  }
  return enc;
}

static int encode_workload(OpusEncoder *enc, const Workload *w, unsigned char *packet, int64_t *bytes) {
  *bytes = 0;
  if (opus_encoder_ctl(enc, OPUS_RESET_STATE) != OPUS_OK) {
    fprintf(stderr, "%s: OPUS_RESET_STATE failed\n", w->name);
    return 0;
  }
  int samples_per_frame = w->frame_size * w->channels;
  for (int frame = 0; frame < w->frame_count; frame++) {
    int n = opus_encode_float(enc, w->pcm + frame * samples_per_frame, w->frame_size, packet, MAX_PACKET_BYTES);
    if (n < 0) {
      fprintf(stderr, "%s frame %d: opus_encode_float failed: %d\n", w->name, frame, n);
      return 0;
    }
    *bytes += n;
  }
  return 1;
}

static void summarize_workloads(Workload *workloads, int *indices, int index_count, int64_t *packets, int64_t *samples) {
  *packets = 0;
  *samples = 0;
  for (int i = 0; i < index_count; i++) {
    Workload *w = &workloads[indices[i]];
    *packets += w->frame_count;
    *samples += (int64_t)w->frame_count * w->frame_size;
  }
}

static BenchRun bench_once(Workload *workloads, int *indices, int index_count, uint64_t min_ns) {
  BenchRun run;
  memset(&run, 0, sizeof(run));
  summarize_workloads(workloads, indices, index_count, &run.packets_per_op, &run.samples_per_op);

  OpusEncoder **encs = (OpusEncoder **)calloc((size_t)index_count, sizeof(OpusEncoder *));
  unsigned char *packet = (unsigned char *)malloc(MAX_PACKET_BYTES);
  if (encs == NULL || packet == NULL) {
    fprintf(stderr, "benchmark allocation failed\n");
    free(encs);
    free(packet);
    return run;
  }
  for (int i = 0; i < index_count; i++) {
    encs[i] = create_encoder(&workloads[indices[i]]);
    if (encs[i] == NULL) {
      for (int j = 0; j < i; j++) {
        opus_encoder_destroy(encs[j]);
      }
      free(encs);
      free(packet);
      return run;
    }
  }

  uint64_t start = now_ns();
  uint64_t elapsed = 0;
  uint64_t iterations = 0;
  int64_t total_bytes = 0;
  do {
    int64_t bytes_per_op = 0;
    for (int i = 0; i < index_count; i++) {
      int64_t bytes = 0;
      if (!encode_workload(encs[i], &workloads[indices[i]], packet, &bytes)) {
        for (int j = 0; j < index_count; j++) {
          opus_encoder_destroy(encs[j]);
        }
        free(encs);
        free(packet);
        memset(&run, 0, sizeof(run));
        return run;
      }
      bytes_per_op += bytes;
    }
    total_bytes += bytes_per_op;
    iterations++;
    elapsed = now_ns() - start;
  } while (elapsed < min_ns);

  for (int i = 0; i < index_count; i++) {
    opus_encoder_destroy(encs[i]);
  }
  free(encs);
  free(packet);

  run.elapsed_ns = elapsed;
  run.iterations = iterations;
  run.bytes_per_op = total_bytes / (int64_t)iterations;
  double total_samples = (double)run.samples_per_op * (double)iterations;
  double total_packets = (double)run.packets_per_op * (double)iterations;
  double elapsed_seconds = (double)elapsed / 1000000000.0;
  run.ns_per_sample = (double)elapsed / total_samples;
  run.ns_per_packet = (double)elapsed / total_packets;
  run.x_realtime = (total_samples / (double)SAMPLE_RATE) / elapsed_seconds;
  return run;
}

static int compare_bench_runs(const void *a, const void *b) {
  const BenchRun *ra = (const BenchRun *)a;
  const BenchRun *rb = (const BenchRun *)b;
  if (ra->ns_per_sample < rb->ns_per_sample) {
    return -1;
  }
  if (ra->ns_per_sample > rb->ns_per_sample) {
    return 1;
  }
  return 0;
}

static int run_suite(const char *name, Workload *workloads, int *indices, int index_count, uint64_t min_ns, int count) {
  BenchRun *runs = (BenchRun *)calloc((size_t)count, sizeof(BenchRun));
  if (runs == NULL) {
    return 0;
  }
  for (int i = 0; i < count; i++) {
    runs[i] = bench_once(workloads, indices, index_count, min_ns);
    if (runs[i].iterations == 0) {
      free(runs);
      return 0;
    }
  }
  qsort(runs, (size_t)count, sizeof(BenchRun), compare_bench_runs);
  BenchRun r = runs[count / 2];
  free(runs);

  printf("libopus\tFloat32\t%s\t%s\t%d\t%" PRIu64 "\t%" PRIu64 "\t%" PRId64 "\t%" PRId64 "\t%" PRId64 "\t%.6f\t%.6f\t%.6f\t-\n",
         name, "0s", count, r.iterations, r.elapsed_ns, r.bytes_per_op, r.packets_per_op, r.samples_per_op,
         r.ns_per_sample, r.ns_per_packet, r.x_realtime);
  return 1;
}

int main(int argc, char **argv) {
  uint64_t min_ns = 200000000;
  int count = 3;
  const char *cases = "all";
  Workload *workloads = NULL;
  int workload_count = 0;
  int workload_cap = 0;

  for (int i = 1; i < argc; i++) {
    if (strcmp(argv[i], "--min-ns") == 0 && i + 1 < argc) {
      min_ns = (uint64_t)strtoull(argv[++i], NULL, 10);
    } else if (strcmp(argv[i], "--count") == 0 && i + 1 < argc) {
      count = atoi(argv[++i]);
    } else if (strcmp(argv[i], "--cases") == 0 && i + 1 < argc) {
      cases = argv[++i];
    } else if (strcmp(argv[i], "--case") == 0 && i + 1 < argc) {
      if (workload_count == workload_cap) {
        int next_cap = workload_cap == 0 ? 8 : workload_cap * 2;
        Workload *next = (Workload *)realloc(workloads, (size_t)next_cap * sizeof(Workload));
        if (next == NULL) {
          fprintf(stderr, "workload table realloc failed\n");
          return 1;
        }
        workloads = next;
        workload_cap = next_cap;
      }
      memset(&workloads[workload_count], 0, sizeof(Workload));
      if (!parse_case_spec(argv[++i], &workloads[workload_count])) {
        return 1;
      }
      workload_count++;
    } else {
      usage(argv[0]);
      return 1;
    }
  }

  if (count < 1 || min_ns == 0 || workload_count == 0 ||
      (strcmp(cases, "aggregate") != 0 && strcmp(cases, "per-case") != 0 && strcmp(cases, "all") != 0)) {
    usage(argv[0]);
    return 1;
  }

  printf("implementation\tpath\tvector\tbenchtime\tcount\titerations\telapsed_ns\tbytes_per_op\tpackets_per_op\tsamples_per_op\tns_per_sample\tns_per_packet\tx_realtime\tallocs_per_op\n");

  int ok = 1;
  if (strcmp(cases, "aggregate") == 0 || strcmp(cases, "all") == 0) {
    int *indices = (int *)malloc((size_t)workload_count * sizeof(int));
    if (indices == NULL) {
      ok = 0;
    } else {
      for (int i = 0; i < workload_count; i++) {
        indices[i] = i;
      }
      ok = run_suite("all", workloads, indices, workload_count, min_ns, count) && ok;
      free(indices);
    }
  }
  if (strcmp(cases, "per-case") == 0 || strcmp(cases, "all") == 0) {
    for (int i = 0; i < workload_count; i++) {
      int index = i;
      ok = run_suite(workloads[i].name, workloads, &index, 1, min_ns, count) && ok;
    }
  }

  for (int i = 0; i < workload_count; i++) {
    free_workload(&workloads[i]);
  }
  free(workloads);
  return ok ? 0 : 1;
}
