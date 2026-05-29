/* libopus_packet_parse_info.c — oracle for full TOC/packet edge-code parity.
 *
 * For each test case the oracle invokes:
 *   opus_packet_get_bandwidth()
 *   opus_packet_get_nb_channels()
 *   opus_packet_get_nb_frames()
 *   opus_packet_get_samples_per_frame()   (at Fs=48000)
 *   opus_packet_get_nb_samples()          (at Fs=48000)
 *   opus_packet_parse()
 *
 * Input frame per case:
 *   u32  packet_len
 *   u8[packet_len]  packet bytes
 *
 * Output frame per case:
 *   i32  bandwidth          (OPUS_BANDWIDTH_* or OPUS_INVALID_PACKET)
 *   i32  nb_channels        (1 or 2)
 *   i32  nb_frames          (>=1, or OPUS_BAD_ARG / OPUS_INVALID_PACKET)
 *   i32  samples_per_frame  (at 48000, or OPUS_BAD_ARG if len==0)
 *   i32  nb_samples         (at 48000, or error code)
 *   i32  parse_ret          (>=1 frame count from opus_packet_parse, or error)
 *   i32  parse_toc          (toc byte as written by opus_packet_parse, or -1 on error)
 *   i32  parse_payload_off  (payload_offset, or -1 on error)
 *   i32  parse_nframes      (number of size[] entries == parse_ret when >=1, else 0)
 *   i16[parse_nframes]  frame_sizes  (each frame size in bytes, in order)
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

#define INPUT_MAGIC  "GPPI"
#define OUTPUT_MAGIC "GPPO"

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
static int read_u32(uint32_t *out)    { return read_exact(out, 4); }
static int write_i32(int32_t v)       { return write_exact(&v, 4); }
static int write_u32(uint32_t v)      { return write_exact(&v, 4); }
static int write_i16(int16_t v)       { return write_exact(&v, 2); }

int main(void) {
  char magic[4];
  uint32_t version, count, i;

  if (!set_binary_stdio()) return 1;
  if (!read_exact(magic, 4) || memcmp(magic, INPUT_MAGIC, 4) != 0) return 1;
  if (!read_u32(&version) || version != 1) return 1;
  if (!read_u32(&count)) return 1;

  if (!write_exact(OUTPUT_MAGIC, 4) || !write_u32(1) || !write_u32(count)) return 1;

  for (i = 0; i < count; i++) {
    uint32_t len;
    unsigned char *pkt;
    int bandwidth, nb_channels, nb_frames, spf, nb_samples;
    int parse_ret;
    unsigned char parse_toc = 0;
    const unsigned char *frames[48];
    opus_int16 sizes[48];
    int payload_off = -1;
    int j;

    if (!read_u32(&len)) return 1;
    pkt = NULL;
    if (len > 0) {
      pkt = (unsigned char *)malloc(len);
      if (!pkt) return 1;
      if (!read_exact(pkt, len)) { free(pkt); return 1; }
    }

    if (len == 0) {
      /* empty packet — all functions return error codes */
      bandwidth    = OPUS_INVALID_PACKET;
      nb_channels  = OPUS_INVALID_PACKET;
      nb_frames    = OPUS_BAD_ARG;
      spf          = OPUS_BAD_ARG;
      nb_samples   = OPUS_BAD_ARG;
      parse_ret    = OPUS_INVALID_PACKET;
    } else {
      bandwidth   = opus_packet_get_bandwidth(pkt);
      nb_channels = opus_packet_get_nb_channels(pkt);
      nb_frames   = opus_packet_get_nb_frames(pkt, (opus_int32)len);
      spf         = opus_packet_get_samples_per_frame(pkt, 48000);
      nb_samples  = opus_packet_get_nb_samples(pkt, (opus_int32)len, 48000);

      memset(sizes, 0, sizeof(sizes));
      parse_ret = opus_packet_parse(pkt, (opus_int32)len, &parse_toc,
                                    frames, sizes, &payload_off);
    }

    free(pkt);

    /* bandwidth */
    if (!write_i32((int32_t)bandwidth)) return 1;
    /* nb_channels */
    if (!write_i32((int32_t)nb_channels)) return 1;
    /* nb_frames */
    if (!write_i32((int32_t)nb_frames)) return 1;
    /* samples_per_frame */
    if (!write_i32((int32_t)spf)) return 1;
    /* nb_samples */
    if (!write_i32((int32_t)nb_samples)) return 1;
    /* parse_ret */
    if (!write_i32((int32_t)parse_ret)) return 1;
    /* parse_toc (-1 if parse failed) */
    if (!write_i32(parse_ret > 0 ? (int32_t)(uint8_t)parse_toc : -1)) return 1;
    /* payload_offset (-1 if parse failed) */
    if (!write_i32(parse_ret > 0 ? (int32_t)payload_off : -1)) return 1;
    /* number of frame sizes emitted */
    {
      int nf = parse_ret > 0 ? parse_ret : 0;
      if (!write_i32((int32_t)nf)) return 1;
      for (j = 0; j < nf; j++) {
        if (!write_i16(sizes[j])) return 1;
      }
    }
  }
  return 0;
}
