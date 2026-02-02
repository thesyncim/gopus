//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO wrappers for spread decision comparison.
// Agent 22: Debug spread decision divergence at byte 7
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: -L${SRCDIR}/../../tmp_check/opus-1.6.1/.libs -lopus -lm

#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <stdio.h>
#include "entenc.h"
#include "entdec.h"
#include "celt.h"
#include "bands.h"

// Get the spread_icdf table from libopus
void get_spread_icdf(unsigned char *out) {
    // From celt.h: static const unsigned char spread_icdf[4] = {25, 23, 2, 0};
    out[0] = 25;
    out[1] = 23;
    out[2] = 2;
    out[3] = 0;
}

// Encode spread decision and return bytes
int encode_spread_decision(int spread, unsigned char *out_buf, int max_size, int *out_len) {
    static const unsigned char spread_icdf[4] = {25, 23, 2, 0};
    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);
    ec_enc_icdf(&enc, spread, spread_icdf, 5);
    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    return 0;
}

// Decode spread decision from bytes
int decode_spread_decision(const unsigned char *data, int data_len) {
    static const unsigned char spread_icdf[4] = {25, 23, 2, 0};
    ec_dec dec;
    ec_dec_init(&dec, (unsigned char*)data, data_len);
    return ec_dec_icdf(&dec, spread_icdf, 5);
}

// Encode a sequence: TF + spread + dynalloc bits + trim
// This helps trace the exact encoding order
typedef struct {
    int tell_before_tf;
    int tell_after_tf;
    int tell_after_spread;
    int tell_after_dynalloc;
    int tell_after_trim;
    unsigned int rng_after_spread;
    int spread_value;
} FrameEncodeTrace;

// Encode header + coarse energy + TF + spread
// Returns the range encoder state after spread encoding
void trace_frame_encode_to_spread(
    unsigned char *out_buf, int max_size,
    int silence, int postfilter, int transient, int intra,
    int spread, int *tf_res, int tf_select, int nbBands, int LM,
    int alloc_trim,
    FrameEncodeTrace *trace, int *out_len
) {
    static const unsigned char spread_icdf[4] = {25, 23, 2, 0};
    static const unsigned char trim_icdf[11] = {126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0};

    ec_enc enc;
    ec_enc_init(&enc, out_buf, max_size);

    // 1. Silence flag
    if (ec_tell(&enc) == 1) {
        ec_enc_bit_logp(&enc, silence, 15);
    }

    // 2. Postfilter
    ec_enc_bit_logp(&enc, postfilter, 1);

    // 3. Transient (if LM > 0)
    if (LM > 0) {
        ec_enc_bit_logp(&enc, transient, 3);
    }

    // 4. Intra
    ec_enc_bit_logp(&enc, intra, 3);

    // Note: Coarse energy would be encoded here, but we skip it for now

    trace->tell_before_tf = ec_tell(&enc);

    // TF encoding (simplified - just encode tf_select bit)
    // In reality, TF encoding is more complex
    if (LM > 0) {
        ec_enc_bit_logp(&enc, tf_select, 1);
    }

    trace->tell_after_tf = ec_tell(&enc);

    // Spread encoding
    ec_enc_icdf(&enc, spread, spread_icdf, 5);
    trace->tell_after_spread = ec_tell(&enc);
    trace->rng_after_spread = enc.rng;
    trace->spread_value = spread;

    // Dynalloc (simplified - just one 0-bit per band with logp=6)
    for (int i = 0; i < nbBands; i++) {
        ec_enc_bit_logp(&enc, 0, 6);
    }
    trace->tell_after_dynalloc = ec_tell(&enc);

    // Trim
    ec_enc_icdf(&enc, alloc_trim, trim_icdf, 7);
    trace->tell_after_trim = ec_tell(&enc);

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
}

*/
import "C"

import (
	"unsafe"
)

// GetSpreadICDF returns the libopus spread_icdf table
func GetSpreadICDF() []byte {
	icdf := make([]byte, 4)
	C.get_spread_icdf((*C.uchar)(unsafe.Pointer(&icdf[0])))
	return icdf
}

// EncodeSpreadDecision encodes a spread decision using libopus
func EncodeSpreadDecision(spread int) []byte {
	buf := make([]byte, 256)
	var length C.int
	C.encode_spread_decision(C.int(spread),
		(*C.uchar)(unsafe.Pointer(&buf[0])), 256, &length)
	result := make([]byte, int(length))
	copy(result, buf[:int(length)])
	return result
}

// DecodeSpreadDecision decodes a spread value from bytes using libopus
func DecodeSpreadDecision(data []byte) int {
	if len(data) == 0 {
		return 2 // SPREAD_NORMAL default
	}
	return int(C.decode_spread_decision(
		(*C.uchar)(unsafe.Pointer(&data[0])), C.int(len(data))))
}

// FrameEncodeTrace holds trace info for frame encoding
type FrameEncodeTrace struct {
	TellBeforeTF     int
	TellAfterTF      int
	TellAfterSpread  int
	TellAfterDynalloc int
	TellAfterTrim    int
	RngAfterSpread   uint32
	SpreadValue      int
}

// TraceFrameEncodeToSpread traces the encoding up to and including spread
func TraceFrameEncodeToSpread(
	silence, postfilter, transient, intra int,
	spread int, tfRes []int, tfSelect int, nbBands, lm int,
	allocTrim int,
) (trace FrameEncodeTrace, outBytes []byte) {
	buf := make([]byte, 4096)
	var cTrace C.FrameEncodeTrace
	var outLen C.int

	C.trace_frame_encode_to_spread(
		(*C.uchar)(unsafe.Pointer(&buf[0])), 4096,
		C.int(silence), C.int(postfilter), C.int(transient), C.int(intra),
		C.int(spread), nil, C.int(tfSelect), C.int(nbBands), C.int(lm),
		C.int(allocTrim),
		&cTrace, &outLen,
	)

	trace = FrameEncodeTrace{
		TellBeforeTF:     int(cTrace.tell_before_tf),
		TellAfterTF:      int(cTrace.tell_after_tf),
		TellAfterSpread:  int(cTrace.tell_after_spread),
		TellAfterDynalloc: int(cTrace.tell_after_dynalloc),
		TellAfterTrim:    int(cTrace.tell_after_trim),
		RngAfterSpread:   uint32(cTrace.rng_after_spread),
		SpreadValue:      int(cTrace.spread_value),
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, buf[:int(outLen)])
	return trace, outBytes
}
