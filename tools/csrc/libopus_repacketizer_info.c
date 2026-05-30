/* libopus_repacketizer_info.c — oracle for byte-exact repacketizer parity.
 *
 * Each test case drives a small repacketizer program:
 *   - init a fresh OpusRepacketizer
 *   - cat N input packets (in order)
 *   - opus_repacketizer_out_range over [begin,end) into a buffer of size maxlen
 *   - separately: opus_packet_pad / opus_packet_unpad on the first input packet
 *
 * Input frame per case:
 *   u32  nb_packets
 *   for each packet:
 *     u32  packet_len
 *     u8[packet_len]  packet bytes
 *   u32  begin
 *   u32  end          (if end==0 it means "all frames": resolved after cat)
 *   u32  maxlen       (output buffer size for out_range)
 *   u32  pad_new_len  (target length for opus_packet_pad on packet 0; 0 = skip)
 *
 * Output frame per case:
 *   i32  cat_ret           (OPUS_OK==0 if all cats succeeded, else first error)
 *   i32  nb_frames         (rp.nb_frames after all cats, or 0 on cat error)
 *   i32  out_ret           (bytes written by out_range, or error code)
 *   i32  out_len           (== out_ret when >0, else 0)
 *   u8[out_len]  out_bytes
 *   i32  pad_ret           (OPUS_OK==0 or error; or 0x7fffffff if skipped)
 *   i32  pad_len           (final padded length, or 0)
 *   u8[pad_len]  pad_bytes
 *   i32  unpad_ret         (length from opus_packet_unpad on the padded buffer, or error; 0x7fffffff if skipped)
 *   i32  unpad_len
 *   u8[unpad_len] unpad_bytes
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

#define INPUT_MAGIC  "GRPI"
#define OUTPUT_MAGIC "GRPO"

#define SKIPPED 0x7fffffff

static int set_binary_stdio(void) {
#ifdef _WIN32
  if (_setmode(_fileno(stdin),  _O_BINARY) == -1) return 0;
  if (_setmode(_fileno(stdout), _O_BINARY) == -1) return 0;
#endif
  return 1;
}

static int read_exact(void *dst, size_t size) {
  return fread(dst, 1, size, stdin) == size;
}
static int write_exact(const void *src, size_t size) {
  return fwrite(src, 1, size, stdout) == size;
}
static int read_u32(uint32_t *out) { return read_exact(out, 4); }
static int write_i32(int32_t v)    { return write_exact(&v, 4); }
static int write_u32(uint32_t v)   { return write_exact(&v, 4); }

int main(void) {
  char magic[4];
  uint32_t version, count, i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 1;
  if (!read_u32(&version) || version != 1) return 1;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) return 1;

  for (i = 0; i < count; i++) {
    uint32_t nb_packets, j;
    uint32_t begin, end, maxlen, pad_new_len;
    unsigned char *packets[64];
    uint32_t lens[64];
    OpusRepacketizer rp;
    int cat_ret = OPUS_OK;
    int nb_frames = 0;
    int out_ret;
    int out_len = 0;
    unsigned char *out_buf = NULL;
    int pad_ret = SKIPPED;
    int pad_len = 0;
    unsigned char *pad_buf = NULL;
    int unpad_ret = SKIPPED;
    int unpad_len = 0;
    unsigned char *unpad_buf = NULL;

    if (!read_u32(&nb_packets) || nb_packets > 64) return 1;
    for (j = 0; j < nb_packets; j++) {
      if (!read_u32(&lens[j])) return 1;
      packets[j] = NULL;
      if (lens[j] > 0) {
        packets[j] = (unsigned char *)malloc(lens[j]);
        if (!packets[j]) return 1;
        if (!read_exact(packets[j], lens[j])) return 1;
      }
    }
    if (!read_u32(&begin) || !read_u32(&end) || !read_u32(&maxlen) || !read_u32(&pad_new_len))
      return 1;

    /* --- repacketizer cat + out_range --- */
    opus_repacketizer_init(&rp);
    for (j = 0; j < nb_packets; j++) {
      int r = opus_repacketizer_cat(&rp, packets[j], (opus_int32)lens[j]);
      if (r != OPUS_OK) { cat_ret = r; break; }
    }
    if (cat_ret == OPUS_OK) {
      int e = (end == 0) ? rp.nb_frames : (int)end;
      nb_frames = rp.nb_frames;
      out_buf = (unsigned char *)malloc(maxlen ? maxlen : 1);
      if (!out_buf) return 1;
      out_ret = opus_repacketizer_out_range(&rp, (int)begin, e, out_buf, (opus_int32)maxlen);
      if (out_ret > 0) out_len = out_ret;
    } else {
      out_ret = cat_ret;
    }

    /* --- opus_packet_pad / unpad on packet 0 --- */
    if (pad_new_len > 0 && nb_packets > 0 && lens[0] > 0) {
      pad_buf = (unsigned char *)malloc(pad_new_len);
      if (!pad_buf) return 1;
      memcpy(pad_buf, packets[0], lens[0]);
      pad_ret = opus_packet_pad(pad_buf, (opus_int32)lens[0], (opus_int32)pad_new_len);
      if (pad_ret == OPUS_OK) {
        pad_len = (int)pad_new_len;
        /* now unpad in place */
        unpad_buf = (unsigned char *)malloc(pad_new_len);
        if (!unpad_buf) return 1;
        memcpy(unpad_buf, pad_buf, pad_new_len);
        unpad_ret = opus_packet_unpad(unpad_buf, (opus_int32)pad_new_len);
        if (unpad_ret > 0) unpad_len = unpad_ret;
      }
    }

    /* --- emit --- */
    if (!write_i32((int32_t)cat_ret)) return 1;
    if (!write_i32((int32_t)nb_frames)) return 1;
    if (!write_i32((int32_t)out_ret)) return 1;
    if (!write_i32((int32_t)out_len)) return 1;
    if (out_len > 0 && !write_exact(out_buf, out_len)) return 1;
    if (!write_i32((int32_t)pad_ret)) return 1;
    if (!write_i32((int32_t)pad_len)) return 1;
    if (pad_len > 0 && !write_exact(pad_buf, pad_len)) return 1;
    if (!write_i32((int32_t)unpad_ret)) return 1;
    if (!write_i32((int32_t)unpad_len)) return 1;
    if (unpad_len > 0 && !write_exact(unpad_buf, unpad_len)) return 1;

    for (j = 0; j < nb_packets; j++) free(packets[j]);
    free(out_buf);
    free(pad_buf);
    free(unpad_buf);
  }
  return 0;
}
