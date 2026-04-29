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
#define CHANNELS 2
#define MAX_PACKET_SAMPLES 5760

typedef struct {
  unsigned char *data;
  int len;
} Packet;

typedef struct {
  char *name;
  Packet *packets;
  int packet_count;
  int64_t packet_bytes;
} Vector;

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
          "usage: %s --min-ns N --count N --cases aggregate|per-vector|all "
          "--paths float32|int16|all VECTOR.bit...\n",
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

static char *vector_name_from_path(const char *path) {
  const char *base = strrchr(path, '/');
  const char *slash = strrchr(path, '\\');
  if (slash != NULL && (base == NULL || slash > base)) {
    base = slash;
  }
  base = base == NULL ? path : base + 1;

  char *name = dup_string(base);
  if (name == NULL) {
    return NULL;
  }
  size_t n = strlen(name);
  if (n > 4 && strcmp(name + n - 4, ".bit") == 0) {
    name[n - 4] = '\0';
  }
  return name;
}

static uint32_t read_be32(const unsigned char *p) {
  return ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) | ((uint32_t)p[2] << 8) | (uint32_t)p[3];
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

static int load_vector(const char *path, Vector *v) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) {
    fprintf(stderr, "open %s: %s\n", path, strerror(errno));
    return 0;
  }

  int64_t size = file_size(f);
  if (size < 0 || size > INT32_MAX) {
    fprintf(stderr, "invalid file size for %s\n", path);
    fclose(f);
    return 0;
  }

  unsigned char *buf = (unsigned char *)malloc((size_t)size);
  if (buf == NULL) {
    fprintf(stderr, "malloc input for %s failed\n", path);
    fclose(f);
    return 0;
  }
  if (fread(buf, 1, (size_t)size, f) != (size_t)size) {
    fprintf(stderr, "read %s failed\n", path);
    free(buf);
    fclose(f);
    return 0;
  }
  fclose(f);

  v->name = vector_name_from_path(path);
  if (v->name == NULL) {
    free(buf);
    return 0;
  }

  int packet_cap = 0;
  int offset = 0;
  while (offset < size) {
    if (offset + 8 > size) {
      fprintf(stderr, "%s: truncated opus_demo packet header\n", path);
      free(buf);
      return 0;
    }
    uint32_t packet_len = read_be32(buf + offset);
    offset += 8; /* skip packet length and final range */
    if (packet_len > (uint32_t)(size - offset)) {
      fprintf(stderr, "%s: truncated packet payload\n", path);
      free(buf);
      return 0;
    }

    if (v->packet_count == packet_cap) {
      int next_cap = packet_cap == 0 ? 1024 : packet_cap * 2;
      Packet *next = (Packet *)realloc(v->packets, (size_t)next_cap * sizeof(Packet));
      if (next == NULL) {
        fprintf(stderr, "packet table realloc failed\n");
        free(buf);
        return 0;
      }
      v->packets = next;
      packet_cap = next_cap;
    }

    Packet *p = &v->packets[v->packet_count++];
    p->len = (int)packet_len;
    p->data = NULL;
    if (packet_len > 0) {
      p->data = (unsigned char *)malloc(packet_len);
      if (p->data == NULL) {
        fprintf(stderr, "packet malloc failed\n");
        free(buf);
        return 0;
      }
      memcpy(p->data, buf + offset, packet_len);
      v->packet_bytes += packet_len;
    }
    offset += (int)packet_len;
  }

  free(buf);
  if (v->packet_count == 0) {
    fprintf(stderr, "%s: no packets\n", path);
    return 0;
  }
  return 1;
}

static void free_vector(Vector *v) {
  if (v == NULL) {
    return;
  }
  for (int i = 0; i < v->packet_count; i++) {
    free(v->packets[i].data);
  }
  free(v->packets);
  free(v->name);
}

static uint64_t now_ns(void) {
  struct timespec ts;
  if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
    return 0;
  }
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

static void summarize_vectors(Vector *vectors, int vector_count, int64_t *packets, int64_t *bytes) {
  *packets = 0;
  *bytes = 0;
  for (int i = 0; i < vector_count; i++) {
    *packets += vectors[i].packet_count;
    *bytes += vectors[i].packet_bytes;
  }
}

