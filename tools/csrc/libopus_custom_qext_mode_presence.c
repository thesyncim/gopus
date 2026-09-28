/* Reports the QEXT side-cache installed by the selected libopus custom mode.
 * GQPI: u32 count, then u32 Fs and u32 frame size per mode.
 * GQPO: u32 count, then u32 short size, cache size, and non-null pointer mask.
 */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "config.h"
#include "opus_custom.h"
#include "modes.h"

#if !defined(ENABLE_QEXT) || !defined(CUSTOM_MODES)
#error "custom QEXT presence oracle requires matching QEXT/custom C features"
#endif

static uint32_t read_u32(void) {
    unsigned char b[4];
    if (fread(b, 1, 4, stdin) != 4) return UINT32_MAX;
    return (uint32_t)b[0] | (uint32_t)b[1]<<8 | (uint32_t)b[2]<<16 | (uint32_t)b[3]<<24;
}

static int write_u32(uint32_t v) {
    unsigned char b[4] = {v, v>>8, v>>16, v>>24};
    return fwrite(b, 1, 4, stdout) == 4;
}

int main(void) {
#ifdef _WIN32
    if (_setmode(_fileno(stdin), _O_BINARY)==-1 ||
        _setmode(_fileno(stdout), _O_BINARY)==-1) return 2;
#endif
    char magic[4];
    if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GQPI", 4)) return 2;
    uint32_t count = read_u32();
    if (count == UINT32_MAX || count > 32) return 2;
    if (fwrite("GQPO", 1, 4, stdout) != 4 || !write_u32(count)) return 3;
    for (uint32_t i = 0; i < count; i++) {
        uint32_t fs = read_u32(), frame = read_u32();
        if (fs == UINT32_MAX || frame == UINT32_MAX) return 2;
        int err = OPUS_OK;
        CELTMode *mode = opus_custom_mode_create((opus_int32)fs, (int)frame, &err);
        if (mode == NULL || err != OPUS_OK) return 4;
        const PulseCache *cache = &mode->qext_cache;
        uint32_t pointers = (cache->index != NULL) | ((cache->bits != NULL)<<1) |
                            ((cache->caps != NULL)<<2);
        if (!write_u32((uint32_t)mode->shortMdctSize) ||
            !write_u32((uint32_t)cache->size) || !write_u32(pointers)) return 3;
        opus_custom_mode_destroy(mode);
    }
    return 0;
}
