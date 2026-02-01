//go:build trace
// +build trace

// Package cgo provides CGO wrappers for TF encoding comparison.
// Agent 22: Debug TF and spread encoding divergence at byte 7
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: -L${SRCDIR}/../../../tmp_check/opus-1.6.1/.libs -lopus -lm

#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <stdio.h>
#include "entenc.h"
#include "entdec.h"
#include "celt.h"
#include "bands.h"

// Trace the TF encoding step
typedef struct {
    int tell_before;       // bit position before TF encoding
    int tell_after;        // bit position after TF encoding
    int tf_changed;        // whether any tf_res was 1
    int tf_select_encoded; // whether tf_select was encoded
    int tf_select_value;   // tf_select value (0 or 1)
    unsigned int rng;      // range after encoding
} TFEncodeTrace;

// tf_select_table is declared in celt.h

// Trace TF encoding
// tf_res: per-band TF resolution (0 or 1 for each of nbBands bands)
// Returns trace info
void trace_tf_encode(
    unsigned char *buf, int buf_size,
    int start, int end, int isTransient, int *tf_res, int LM, int tf_select,
    TFEncodeTrace *trace, unsigned char *out_bytes, int *out_len
) {
    ec_enc enc;
    ec_enc_init(&enc, buf, buf_size);

    opus_uint32 budget = enc.storage * 8;
    trace->tell_before = ec_tell(&enc);

    int logp = isTransient ? 2 : 4;
    int tf_select_rsv = LM > 0 && trace->tell_before + logp + 1 <= (int)budget;
    if (tf_select_rsv) {
        budget--;
    }

    int curr = 0;
    int tf_changed = 0;
    int tell = trace->tell_before;

    for (int i = start; i < end; i++) {
        if (tell + logp <= (int)budget) {
            ec_enc_bit_logp(&enc, tf_res[i] ^ curr, logp);
            tell = ec_tell(&enc);
            curr = tf_res[i];
            tf_changed |= curr;
        }
        logp = isTransient ? 4 : 5;
    }

    trace->tf_changed = tf_changed;
    trace->tf_select_encoded = 0;

    if (tf_select_rsv &&
        tf_select_table[LM][4*isTransient+0+tf_changed] !=
        tf_select_table[LM][4*isTransient+2+tf_changed]) {
        ec_enc_bit_logp(&enc, tf_select, 1);
        trace->tf_select_encoded = 1;
        trace->tf_select_value = tf_select;
    } else {
        trace->tf_select_value = 0;
    }

    trace->tell_after = ec_tell(&enc);
    trace->rng = enc.rng;

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    memcpy(out_bytes, buf, *out_len);
}

// Trace combined TF + spread encoding
typedef struct {
    int tell_before_tf;
    int tell_after_tf;
    int tell_after_spread;
    int spread_value;
    int tf_select;
    unsigned int rng_after_spread;
} TFSpreadTrace;

// spread_icdf is declared in celt.h

void trace_tf_and_spread_encode(
    unsigned char *buf, int buf_size,
    int start, int end, int isTransient, int *tf_res, int LM, int tf_select,
    int spread,
    TFSpreadTrace *trace, unsigned char *out_bytes, int *out_len
) {
    ec_enc enc;
    ec_enc_init(&enc, buf, buf_size);

    trace->tell_before_tf = ec_tell(&enc);

    // TF encoding
    opus_uint32 budget = enc.storage * 8;
    int logp = isTransient ? 2 : 4;
    int tf_select_rsv = LM > 0 && trace->tell_before_tf + logp + 1 <= (int)budget;
    if (tf_select_rsv) {
        budget--;
    }

    int curr = 0;
    int tf_changed = 0;
    int tell = trace->tell_before_tf;

    for (int i = start; i < end; i++) {
        if (tell + logp <= (int)budget) {
            ec_enc_bit_logp(&enc, tf_res[i] ^ curr, logp);
            tell = ec_tell(&enc);
            curr = tf_res[i];
            tf_changed |= curr;
        }
        logp = isTransient ? 4 : 5;
    }

    int actual_tf_select = tf_select;
    if (tf_select_rsv &&
        tf_select_table[LM][4*isTransient+0+tf_changed] !=
        tf_select_table[LM][4*isTransient+2+tf_changed]) {
        ec_enc_bit_logp(&enc, tf_select, 1);
    } else {
        actual_tf_select = 0;
    }

    trace->tell_after_tf = ec_tell(&enc);
    trace->tf_select = actual_tf_select;

    // Spread encoding
    ec_enc_icdf(&enc, spread, spread_icdf, 5);
    trace->tell_after_spread = ec_tell(&enc);
    trace->spread_value = spread;
    trace->rng_after_spread = enc.rng;

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    memcpy(out_bytes, buf, *out_len);
}

