#include <arm_neon.h>

void imdct_prerotate_f32(float *out, const double *spec, const float *trig, int n2, int n4) {
    if (n4 <= 0) {
        return;
    }

    int i = 0;
    for (; i + 1 < n4; i += 2) {
        int front = 2 * i;
        int back = n2 - 1 - 2 * i;

        float32x2_t x1 = vdup_n_f32((float)spec[front]);
        x1 = vset_lane_f32((float)spec[front + 2], x1, 1);

        float32x2_t x2 = vdup_n_f32((float)spec[back]);
        x2 = vset_lane_f32((float)spec[back - 2], x2, 1);

        float32x2_t t0 = vld1_f32(trig + i);
        float32x2_t t1 = vld1_f32(trig + n4 + i);

        float32x2_t yr = vadd_f32(vmul_f32(x2, t0), vmul_f32(x1, t1));
        float32x2_t yi = vsub_f32(vmul_f32(x1, t0), vmul_f32(x2, t1));

        float32x2x2_t zip = vzip_f32(yi, yr);
        float32x4_t outv = vcombine_f32(zip.val[0], zip.val[1]);
        vst1q_f32(out + 2 * i, outv);
    }

    for (; i < n4; i++) {
        float x1 = (float)spec[2 * i];
        float x2 = (float)spec[n2 - 1 - 2 * i];
        float t0 = trig[i];
        float t1 = trig[n4 + i];
        float yr = x2 * t0 + x1 * t1;
        float yi = x1 * t0 - x2 * t1;
        out[2 * i] = yi;
        out[2 * i + 1] = yr;
    }
}
