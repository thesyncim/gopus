/*
 * Helper that keeps libopus opus_compare semantics but accepts PCM directly
 * over stdin/stdout so Go tests can avoid temp files and repeated process
 * launches.
 */

#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif

#define main libopus_opus_compare_cli_main
#include "../../tmp_check/opus-1.6.1/src/opus_compare.c"
#undef main

#define GOCI_MAGIC "GOCI"
#define GOCO_MAGIC "GOCO"

static int read_exact_bytes(void *dst, size_t n) {
  return fread(dst, 1, n, stdin) == n;
}

static int write_exact_bytes(const void *src, size_t n) {
  return fwrite(src, 1, n, stdout) == n;
}

static int read_u32_le(uint32_t *out) {
  unsigned char b[4];
  if (!read_exact_bytes(b, sizeof(b))) {
    return 0;
  }
  *out = (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
  return 1;
}

static int read_i32_le(int32_t *out) {
  uint32_t u = 0;
  if (!read_u32_le(&u)) {
    return 0;
  }
  *out = (int32_t)u;
  return 1;
}

static int write_u32_le(uint32_t v) {
  unsigned char b[4];
  b[0] = (unsigned char)(v & 0xFF);
  b[1] = (unsigned char)((v >> 8) & 0xFF);
  b[2] = (unsigned char)((v >> 16) & 0xFF);
  b[3] = (unsigned char)((v >> 24) & 0xFF);
  return write_exact_bytes(b, sizeof(b));
}

static int write_i32_le(int32_t v) {
  return write_u32_le((uint32_t)v);
}

static int write_f64_le(double v) {
  union {
    double f;
    uint64_t u;
  } bits;
  unsigned char b[8];
  bits.f = v;
  b[0] = (unsigned char)(bits.u & 0xFF);
  b[1] = (unsigned char)((bits.u >> 8) & 0xFF);
  b[2] = (unsigned char)((bits.u >> 16) & 0xFF);
  b[3] = (unsigned char)((bits.u >> 24) & 0xFF);
  b[4] = (unsigned char)((bits.u >> 32) & 0xFF);
  b[5] = (unsigned char)((bits.u >> 40) & 0xFF);
  b[6] = (unsigned char)((bits.u >> 48) & 0xFF);
  b[7] = (unsigned char)((bits.u >> 56) & 0xFF);
  return write_exact_bytes(b, sizeof(b));
}

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin), _O_BINARY) == -1) {
    return 0;
  }
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) {
    return 0;
  }
#endif
  return 1;
}

static int read_pcm16_stream(int16_t *dst, size_t n) {
  for (size_t i = 0; i < n; i++) {
    unsigned char b[2];
    if (!read_exact_bytes(b, sizeof(b))) {
      return 0;
    }
    dst[i] = (int16_t)((uint16_t)b[0] | ((uint16_t)b[1] << 8));
  }
  return 1;
}

static int read_delay_stream(int32_t *dst, size_t n) {
  for (size_t i = 0; i < n; i++) {
    if (!read_i32_le(dst + i)) {
      return 0;
    }
  }
  return 1;
}

static int16_t *alloc_pcm16_buffer(size_t n) {
  if (n == 0) {
    return NULL;
  }
  if (n > SIZE_MAX / sizeof(int16_t)) {
    return NULL;
  }
  return (int16_t *)malloc(n * sizeof(int16_t));
}

static float *pcm16_to_float(const int16_t *src, size_t n) {
  float *dst;
  size_t i;
  if (n == 0) {
    return NULL;
  }
  if (n > SIZE_MAX / sizeof(float)) {
    return NULL;
  }
  dst = (float *)malloc(n * sizeof(float));
  if (dst == NULL) {
    return NULL;
  }
  for (i = 0; i < n; i++) {
    dst[i] = (float)src[i];
  }
  return dst;
}

