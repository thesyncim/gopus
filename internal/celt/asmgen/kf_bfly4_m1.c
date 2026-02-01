#include <arm_neon.h>

typedef struct {
    float r;
    float i;
} kissCpx;

void kf_bfly4_m1(kissCpx *fout, int n) {
    for (int i = 0; i < n; i++) {
        float32x4_t a = vld1q_f32((const float *)fout);     // r0 i0 r1 i1
        float32x4_t b = vld1q_f32((const float *)(fout + 2)); // r2 i2 r3 i3

        float32x4_t t0 = vaddq_f32(a, b);
        float32x4_t t1 = vsubq_f32(a, b);

        float32x4_t t0swap = vextq_f32(t0, t0, 2);
        float32x4_t aout = vaddq_f32(t0, t0swap);
        float32x4_t cout = vsubq_f32(t0, t0swap);

        float32x2_t s0 = vget_low_f32(t1);
        float32x2_t s1 = vget_high_f32(t1);
        float32x2_t s1swap = vrev64_f32(s1); // [s1.i, s1.r]

        const uint32x2_t signpos = (uint32x2_t){0u, 0x80000000u};
        const uint32x2_t signneg = (uint32x2_t){0x80000000u, 0u};
        float32x2_t rotpos = vreinterpret_f32_u32(
            veor_u32(vreinterpret_u32_f32(s1swap), signpos));
        float32x2_t rotneg = vreinterpret_f32_u32(
            veor_u32(vreinterpret_u32_f32(s1swap), signneg));

        float32x2_t bout = vadd_f32(s0, rotpos);
        float32x2_t dout = vadd_f32(s0, rotneg);

        float32x4_t out0 = vcombine_f32(vget_low_f32(aout), bout);
        float32x4_t out1 = vcombine_f32(vget_low_f32(cout), dout);

        vst1q_f32((float *)fout, out0);
        vst1q_f32((float *)(fout + 2), out1);
        fout += 4;
    }
}
