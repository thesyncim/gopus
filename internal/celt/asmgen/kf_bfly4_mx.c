#include <arm_neon.h>

typedef struct {
    float r;
    float i;
} kissCpx;

static inline float32x2_t cmul2(float32x2_t a, float32x2_t b) {
    float32x2_t br = vdup_lane_f32(b, 0);
    float32x2_t bi = vdup_lane_f32(b, 1);
    float32x2_t a_sw = vrev64_f32(a);
    float32x2_t t0 = vmul_f32(a, br);
    float32x2_t t1 = vmul_f32(a_sw, bi);
    float32x2_t sign = (float32x2_t){-1.0f, 1.0f};
    t1 = vmul_f32(t1, sign);
    return vadd_f32(t0, t1);
}

static inline float32x4_t cmul2x(float32x4_t a, float32x4_t b) {
    float32x2_t bl = vget_low_f32(b);
    float32x2_t bh = vget_high_f32(b);
    float32x4_t br = vcombine_f32(vdup_lane_f32(bl, 0), vdup_lane_f32(bh, 0));
    float32x4_t bi = vcombine_f32(vdup_lane_f32(bl, 1), vdup_lane_f32(bh, 1));
    float32x4_t a_sw = vrev64q_f32(a);
    float32x4_t t0 = vmulq_f32(a, br);
    float32x4_t t1 = vmulq_f32(a_sw, bi);
    float32x4_t sign = (float32x4_t){-1.0f, 1.0f, -1.0f, 1.0f};
    t1 = vmulq_f32(t1, sign);
    return vaddq_f32(t0, t1);
}

static inline float32x2_t cadd2(float32x2_t a, float32x2_t b) { return vadd_f32(a, b); }
static inline float32x2_t csub2(float32x2_t a, float32x2_t b) { return vsub_f32(a, b); }

