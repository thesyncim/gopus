#ifndef GOPUS_LIBOPUS_DNN_BLOB_IO_H
#define GOPUS_LIBOPUS_DNN_BLOB_IO_H

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#if WEIGHT_BLOCK_SIZE != 64
#error "unexpected libopus weight header size"
#endif

#define GOPUS_WEIGHT_HEADER_NAME_OFFSET 20
#define GOPUS_WEIGHT_HEADER_NAME_SIZE (WEIGHT_BLOCK_SIZE - GOPUS_WEIGHT_HEADER_NAME_OFFSET)

static void gopus_dnn_blob_put_le32(unsigned char *dst, int value) {
  uint32_t u = (uint32_t)value;
  dst[0] = (unsigned char)(u & 0xffu);
  dst[1] = (unsigned char)((u >> 8) & 0xffu);
  dst[2] = (unsigned char)((u >> 16) & 0xffu);
  dst[3] = (unsigned char)((u >> 24) & 0xffu);
}

static int gopus_dnn_blob_write_weight_header(FILE *out, const WeightArray *array, int block_size) {
  unsigned char header[WEIGHT_BLOCK_SIZE] = {0};
  memcpy(header, "DNNw", 4);
  gopus_dnn_blob_put_le32(header + 4, WEIGHT_BLOB_VERSION);
  gopus_dnn_blob_put_le32(header + 8, array->type);
  gopus_dnn_blob_put_le32(header + 12, array->size);
  gopus_dnn_blob_put_le32(header + 16, block_size);
  strncpy((char *)header + GOPUS_WEIGHT_HEADER_NAME_OFFSET, array->name, GOPUS_WEIGHT_HEADER_NAME_SIZE - 1);
  return fwrite(header, 1, sizeof(header), out) == sizeof(header);
}

static int write_weights(FILE *out, const WeightArray *list) {
  unsigned char zeros[WEIGHT_BLOCK_SIZE] = {0};
  int i = 0;
  while (list[i].name != NULL) {
    const WeightArray *array = &list[i];
    int block_size = (array->size + WEIGHT_BLOCK_SIZE - 1) / WEIGHT_BLOCK_SIZE * WEIGHT_BLOCK_SIZE;
    if (!gopus_dnn_blob_write_weight_header(out, array, block_size)) {
      return 0;
    }
    if (array->size > 0 && fwrite(array->data, 1, array->size, out) != (size_t)array->size) {
      return 0;
    }
    if (block_size > array->size &&
        fwrite(zeros, 1, (size_t)(block_size - array->size), out) != (size_t)(block_size - array->size)) {
      return 0;
    }
    i++;
  }
  return 1;
}

#endif