*/
import "C"

import (
	"unsafe"
)

// TFEncodeTrace holds trace info for TF encoding
type TFEncodeTrace struct {
	TellBefore      int
	TellAfter       int
	TFChanged       int
	TFSelectEncoded int
	TFSelectValue   int
	Rng             uint32
}

// TraceTFEncode traces TF encoding using libopus
func TraceTFEncode(start, end int, isTransient bool, tfRes []int, lm, tfSelect int) (trace TFEncodeTrace, outBytes []byte) {
	buf := make([]byte, 4096)
	outBuf := make([]byte, 4096)
	var cTrace C.TFEncodeTrace
	var outLen C.int

	isTransientInt := 0
	if isTransient {
		isTransientInt = 1
	}

	cTfRes := make([]C.int, len(tfRes))
	for i, v := range tfRes {
		cTfRes[i] = C.int(v)
	}

	C.trace_tf_encode(
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)),
		C.int(start), C.int(end), C.int(isTransientInt),
		(*C.int)(unsafe.Pointer(&cTfRes[0])), C.int(lm), C.int(tfSelect),
		&cTrace, (*C.uchar)(unsafe.Pointer(&outBuf[0])), &outLen,
	)

	trace = TFEncodeTrace{
		TellBefore:      int(cTrace.tell_before),
		TellAfter:       int(cTrace.tell_after),
		TFChanged:       int(cTrace.tf_changed),
		TFSelectEncoded: int(cTrace.tf_select_encoded),
		TFSelectValue:   int(cTrace.tf_select_value),
		Rng:             uint32(cTrace.rng),
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, outBuf[:int(outLen)])
	return trace, outBytes
}

// TFSpreadTrace holds trace info for TF + spread encoding
type TFSpreadTrace struct {
	TellBeforeTF   int
	TellAfterTF    int
	TellAfterSpread int
	SpreadValue    int
	TFSelect       int
	RngAfterSpread uint32
}

// TraceTFAndSpreadEncode traces TF and spread encoding using libopus
func TraceTFAndSpreadEncode(start, end int, isTransient bool, tfRes []int, lm, tfSelect, spread int) (trace TFSpreadTrace, outBytes []byte) {
	buf := make([]byte, 4096)
	outBuf := make([]byte, 4096)
	var cTrace C.TFSpreadTrace
	var outLen C.int

	isTransientInt := 0
	if isTransient {
		isTransientInt = 1
	}

	cTfRes := make([]C.int, len(tfRes))
	for i, v := range tfRes {
		cTfRes[i] = C.int(v)
	}

	C.trace_tf_and_spread_encode(
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)),
		C.int(start), C.int(end), C.int(isTransientInt),
		(*C.int)(unsafe.Pointer(&cTfRes[0])), C.int(lm), C.int(tfSelect),
		C.int(spread),
		&cTrace, (*C.uchar)(unsafe.Pointer(&outBuf[0])), &outLen,
	)

	trace = TFSpreadTrace{
		TellBeforeTF:   int(cTrace.tell_before_tf),
		TellAfterTF:    int(cTrace.tell_after_tf),
		TellAfterSpread: int(cTrace.tell_after_spread),
		SpreadValue:    int(cTrace.spread_value),
		TFSelect:       int(cTrace.tf_select),
		RngAfterSpread: uint32(cTrace.rng_after_spread),
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, outBuf[:int(outLen)])
	return trace, outBytes
}
