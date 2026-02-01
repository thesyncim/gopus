#include <arm_neon.h>

typedef struct {
    float r;
    float i;
} kissCpx;

void kf_bfly3_m1(kissCpx *fout, const kissCpx *tw, int fstride, int n, int mm) {
    if (n <= 0) {
        return;
    }

    float32x2_t epi = vdup_n_f32(tw[fstride].i);
    float32x2_t half = vdup_n_f32(0.5f);
    float32x2_t sign = (float32x2_t){1.0f, -1.0f};

    for (int i = 0; i < n; i++) {
        kissCpx *f = fout + i * mm;
        float32x2_t f0 = vld1_f32((const float *)&f[0]);
        float32x2_t f1 = vld1_f32((const float *)&f[1]);
        float32x2_t f2 = vld1_f32((const float *)&f[2]);

        float32x2_t scratch3 = vadd_f32(f1, f2);
        float32x2_t scratch0 = vsub_f32(f1, f2);

        float32x2_t f1out = vsub_f32(f0, vmul_f32(scratch3, half));
        scratch0 = vmul_f32(scratch0, epi);
        f0 = vadd_f32(f0, scratch3);

        float32x2_t scratch0swap = vrev64_f32(scratch0);
        float32x2_t scratch0rot = vmul_f32(scratch0swap, sign);
        float32x2_t f2out = vadd_f32(f1out, scratch0rot);
        f1out = vsub_f32(f1out, scratch0rot);

        vst1_f32((float *)&f[0], f0);
        vst1_f32((float *)&f[1], f1out);
        vst1_f32((float *)&f[2], f2out);
    }
}