/*
 * A bandCache memoizes the raw band_energy output of one PCM side keyed by its
 * start offset within the request. band_energy frame xi depends only on the
 * window at (start + xi*TEST_WIN_STEP), so the first F frames of a result
 * computed for F_max >= F frames are bit-identical to a fresh F-frame compute.
 * We therefore compute each distinct start once at the maximum frame count and
 * slice the prefix per delay candidate. This removes the dominant cost of
 * re-running the band_energy DFT for every delay candidate while keeping every
 * floating-point operation (and hence every Q value) identical to upstream.
 */
typedef struct {
  const float *base; /* PCM base pointer of this side */
  int with_xb;       /* whether xb (masking energy) is also cached */
  size_t cap;        /* number of cached start slots */
  size_t count;
  int *starts;
  size_t *nframes; /* frames computed (== max needed for this start) */
  float **xb;      /* cached xb, NULL when with_xb==0 */
  float **ps;      /* cached raw band_energy ps (X or Y) */
} bandCache;

static void band_cache_init(bandCache *c, const float *base, int with_xb, size_t cap) {
  c->base = base;
  c->with_xb = with_xb;
  c->cap = cap;
  c->count = 0;
  c->starts = (int *)malloc(cap * sizeof(*c->starts));
  c->nframes = (size_t *)malloc(cap * sizeof(*c->nframes));
  c->xb = (float **)calloc(cap, sizeof(*c->xb));
  c->ps = (float **)calloc(cap, sizeof(*c->ps));
}

static void band_cache_free(bandCache *c) {
  for (size_t i = 0; i < c->count; i++) {
    free(c->xb[i]);
    free(c->ps[i]);
  }
  free(c->starts);
  free(c->nframes);
  free(c->xb);
  free(c->ps);
}

/* band_cache_get returns cached raw band energies for (start) covering at least
   nframes, computing them on first use. If an entry for start exists but covers
   fewer frames, it is recomputed at the larger frame count (a band_energy result
   for F_max frames contains every shorter prefix bit-for-bit, so a re-grown entry
   yields identical values for any earlier candidate). Returns 0 on allocation
   failure. */
static int band_cache_get(bandCache *c, int nchannels, size_t start, size_t nframes, float **out_xb, float **out_ps) {
  size_t slot = c->count;
  for (size_t i = 0; i < c->count; i++) {
    if (c->starts[i] == (int)start) {
      if (c->nframes[i] >= nframes) {
        if (out_xb) {
          *out_xb = c->xb[i];
        }
        *out_ps = c->ps[i];
        return 1;
      }
      slot = i; /* grow this entry to the larger frame count */
      break;
    }
  }

  float *xb = NULL;
  float *ps = (float *)opus_malloc(nframes * NFREQS * nchannels * sizeof(*ps));
  if (ps == NULL) {
    return 0;
  }
  if (c->with_xb) {
    xb = (float *)opus_malloc(nframes * NBANDS * nchannels * sizeof(*xb));
    if (xb == NULL) {
      free(ps);
      return 0;
    }
  }
  band_energy(xb, ps, BANDS, NBANDS, c->base + start, nchannels, nframes, TEST_WIN_SIZE, TEST_WIN_STEP, 1);

  if (slot == c->count) {
    if (c->count >= c->cap) {
      free(xb);
      free(ps);
      return 0;
    }
    c->count++;
  } else {
    free(c->xb[slot]);
    free(c->ps[slot]);
  }
  c->starts[slot] = (int)start;
  c->nframes[slot] = nframes;
  c->xb[slot] = xb;
  c->ps[slot] = ps;
  if (out_xb) {
    *out_xb = xb;
  }
  *out_ps = ps;
  return 1;
}

/* compare_quality_cached scores one delay candidate reusing cached raw band
   energies for the reference (xb+X) and decoded (Y) sides. The arithmetic is a
   faithful copy of opus_compare.c's 48 kHz path; only the redundant per-band
   DFT has been hoisted into the caches. */
