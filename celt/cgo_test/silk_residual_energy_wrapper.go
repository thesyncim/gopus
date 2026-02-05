//go:build cgo_libopus
// +build cgo_libopus

package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk -I${SRCDIR}/../../tmp_check/opus-1.6.1/silk/float -DHAVE_CONFIG_H
#cgo LDFLAGS: ${SRCDIR}/../../tmp_check/opus-1.6.1/.libs/libopus.a -lm

#include <string.h>
#include "main_FLP.h"

static int test_silk_residual_energy_flp(
    const float *x,
    const float *a0,
    const float *a1,
    const float *gains,
    int subfr_length,
    int nb_subfr,
    int lpc_order,
    float *out_nrgs
) {
    silk_float a[2][MAX_LPC_ORDER];
    int i;
    if (!x || !a0 || !a1 || !gains || !out_nrgs) {
        return -1;
    }
    if (lpc_order <= 0 || lpc_order > MAX_LPC_ORDER) {
        return -2;
    }
    if (nb_subfr <= 0 || nb_subfr > MAX_NB_SUBFR) {
        return -3;
    }

    memset(a, 0, sizeof(a));
    for (i = 0; i < lpc_order; i++) {
        a[0][i] = a0[i];
        a[1][i] = a1[i];
    }
    silk_residual_energy_FLP(out_nrgs, x, a, gains, subfr_length, nb_subfr, lpc_order);
    return 0;
}
*/
import "C"

import "unsafe"

// SilkResidualEnergyFLP calls libopus silk_residual_energy_FLP.
func SilkResidualEnergyFLP(x, a0, a1, gains []float32, subfrLength, nbSubfr, lpcOrder int) ([]float32, bool) {
	if len(x) == 0 || len(a0) < lpcOrder || len(a1) < lpcOrder || len(gains) < nbSubfr || nbSubfr <= 0 {
		return nil, false
	}
	out := make([]float32, nbSubfr)
	ret := C.test_silk_residual_energy_flp(
		(*C.float)(unsafe.Pointer(&x[0])),
		(*C.float)(unsafe.Pointer(&a0[0])),
		(*C.float)(unsafe.Pointer(&a1[0])),
		(*C.float)(unsafe.Pointer(&gains[0])),
		C.int(subfrLength),
		C.int(nbSubfr),
		C.int(lpcOrder),
		(*C.float)(unsafe.Pointer(&out[0])),
	)
	return out, ret == 0
}
