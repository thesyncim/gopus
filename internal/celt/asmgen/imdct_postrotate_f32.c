#include <arm_neon.h>

void imdct_postrotate_f32(float *buf, const float *trig, int n2, int n4) {
    int limit = (n4 + 1) >> 1;
    if (limit <= 0) {
        return;
    }

    int yp0 = 0;
    int yp1 = n2 - 2;
    int i = 0;

    for (; i + 1 < limit; i += 2) {
        float32x4_t vlow = vld1q_f32(buf + yp0);
        float32x4_t vhi = vld1q_f32(buf + yp1 - 2);

        float32x2_t low_im = vget_low_f32(vuzp1q_f32(vlow, vlow));
        float32x2_t low_re = vget_low_f32(vuzp2q_f32(vlow, vlow));

        float32x2_t high_im = vget_low_f32(vuzp1q_f32(vhi, vhi));
        float32x2_t high_re = vget_low_f32(vuzp2q_f32(vhi, vhi));
        high_im = vrev64_f32(high_im);
        high_re = vrev64_f32(high_re);

        float32x2_t t0 = vld1_f32(trig + i);
        float32x2_t t1 = vld1_f32(trig + n4 + i);

        float32x2_t t0b = vld1_f32(trig + (n4 - i - 2));
        float32x2_t t1b = vld1_f32(trig + (n2 - i - 2));
        t0b = vrev64_f32(t0b);
        t1b = vrev64_f32(t1b);

        float32x2_t low_yr = vadd_f32(vmul_f32(low_re, t0), vmul_f32(low_im, t1));
        float32x2_t low_yi = vsub_f32(vmul_f32(low_re, t1), vmul_f32(low_im, t0));

        float32x2_t high_yr = vadd_f32(vmul_f32(high_re, t0b), vmul_f32(high_im, t1b));
        float32x2_t high_yi = vsub_f32(vmul_f32(high_re, t1b), vmul_f32(high_im, t0b));

        float32x2x2_t zip0 = vzip_f32(low_yr, high_yi);
        float32x4_t out_lo = vcombine_f32(zip0.val[0], zip0.val[1]);

        float32x2x2_t zip1 = vzip_f32(high_yr, low_yi);
        float32x4_t out_hi = vcombine_f32(zip1.val[1], zip1.val[0]);

        vst1q_f32(buf + yp0, out_lo);
        vst1q_f32(buf + yp1 - 2, out_hi);

        yp0 += 4;
        yp1 -= 4;
    }

    for (; i < limit; i++) {
        float re = buf[yp0 + 1];
        float im = buf[yp0];
        float t0 = trig[i];
        float t1 = trig[n4 + i];
        float yr = re * t0 + im * t1;
        float yi = re * t1 - im * t0;
        float re2 = buf[yp1 + 1];
        float im2 = buf[yp1];
        buf[yp0] = yr;
        buf[yp1 + 1] = yi;

        t0 = trig[n4 - i - 1];
        t1 = trig[n2 - i - 1];
        yr = re2 * t0 + im2 * t1;
        yi = re2 * t1 - im2 * t0;
        buf[yp1] = yr;
        buf[yp0 + 1] = yi;
        yp0 += 2;
        yp1 -= 2;
    }
}