static int compare_quality_cached(bandCache *refCache, bandCache *decCache, size_t ref_start, size_t dec_start,
                                  size_t frames, int nchannels, double *out_q) {
  float *xb;
  float *X;
  float *Y;
  const float *xb_raw;
  const float *X_raw;
  const float *Y_raw;
  double err;
  size_t nframes;
  size_t xi;
  int ci;
  int xj;
  int bi;
  int max_compare;

  if (frames < TEST_WIN_SIZE) {
    *out_q = -INFINITY;
    return 1;
  }

  nframes = (frames - TEST_WIN_SIZE + TEST_WIN_STEP) / TEST_WIN_STEP;

  {
    float *ref_xb;
    float *ref_ps;
    float *dec_ps;
    if (!band_cache_get(refCache, nchannels, ref_start, nframes, &ref_xb, &ref_ps)) {
      return 0;
    }
    if (!band_cache_get(decCache, nchannels, dec_start, nframes, NULL, &dec_ps)) {
      return 0;
    }
    xb_raw = ref_xb;
    X_raw = ref_ps;
    Y_raw = dec_ps;
  }

  /* Copy the cached raw energies into per-candidate scratch the masking steps
     mutate in place. These memcpys are cheap relative to the band_energy DFT. */
  xb = (float *)opus_malloc(nframes * NBANDS * nchannels * sizeof(*xb));
  X = (float *)opus_malloc(nframes * NFREQS * nchannels * sizeof(*X));
  Y = (float *)opus_malloc(nframes * NFREQS * nchannels * sizeof(*Y));
  if (xb == NULL || X == NULL || Y == NULL) {
    free(xb);
    free(X);
    free(Y);
    return 0;
  }
  memcpy(xb, xb_raw, nframes * NBANDS * nchannels * sizeof(*xb));
  memcpy(X, X_raw, nframes * NFREQS * nchannels * sizeof(*X));
  memcpy(Y, Y_raw, nframes * NFREQS * nchannels * sizeof(*Y));

  for (xi = 0; xi < nframes; xi++) {
    for (bi = 1; bi < NBANDS; bi++) {
      for (ci = 0; ci < nchannels; ci++) {
        xb[(xi * NBANDS + bi) * nchannels + ci] += 0.1F * xb[(xi * NBANDS + bi - 1) * nchannels + ci];
      }
    }
    for (bi = NBANDS - 1; bi-- > 0;) {
      for (ci = 0; ci < nchannels; ci++) {
        xb[(xi * NBANDS + bi) * nchannels + ci] += 0.03F * xb[(xi * NBANDS + bi + 1) * nchannels + ci];
      }
    }
    if (xi > 0) {
      for (bi = 0; bi < NBANDS; bi++) {
        for (ci = 0; ci < nchannels; ci++) {
          xb[(xi * NBANDS + bi) * nchannels + ci] += 0.5F * xb[((xi - 1) * NBANDS + bi) * nchannels + ci];
        }
      }
    }
    if (nchannels == 2) {
      for (bi = 0; bi < NBANDS; bi++) {
        float l;
        float r;
        l = xb[(xi * NBANDS + bi) * nchannels + 0];
        r = xb[(xi * NBANDS + bi) * nchannels + 1];
        xb[(xi * NBANDS + bi) * nchannels + 0] += 0.01F * r;
        xb[(xi * NBANDS + bi) * nchannels + 1] += 0.01F * l;
      }
    }

    for (bi = 0; bi < NBANDS; bi++) {
      for (xj = BANDS[bi]; xj < BANDS[bi + 1]; xj++) {
        for (ci = 0; ci < nchannels; ci++) {
          X[(xi * NFREQS + xj) * nchannels + ci] += 0.1F * xb[(xi * NBANDS + bi) * nchannels + ci];
          Y[(xi * NFREQS + xj) * nchannels + ci] += 0.1F * xb[(xi * NBANDS + bi) * nchannels + ci];
        }
      }
    }
  }

  for (bi = 0; bi < NBANDS; bi++) {
    for (xj = BANDS[bi]; xj < BANDS[bi + 1]; xj++) {
      for (ci = 0; ci < nchannels; ci++) {
        float xtmp;
        float ytmp;
        xtmp = X[xj * nchannels + ci];
        ytmp = Y[xj * nchannels + ci];
        for (xi = 1; xi < nframes; xi++) {
          float xtmp2;
          float ytmp2;
          xtmp2 = X[(xi * NFREQS + xj) * nchannels + ci];
          ytmp2 = Y[(xi * NFREQS + xj) * nchannels + ci];
          X[(xi * NFREQS + xj) * nchannels + ci] += xtmp;
          Y[(xi * NFREQS + xj) * nchannels + ci] += ytmp;
          xtmp = xtmp2;
          ytmp = ytmp2;
        }
      }
    }
  }

  max_compare = BANDS[NBANDS];
  err = 0;
  for (xi = 0; xi < nframes; xi++) {
    double Ef;
    Ef = 0;
    for (bi = 0; bi < NBANDS; bi++) {
      double Eb;
      Eb = 0;
      for (xj = BANDS[bi]; xj < BANDS[bi + 1] && xj < max_compare; xj++) {
        for (ci = 0; ci < nchannels; ci++) {
          float re;
          float im;
          re = Y[(xi * NFREQS + xj) * nchannels + ci] / X[(xi * NFREQS + xj) * nchannels + ci];
          im = re - logf(re) - 1;
          if (xj >= 79 && xj <= 81) {
            im *= 0.1F;
          }
          if (xj == 80) {
            im *= 0.1F;
          }
          Eb += im;
        }
      }
      Eb /= (BANDS[bi + 1] - BANDS[bi]) * nchannels;
      Ef += Eb * Eb;
    }
    Ef /= NBANDS;
    Ef *= Ef;
    err += Ef * Ef;
  }

  free(xb);
  free(X);
  free(Y);

  err = pow(err / nframes, 1.0 / 16.0);
  *out_q = 100.0 * (1.0 - 0.5 * log(1.0 + err) / log(1.13));
  return 1;
}