static int decode_float_case(OpusDecoder *dec, Vector *vectors, int vector_count, float *pcm, int64_t *samples) {
  *samples = 0;
  for (int v = 0; v < vector_count; v++) {
    opus_decoder_ctl(dec, OPUS_RESET_STATE);
    for (int i = 0; i < vectors[v].packet_count; i++) {
      Packet *p = &vectors[v].packets[i];
      int n = opus_decode_float(dec, p->data, p->len, pcm, MAX_PACKET_SAMPLES, 0);
      if (n < 0) {
        fprintf(stderr, "opus_decode_float %s packet %d failed: %d\n", vectors[v].name, i, n);
        return 0;
      }
      *samples += n;
    }
  }
  return 1;
}

static int decode_int16_case(OpusDecoder *dec, Vector *vectors, int vector_count, opus_int16 *pcm, int64_t *samples) {
  *samples = 0;
  for (int v = 0; v < vector_count; v++) {
    opus_decoder_ctl(dec, OPUS_RESET_STATE);
    for (int i = 0; i < vectors[v].packet_count; i++) {
      Packet *p = &vectors[v].packets[i];
      int n = opus_decode(dec, p->data, p->len, pcm, MAX_PACKET_SAMPLES, 0);
      if (n < 0) {
        fprintf(stderr, "opus_decode %s packet %d failed: %d\n", vectors[v].name, i, n);
        return 0;
      }
      *samples += n;
    }
  }
  return 1;
}