void kf_bfly4_mx(kissCpx *fout, const kissCpx *tw, int m, int n, int fstride, int mm) {
    kissCpx *foutBeg = fout;

    if (fstride == 1) {
        float32x4_t sign4 = (float32x4_t){-1.0f, 1.0f, -1.0f, 1.0f};
        for (int i = 0; i < n; i++) {
            kissCpx *f = foutBeg + i * mm;
            int tw1 = 0;
            int tw2 = 0;
            int tw3 = 0;
            int j = 0;
            for (; j + 1 < m; j += 2) {
                float32x4_t f0 = vld1q_f32((const float *)&f[0]);
                float32x4_t f1 = vld1q_f32((const float *)&f[m]);
                float32x4_t f2 = vld1q_f32((const float *)&f[2 * m]);
                float32x4_t f3 = vld1q_f32((const float *)&f[3 * m]);

                float32x4_t tw1v = vld1q_f32((const float *)&tw[tw1]);
                float32x2_t tw2a = vld1_f32((const float *)&tw[tw2]);
                float32x2_t tw2b = vld1_f32((const float *)&tw[tw2 + 2]);
                float32x4_t tw2v = vcombine_f32(tw2a, tw2b);
                float32x2_t tw3a = vld1_f32((const float *)&tw[tw3]);
                float32x2_t tw3b = vld1_f32((const float *)&tw[tw3 + 3]);
                float32x4_t tw3v = vcombine_f32(tw3a, tw3b);

                float32x4_t scratch0 = cmul2x(f1, tw1v);
                float32x4_t scratch1 = cmul2x(f2, tw2v);
                float32x4_t scratch2 = cmul2x(f3, tw3v);

                float32x4_t scratch5 = vsubq_f32(f0, scratch1);
                f0 = vaddq_f32(f0, scratch1);
                float32x4_t scratch3 = vaddq_f32(scratch0, scratch2);
                float32x4_t scratch4 = vsubq_f32(scratch0, scratch2);

                float32x4_t f2out = vsubq_f32(f0, scratch3);
                f0 = vaddq_f32(f0, scratch3);

                float32x4_t sc4swap = vrev64q_f32(scratch4);
                float32x4_t sc4i = vmulq_f32(sc4swap, sign4);
                float32x4_t fmout = vsubq_f32(scratch5, sc4i);
                float32x4_t f3out = vaddq_f32(scratch5, sc4i);

                vst1q_f32((float *)&f[0], f0);
                vst1q_f32((float *)&f[m], fmout);
                vst1q_f32((float *)&f[2 * m], f2out);
                vst1q_f32((float *)&f[3 * m], f3out);

                tw1 += 2;
                tw2 += 4;
                tw3 += 6;
                f += 2;
            }
            for (; j < m; j++) {
                float32x2_t f0 = vld1_f32((const float *)&f[0]);
                float32x2_t f1 = vld1_f32((const float *)&f[m]);
                float32x2_t f2 = vld1_f32((const float *)&f[2 * m]);
                float32x2_t f3 = vld1_f32((const float *)&f[3 * m]);

                float32x2_t tw1v = vld1_f32((const float *)&tw[tw1]);
                float32x2_t tw2v = vld1_f32((const float *)&tw[tw2]);
                float32x2_t tw3v = vld1_f32((const float *)&tw[tw3]);

                float32x2_t scratch0 = cmul2(f1, tw1v);
                float32x2_t scratch1 = cmul2(f2, tw2v);
                float32x2_t scratch2 = cmul2(f3, tw3v);

                float32x2_t scratch5 = csub2(f0, scratch1);
                f0 = cadd2(f0, scratch1);
                float32x2_t scratch3 = cadd2(scratch0, scratch2);
                float32x2_t scratch4 = csub2(scratch0, scratch2);

                float32x2_t f2out = csub2(f0, scratch3);
                f0 = cadd2(f0, scratch3);

                float32x2_t sc4swap = vrev64_f32(scratch4);
                float32x2_t sign2 = (float32x2_t){-1.0f, 1.0f};
                float32x2_t sc4i = vmul_f32(sc4swap, sign2);
                float32x2_t fmout = csub2(scratch5, sc4i);
                float32x2_t f3out = cadd2(scratch5, sc4i);

                vst1_f32((float *)&f[0], f0);
                vst1_f32((float *)&f[m], fmout);
                vst1_f32((float *)&f[2 * m], f2out);
                vst1_f32((float *)&f[3 * m], f3out);

                tw1 += 1;
                tw2 += 2;
                tw3 += 3;
                f++;
            }
        }
        return;
    }

    for (int i = 0; i < n; i++) {
        kissCpx *f = foutBeg + i * mm;
        int tw1 = 0;
        int tw2 = 0;
        int tw3 = 0;
        for (int j = 0; j < m; j++) {
            float32x2_t f0 = vld1_f32((const float *)&f[0]);
            float32x2_t f1 = vld1_f32((const float *)&f[m]);
            float32x2_t f2 = vld1_f32((const float *)&f[2 * m]);
            float32x2_t f3 = vld1_f32((const float *)&f[3 * m]);

            float32x2_t tw1v = vld1_f32((const float *)&tw[tw1]);
            float32x2_t tw2v = vld1_f32((const float *)&tw[tw2]);
            float32x2_t tw3v = vld1_f32((const float *)&tw[tw3]);

            float32x2_t scratch0 = cmul2(f1, tw1v);
            float32x2_t scratch1 = cmul2(f2, tw2v);
            float32x2_t scratch2 = cmul2(f3, tw3v);

            float32x2_t scratch5 = csub2(f0, scratch1);
            f0 = cadd2(f0, scratch1);
            float32x2_t scratch3 = cadd2(scratch0, scratch2);
            float32x2_t scratch4 = csub2(scratch0, scratch2);

            float32x2_t f2out = csub2(f0, scratch3);
            f0 = cadd2(f0, scratch3);

            float32x2_t sc4swap = vrev64_f32(scratch4);
            float32x2_t sign2 = (float32x2_t){-1.0f, 1.0f};
            float32x2_t sc4i = vmul_f32(sc4swap, sign2);
            float32x2_t fmout = csub2(scratch5, sc4i);
            float32x2_t f3out = cadd2(scratch5, sc4i);

            vst1_f32((float *)&f[0], f0);
            vst1_f32((float *)&f[m], fmout);
            vst1_f32((float *)&f[2 * m], f2out);
            vst1_f32((float *)&f[3 * m], f3out);

            tw1 += fstride;
            tw2 += fstride * 2;
            tw3 += fstride * 3;
            f++;
        }
    }
}