static int abs_i32(int32_t v) {
  return v < 0 ? -v : v;
}

static int handle_request(void) {
  unsigned char magic[4];
  uint32_t version = 0;
  uint32_t sample_rate = 0;
  uint32_t channels = 0;
  uint32_t reference_len = 0;
  uint32_t decoded_len = 0;
  uint32_t delay_count = 0;
  int16_t *reference_pcm = NULL;
  int16_t *decoded_pcm = NULL;
  float *reference = NULL;
  float *decoded = NULL;
  int32_t *delays = NULL;
  double best_q = -INFINITY;
  int32_t best_delay = 0;
  int found = 0;

  if (!read_exact_bytes(magic, sizeof(magic))) {
    return 0;
  }
  if (memcmp(magic, GOCI_MAGIC, sizeof(magic)) != 0) {
    fprintf(stderr, "invalid compare input magic\n");
    return -1;
  }
  if (!read_u32_le(&version) || version != 1 || !read_u32_le(&sample_rate) || !read_u32_le(&channels) ||
      !read_u32_le(&reference_len) || !read_u32_le(&decoded_len) || !read_u32_le(&delay_count)) {
    fprintf(stderr, "failed to read compare header\n");
    return -1;
  }
  if (sample_rate != 48000) {
    fprintf(stderr, "unsupported sample rate %u\n", sample_rate);
    return -1;
  }
  if (channels != 1 && channels != 2) {
    fprintf(stderr, "unsupported channel count %u\n", channels);
    return -1;
  }
  if (delay_count == 0) {
    fprintf(stderr, "missing delay candidates\n");
    return -1;
  }

  reference_pcm = alloc_pcm16_buffer(reference_len);
  decoded_pcm = alloc_pcm16_buffer(decoded_len);
  delays = (int32_t *)malloc((size_t)delay_count * sizeof(*delays));
  if ((reference_len > 0 && reference_pcm == NULL) || (decoded_len > 0 && decoded_pcm == NULL) || delays == NULL) {
    fprintf(stderr, "failed to allocate request buffers\n");
    free(reference_pcm);
    free(decoded_pcm);
    free(delays);
    return -1;
  }
  if ((reference_len > 0 && !read_pcm16_stream(reference_pcm, reference_len)) ||
      (decoded_len > 0 && !read_pcm16_stream(decoded_pcm, decoded_len)) || !read_delay_stream(delays, delay_count)) {
    fprintf(stderr, "failed to read request payload\n");
    free(reference_pcm);
    free(decoded_pcm);
    free(delays);
    return -1;
  }

  reference = pcm16_to_float(reference_pcm, reference_len);
  decoded = pcm16_to_float(decoded_pcm, decoded_len);
  if ((reference_len > 0 && reference == NULL) || (decoded_len > 0 && decoded == NULL)) {
    fprintf(stderr, "failed to convert pcm payload\n");
    free(reference_pcm);
    free(decoded_pcm);
    free(reference);
    free(decoded);
    free(delays);
    return -1;
  }

  if (reference_len == 0 || decoded_len == 0) {
    best_q = -INFINITY;
    best_delay = delays[0];
    found = 1;
  } else {
    bandCache refCache;
    bandCache decCache;
    int cache_failed = 0;
    band_cache_init(&refCache, reference, 1, delay_count);
    band_cache_init(&decCache, decoded, 0, delay_count);

    for (uint32_t i = 0; i < delay_count && !cache_failed; i++) {
      size_t ref_start = 0;
      size_t dec_start = 0;
      size_t n;
      size_t frames;
      double q = -INFINITY;

      if (delays[i] > 0) {
        dec_start = (size_t)delays[i];
      } else if (delays[i] < 0) {
        ref_start = (size_t)(-delays[i]);
      }
      if (ref_start >= reference_len || dec_start >= decoded_len) {
        continue;
      }

      n = reference_len - ref_start;
      if (decoded_len - dec_start < n) {
        n = decoded_len - dec_start;
      }
      n -= n % channels;
      if (n == 0) {
        continue;
      }
      frames = n / channels;
      if (frames < TEST_WIN_SIZE) {
        continue;
      }
      if (!compare_quality_cached(&refCache, &decCache, ref_start, dec_start, frames, (int)channels, &q)) {
        cache_failed = 1;
        break;
      }
      if (!found || q > best_q || (q == best_q && abs_i32(delays[i]) < abs_i32(best_delay))) {
        best_q = q;
        best_delay = delays[i];
        found = 1;
      }
    }
    band_cache_free(&refCache);
    band_cache_free(&decCache);
    if (cache_failed) {
      fprintf(stderr, "failed to evaluate opus_compare request\n");
      free(reference_pcm);
      free(decoded_pcm);
      free(reference);
      free(decoded);
      free(delays);
      return -1;
    }
  }

  free(reference_pcm);
  free(decoded_pcm);
  free(reference);
  free(decoded);
  free(delays);

  if (!found) {
    best_q = -INFINITY;
    best_delay = 0;
  }

  if (!write_exact_bytes(GOCO_MAGIC, 4) || !write_i32_le(best_delay) || !write_f64_le(best_q) || fflush(stdout) != 0) {
    fprintf(stderr, "failed to write compare response\n");
    return -1;
  }
  return 1;
}

int main(void) {
  if (!set_binary_stdio()) {
    fprintf(stderr, "failed to set binary stdio mode\n");
    return 1;
  }
  for (;;) {
    int rc = handle_request();
    if (rc == 0) {
      return 0;
    }
    if (rc < 0) {
      return 1;
    }
  }
}