static int compare_run(const void *a, const void *b) {
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

static int bench_case(const char *path_name, const char *vector_name, Vector *vectors, int vector_count,
                      uint64_t min_ns, int count) {
  int err = OPUS_OK;
  OpusDecoder *dec = opus_decoder_create(SAMPLE_RATE, CHANNELS, &err);
  if (dec == NULL || err != OPUS_OK) {
    fprintf(stderr, "opus_decoder_create failed: %d\n", err);
    return 0;
  }

  float *pcm_float = NULL;
  opus_int16 *pcm_int16 = NULL;
  int64_t expected_samples = 0;
  int64_t packets_per_op = 0;
  int64_t bytes_per_op = 0;
  int ok = 1;

  summarize_vectors(vectors, vector_count, &packets_per_op, &bytes_per_op);

  if (strcmp(path_name, "Float32") == 0) {
    pcm_float = (float *)malloc((size_t)MAX_PACKET_SAMPLES * CHANNELS * sizeof(float));
    if (pcm_float == NULL || !decode_float_case(dec, vectors, vector_count, pcm_float, &expected_samples)) {
      ok = 0;
    }
  } else {
    pcm_int16 = (opus_int16 *)malloc((size_t)MAX_PACKET_SAMPLES * CHANNELS * sizeof(opus_int16));
    if (pcm_int16 == NULL || !decode_int16_case(dec, vectors, vector_count, pcm_int16, &expected_samples)) {
      ok = 0;
    }
  }

  if (!ok || expected_samples <= 0) {
    free(pcm_float);
    free(pcm_int16);
    opus_decoder_destroy(dec);
    return 0;
  }

  BenchRun *runs = (BenchRun *)calloc((size_t)count, sizeof(BenchRun));
  if (runs == NULL) {
    free(pcm_float);
    free(pcm_int16);
    opus_decoder_destroy(dec);
    return 0;
  }

  for (int r = 0; r < count; r++) {
    uint64_t start = now_ns();
    uint64_t elapsed = 0;
    uint64_t iterations = 0;

    do {
      int64_t got_samples = 0;
      if (strcmp(path_name, "Float32") == 0) {
        ok = decode_float_case(dec, vectors, vector_count, pcm_float, &got_samples);
      } else {
        ok = decode_int16_case(dec, vectors, vector_count, pcm_int16, &got_samples);
      }
      if (!ok || got_samples != expected_samples) {
        fprintf(stderr, "%s %s decoded samples mismatch: got %" PRId64 " want %" PRId64 "\n",
                path_name, vector_name, got_samples, expected_samples);
        free(runs);
        free(pcm_float);
        free(pcm_int16);
        opus_decoder_destroy(dec);
        return 0;
      }
      iterations++;
      elapsed = now_ns() - start;
    } while (elapsed < min_ns || iterations == 0);

    runs[r].elapsed_ns = elapsed;
    runs[r].iterations = iterations;
    runs[r].samples_per_op = expected_samples;
    runs[r].packets_per_op = packets_per_op;
    runs[r].bytes_per_op = bytes_per_op;
    runs[r].ns_per_sample = (double)elapsed / ((double)expected_samples * (double)iterations);
    runs[r].ns_per_packet = (double)elapsed / ((double)packets_per_op * (double)iterations);
    runs[r].x_realtime = (((double)expected_samples * (double)iterations) / (double)SAMPLE_RATE) /
                          ((double)elapsed / 1000000000.0);
  }

  qsort(runs, (size_t)count, sizeof(BenchRun), compare_run);
  BenchRun median = runs[count / 2];
  printf("libopus\t%s\t%s\t%d\t%" PRIu64 "\t%" PRIu64 "\t%" PRId64 "\t%" PRId64 "\t%" PRId64
         "\t%.6f\t%.6f\t%.6f\n",
         path_name, vector_name, count, median.iterations, median.elapsed_ns, median.bytes_per_op,
         median.packets_per_op, median.samples_per_op, median.ns_per_sample, median.ns_per_packet,
         median.x_realtime);

  free(runs);
  free(pcm_float);
  free(pcm_int16);
  opus_decoder_destroy(dec);
  return 1;
}

static int wants_aggregate(const char *cases) {
  return strcmp(cases, "aggregate") == 0 || strcmp(cases, "all") == 0;
}

static int wants_per_vector(const char *cases) {
  return strcmp(cases, "per-vector") == 0 || strcmp(cases, "all") == 0;
}

static int wants_float32(const char *paths) {
  return strcmp(paths, "float32") == 0 || strcmp(paths, "all") == 0;
}

static int wants_int16(const char *paths) {
  return strcmp(paths, "int16") == 0 || strcmp(paths, "all") == 0;
}

int main(int argc, char **argv) {
  uint64_t min_ns = 200000000ULL;
  int count = 3;
  const char *cases = "all";
  const char *paths = "all";
  int first_path = 1;

  for (int i = 1; i < argc; i++) {
    if (strcmp(argv[i], "--min-ns") == 0 && i + 1 < argc) {
      min_ns = (uint64_t)strtoull(argv[++i], NULL, 10);
    } else if (strcmp(argv[i], "--count") == 0 && i + 1 < argc) {
      count = atoi(argv[++i]);
    } else if (strcmp(argv[i], "--cases") == 0 && i + 1 < argc) {
      cases = argv[++i];
    } else if (strcmp(argv[i], "--paths") == 0 && i + 1 < argc) {
      paths = argv[++i];
    } else if (strcmp(argv[i], "--") == 0) {
      first_path = i + 1;
      break;
    } else if (argv[i][0] == '-') {
      usage(argv[0]);
      return 2;
    } else {
      first_path = i;
      break;
    }
    first_path = i + 1;
  }

  if (count < 1 || min_ns == 0 || first_path >= argc) {
    usage(argv[0]);
    return 2;
  }
  if (!wants_aggregate(cases) && !wants_per_vector(cases)) {
    usage(argv[0]);
    return 2;
  }
  if (!wants_float32(paths) && !wants_int16(paths)) {
    usage(argv[0]);
    return 2;
  }

  int vector_count = argc - first_path;
  Vector *vectors = (Vector *)calloc((size_t)vector_count, sizeof(Vector));
  if (vectors == NULL) {
    return 1;
  }

  for (int i = 0; i < vector_count; i++) {
    if (!load_vector(argv[first_path + i], &vectors[i])) {
      for (int j = 0; j <= i; j++) {
        free_vector(&vectors[j]);
      }
      free(vectors);
      return 1;
    }
  }

  printf("implementation\tpath\tvector\tcount\titerations\telapsed_ns\tbytes_per_op\tpackets_per_op\tsamples_per_op\tns_per_sample\tns_per_packet\tx_realtime\n");

  const char *bench_paths[2] = {"Float32", "Int16"};
  for (int p = 0; p < 2; p++) {
    if ((p == 0 && !wants_float32(paths)) || (p == 1 && !wants_int16(paths))) {
      continue;
    }
    if (wants_aggregate(cases) && !bench_case(bench_paths[p], "all", vectors, vector_count, min_ns, count)) {
      for (int i = 0; i < vector_count; i++) {
        free_vector(&vectors[i]);
      }
      free(vectors);
      return 1;
    }
    if (wants_per_vector(cases)) {
      for (int i = 0; i < vector_count; i++) {
        if (!bench_case(bench_paths[p], vectors[i].name, &vectors[i], 1, min_ns, count)) {
          for (int j = 0; j < vector_count; j++) {
            free_vector(&vectors[j]);
          }
          free(vectors);
          return 1;
        }
      }
    }
  }

  for (int i = 0; i < vector_count; i++) {
    free_vector(&vectors[i]);
  }
  free(vectors);
  return 0;
}
