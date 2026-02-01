#include <arm_neon.h>

typedef struct {
    float r;
    float i;
} kissCpx;

void kf_bfly5_m1(kissCpx *fout, const kissCpx *tw, int fstride, int n, int mm) {
    if (n <= 0) {
        return;
    }

    kissCpx ya = tw[fstride];
    kissCpx yb = tw[2 * fstride];

    float32x2_t ya_r = vdup_n_f32(ya.r);
    float32x2_t yb_r = vdup_n_f32(yb.r);
    float32x2_t ya_i = vdup_n_f32(ya.i);
    float32x2_t yb_i = vdup_n_f32(yb.i);
    float32x2_t sign = (float32x2_t){1.0f, -1.0f};

    for (int i = 0; i < n; i++) {
        kissCpx *f = fout + i * mm;
        float32x2_t f0 = vld1_f32((const float *)&f[0]);
        float32x2_t f1 = vld1_f32((const float *)&f[1]);
        float32x2_t f2 = vld1_f32((const float *)&f[2]);
        float32x2_t f3 = vld1_f32((const float *)&f[3]);
        float32x2_t f4 = vld1_f32((const float *)&f[4]);

        float32x2_t scratch7 = vadd_f32(f1, f4);
        float32x2_t scratch10 = vsub_f32(f1, f4);
        float32x2_t scratch8 = vadd_f32(f2, f3);
        float32x2_t scratch9 = vsub_f32(f2, f3);

        float32x2_t f0orig = f0;
        f0 = vadd_f32(f0, vadd_f32(scratch7, scratch8));

        float32x2_t scratch5 = vmla_f32(vmul_f32(scratch7, ya_r), scratch8, yb_r);
        scratch5 = vadd_f32(scratch5, f0orig);

        float32x2_t s10swap = vrev64_f32(scratch10);
        float32x2_t s9swap = vrev64_f32(scratch9);
        float32x2_t scratch6 = vmla_f32(vmul_f32(s10swap, ya_i), s9swap, yb_i);
        scratch6 = vmul_f32(scratch6, sign);

        float32x2_t f1out = vsub_f32(scratch5, scratch6);
        float32x2_t f4out = vadd_f32(scratch5, scratch6);

        float32x2_t scratch11 = vmla_f32(vmul_f32(scratch7, yb_r), scratch8, ya_r);
        scratch11 = vadd_f32(scratch11, f0orig);

        float32x2_t scratch12 = vmla_f32(vmul_f32(s9swap, ya_i), vneg_f32(s10swap), yb_i);
        scratch12 = vmul_f32(scratch12, sign);

        float32x2_t f2out = vadd_f32(scratch11, scratch12);
        float32x2_t f3out = vsub_f32(scratch11, scratch12);

        vst1_f32((float *)&f[0], f0);
        vst1_f32((float *)&f[1], f1out);
        vst1_f32((float *)&f[2], f2out);
        vst1_f32((float *)&f[3], f3out);
        vst1_f32((float *)&f[4], f4out);
    }
}
