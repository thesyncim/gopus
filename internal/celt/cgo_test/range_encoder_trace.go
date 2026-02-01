//go:build trace
// +build trace

// Package cgo provides CGO comparison tests.
// This file provides range encoder state tracing wrappers.
package cgo

/*
#cgo CFLAGS: -I${SRCDIR}/../../../tmp_check/opus-1.6.1/include -I${SRCDIR}/../../../tmp_check/opus-1.6.1/celt -I${SRCDIR}/../../../tmp_check/opus-1.6.1 -DHAVE_CONFIG_H
#cgo LDFLAGS: -L${SRCDIR}/../../../tmp_check/opus-1.6.1/.libs -lopus -lm

#include <stdlib.h>
#include <string.h>
#include "entenc.h"
#include "entcode.h"
#include "mfrngcod.h"
#include "laplace.h"

// RangeEncoderState holds encoder state for comparison
typedef struct {
    opus_uint32 rng;
    opus_uint32 val;
    int rem;
    opus_uint32 ext;
    opus_uint32 offs;
    int nbits_total;
    int tell;
} RangeEncoderState;

// Encode a sequence of bits with logp and trace state after each
// states array should have count+1 elements (init + after each bit)
void range_trace_enc_bit_sequence(unsigned char *buf, int size, int *bits, int *logps, int count,
                                   RangeEncoderState *states, unsigned char *out_bytes, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, buf, size);

    // Record initial state
    states[0].rng = enc.rng;
    states[0].val = enc.val;
    states[0].rem = enc.rem;
    states[0].ext = enc.ext;
    states[0].offs = enc.offs;
    states[0].nbits_total = enc.nbits_total;
    states[0].tell = ec_tell(&enc);

    for (int i = 0; i < count; i++) {
        ec_enc_bit_logp(&enc, bits[i], logps[i]);
        states[i+1].rng = enc.rng;
        states[i+1].val = enc.val;
        states[i+1].rem = enc.rem;
        states[i+1].ext = enc.ext;
        states[i+1].offs = enc.offs;
        states[i+1].nbits_total = enc.nbits_total;
        states[i+1].tell = ec_tell(&enc);
    }

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    if (*out_len > 0) {
        memcpy(out_bytes, buf, *out_len);
    }
}

// Encode using ec_encode (fl, fh, ft) and trace
void range_trace_enc_encode_sequence(unsigned char *buf, int size,
                                      unsigned int *fls, unsigned int *fhs, unsigned int *fts,
                                      int count, RangeEncoderState *states,
                                      unsigned char *out_bytes, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, buf, size);

    states[0].rng = enc.rng;
    states[0].val = enc.val;
    states[0].rem = enc.rem;
    states[0].ext = enc.ext;
    states[0].offs = enc.offs;
    states[0].nbits_total = enc.nbits_total;
    states[0].tell = ec_tell(&enc);

    for (int i = 0; i < count; i++) {
        ec_encode(&enc, fls[i], fhs[i], fts[i]);
        states[i+1].rng = enc.rng;
        states[i+1].val = enc.val;
        states[i+1].rem = enc.rem;
        states[i+1].ext = enc.ext;
        states[i+1].offs = enc.offs;
        states[i+1].nbits_total = enc.nbits_total;
        states[i+1].tell = ec_tell(&enc);
    }

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    if (*out_len > 0) {
        memcpy(out_bytes, buf, *out_len);
    }
}

// Encode ICDF sequence and trace
void range_trace_enc_icdf_sequence(unsigned char *buf, int size,
                                    int *symbols, const unsigned char *icdf, int ftb,
                                    int count, RangeEncoderState *states,
                                    unsigned char *out_bytes, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, buf, size);

    states[0].rng = enc.rng;
    states[0].val = enc.val;
    states[0].rem = enc.rem;
    states[0].ext = enc.ext;
    states[0].offs = enc.offs;
    states[0].nbits_total = enc.nbits_total;
    states[0].tell = ec_tell(&enc);

    for (int i = 0; i < count; i++) {
        ec_enc_icdf(&enc, symbols[i], icdf, ftb);
        states[i+1].rng = enc.rng;
        states[i+1].val = enc.val;
        states[i+1].rem = enc.rem;
        states[i+1].ext = enc.ext;
        states[i+1].offs = enc.offs;
        states[i+1].nbits_total = enc.nbits_total;
        states[i+1].tell = ec_tell(&enc);
    }

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    if (*out_len > 0) {
        memcpy(out_bytes, buf, *out_len);
    }
}

// Encode header bits (silence, postfilter, transient, intra) followed by Laplace sequence.
// This matches the exact encoding order used by libopus CELT frame encoding.
// header_bits and header_logps should have 4 elements each (for the 4 header flags).
// laplace_vals, laplace_fs, laplace_decay specify the Laplace encoding parameters.
// states array should have 4 + laplace_count + 1 elements (header states + laplace states + final).
void range_trace_header_plus_laplace(unsigned char *buf, int size,
                                      int *header_bits, int *header_logps,
                                      int *laplace_vals, unsigned int *laplace_fs, int *laplace_decay,
                                      int laplace_count,
                                      RangeEncoderState *states,
                                      int *out_laplace_vals,
                                      unsigned char *out_bytes, int *out_len) {
    ec_enc enc;
    ec_enc_init(&enc, buf, size);

    int state_idx = 0;

    // Record initial state
    states[state_idx].rng = enc.rng;
    states[state_idx].val = enc.val;
    states[state_idx].rem = enc.rem;
    states[state_idx].ext = enc.ext;
    states[state_idx].offs = enc.offs;
    states[state_idx].nbits_total = enc.nbits_total;
    states[state_idx].tell = ec_tell(&enc);
    state_idx++;

    // Encode 4 header bits: silence (logp=15), postfilter (logp=1), transient (logp=3), intra (logp=3)
    for (int i = 0; i < 4; i++) {
        ec_enc_bit_logp(&enc, header_bits[i], header_logps[i]);
        states[state_idx].rng = enc.rng;
        states[state_idx].val = enc.val;
        states[state_idx].rem = enc.rem;
        states[state_idx].ext = enc.ext;
        states[state_idx].offs = enc.offs;
        states[state_idx].nbits_total = enc.nbits_total;
        states[state_idx].tell = ec_tell(&enc);
        state_idx++;
    }

    // Encode Laplace values
    for (int i = 0; i < laplace_count; i++) {
        int val = laplace_vals[i];
        ec_laplace_encode(&enc, &val, laplace_fs[i], laplace_decay[i]);
        out_laplace_vals[i] = val;  // Return possibly-clamped value

        states[state_idx].rng = enc.rng;
        states[state_idx].val = enc.val;
        states[state_idx].rem = enc.rem;
        states[state_idx].ext = enc.ext;
        states[state_idx].offs = enc.offs;
        states[state_idx].nbits_total = enc.nbits_total;
        states[state_idx].tell = ec_tell(&enc);
        state_idx++;
    }

    ec_enc_done(&enc);
    *out_len = enc.offs + enc.end_offs;
    if (*out_len > 0) {
        memcpy(out_bytes, buf, *out_len);
    }
}

*/
import "C"

import (
	"unsafe"
)

// RangeEncoderStateSnapshot holds encoder state for comparison
type RangeEncoderStateSnapshot struct {
	Rng        uint32
	Val        uint32
	Rem        int
	Ext        uint32
	Offs       uint32
	NbitsTotal int
	Tell       int
}

// TraceBitSequence traces libopus encoder state after encoding each bit
func TraceBitSequence(bits []int, logps []int) (states []RangeEncoderStateSnapshot, outBytes []byte) {
	if len(bits) == 0 || len(bits) != len(logps) {
		return nil, nil
	}

	count := len(bits)
	bufSize := 256

	buf := make([]byte, bufSize)
	cBits := make([]C.int, count)
	cLogps := make([]C.int, count)
	for i := range bits {
		cBits[i] = C.int(bits[i])
		cLogps[i] = C.int(logps[i])
	}
	cStates := make([]C.RangeEncoderState, count+1)
	outBuf := make([]byte, bufSize)
	var outLen C.int

	C.range_trace_enc_bit_sequence(
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(bufSize),
		(*C.int)(unsafe.Pointer(&cBits[0])), (*C.int)(unsafe.Pointer(&cLogps[0])), C.int(count),
		(*C.RangeEncoderState)(unsafe.Pointer(&cStates[0])),
		(*C.uchar)(unsafe.Pointer(&outBuf[0])), &outLen,
	)

	states = make([]RangeEncoderStateSnapshot, count+1)
	for i := 0; i <= count; i++ {
		states[i] = RangeEncoderStateSnapshot{
			Rng:        uint32(cStates[i].rng),
			Val:        uint32(cStates[i].val),
			Rem:        int(cStates[i].rem),
			Ext:        uint32(cStates[i].ext),
			Offs:       uint32(cStates[i].offs),
			NbitsTotal: int(cStates[i].nbits_total),
			Tell:       int(cStates[i].tell),
		}
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, outBuf[:int(outLen)])
	return states, outBytes
}

// TraceEncodeSequence traces libopus encoder state after each ec_encode call
func TraceEncodeSequence(fls, fhs, fts []uint32) (states []RangeEncoderStateSnapshot, outBytes []byte) {
	if len(fls) == 0 || len(fls) != len(fhs) || len(fls) != len(fts) {
		return nil, nil
	}

	count := len(fls)
	bufSize := 256

	buf := make([]byte, bufSize)
	cFls := make([]C.uint, count)
	cFhs := make([]C.uint, count)
	cFts := make([]C.uint, count)
	for i := range fls {
		cFls[i] = C.uint(fls[i])
		cFhs[i] = C.uint(fhs[i])
		cFts[i] = C.uint(fts[i])
	}
	cStates := make([]C.RangeEncoderState, count+1)
	outBuf := make([]byte, bufSize)
	var outLen C.int

	C.range_trace_enc_encode_sequence(
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(bufSize),
		(*C.uint)(unsafe.Pointer(&cFls[0])), (*C.uint)(unsafe.Pointer(&cFhs[0])), (*C.uint)(unsafe.Pointer(&cFts[0])),
		C.int(count),
		(*C.RangeEncoderState)(unsafe.Pointer(&cStates[0])),
		(*C.uchar)(unsafe.Pointer(&outBuf[0])), &outLen,
	)

	states = make([]RangeEncoderStateSnapshot, count+1)
	for i := 0; i <= count; i++ {
		states[i] = RangeEncoderStateSnapshot{
			Rng:        uint32(cStates[i].rng),
			Val:        uint32(cStates[i].val),
			Rem:        int(cStates[i].rem),
			Ext:        uint32(cStates[i].ext),
			Offs:       uint32(cStates[i].offs),
			NbitsTotal: int(cStates[i].nbits_total),
			Tell:       int(cStates[i].tell),
		}
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, outBuf[:int(outLen)])
	return states, outBytes
}

// TraceICDFSequence traces libopus encoder state after each ec_enc_icdf call
func TraceICDFSequence(symbols []int, icdf []byte, ftb uint) (states []RangeEncoderStateSnapshot, outBytes []byte) {
	if len(symbols) == 0 || len(icdf) == 0 {
		return nil, nil
	}

	count := len(symbols)
	bufSize := 256

	buf := make([]byte, bufSize)
	cSymbols := make([]C.int, count)
	for i := range symbols {
		cSymbols[i] = C.int(symbols[i])
	}
	cStates := make([]C.RangeEncoderState, count+1)
	outBuf := make([]byte, bufSize)
	var outLen C.int

	C.range_trace_enc_icdf_sequence(
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(bufSize),
		(*C.int)(unsafe.Pointer(&cSymbols[0])),
		(*C.uchar)(unsafe.Pointer(&icdf[0])), C.int(ftb),
		C.int(count),
		(*C.RangeEncoderState)(unsafe.Pointer(&cStates[0])),
		(*C.uchar)(unsafe.Pointer(&outBuf[0])), &outLen,
	)

	states = make([]RangeEncoderStateSnapshot, count+1)
	for i := 0; i <= count; i++ {
		states[i] = RangeEncoderStateSnapshot{
			Rng:        uint32(cStates[i].rng),
			Val:        uint32(cStates[i].val),
			Rem:        int(cStates[i].rem),
			Ext:        uint32(cStates[i].ext),
			Offs:       uint32(cStates[i].offs),
			NbitsTotal: int(cStates[i].nbits_total),
			Tell:       int(cStates[i].tell),
		}
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, outBuf[:int(outLen)])
	return states, outBytes
}

// TraceHeaderPlusLaplace encodes header bits followed by Laplace values using libopus.
// This matches the exact encoding order: silence (logp=15), postfilter (logp=1),
// transient (logp=3), intra (logp=3), then Laplace-encoded qi values.
// headerBits: 4 values (0/1) for [silence, postfilter, transient, intra]
// headerLogps: 4 logp values [15, 1, 3, 3]
// laplaceVals: qi values to encode with Laplace
// laplaceFS: fs parameter for each Laplace encoding
// laplaceDecay: decay parameter for each Laplace encoding
// Returns: states after each operation, output Laplace values, output bytes
func TraceHeaderPlusLaplace(headerBits, headerLogps []int, laplaceVals []int, laplaceFS []int, laplaceDecay []int) (
	states []RangeEncoderStateSnapshot, outLaplaceVals []int, outBytes []byte) {

	if len(headerBits) != 4 || len(headerLogps) != 4 {
		return nil, nil, nil
	}
	if len(laplaceVals) != 0 && (len(laplaceVals) != len(laplaceFS) || len(laplaceVals) != len(laplaceDecay)) {
		return nil, nil, nil
	}

	laplaceCount := len(laplaceVals)
	totalStates := 5 + laplaceCount // 1 initial + 4 header + laplaceCount
	bufSize := 4096

	buf := make([]byte, bufSize)

	cHeaderBits := make([]C.int, 4)
	cHeaderLogps := make([]C.int, 4)
	for i := 0; i < 4; i++ {
		cHeaderBits[i] = C.int(headerBits[i])
		cHeaderLogps[i] = C.int(headerLogps[i])
	}

	var cLaplaceVals []C.int
	var cLaplaceFS []C.uint
	var cLaplaceDecay []C.int
	if laplaceCount > 0 {
		cLaplaceVals = make([]C.int, laplaceCount)
		cLaplaceFS = make([]C.uint, laplaceCount)
		cLaplaceDecay = make([]C.int, laplaceCount)
		for i := range laplaceVals {
			cLaplaceVals[i] = C.int(laplaceVals[i])
			cLaplaceFS[i] = C.uint(laplaceFS[i])
			cLaplaceDecay[i] = C.int(laplaceDecay[i])
		}
	}

	cStates := make([]C.RangeEncoderState, totalStates)
	var cOutLaplaceVals []C.int
	if laplaceCount > 0 {
		cOutLaplaceVals = make([]C.int, laplaceCount)
	}
	outBuf := make([]byte, bufSize)
	var outLen C.int

	var laplaceValsPtr *C.int
	var laplaceFSPtr *C.uint
	var laplaceDecayPtr *C.int
	var outLaplaceValsPtr *C.int
	if laplaceCount > 0 {
		laplaceValsPtr = (*C.int)(unsafe.Pointer(&cLaplaceVals[0]))
		laplaceFSPtr = (*C.uint)(unsafe.Pointer(&cLaplaceFS[0]))
		laplaceDecayPtr = (*C.int)(unsafe.Pointer(&cLaplaceDecay[0]))
		outLaplaceValsPtr = (*C.int)(unsafe.Pointer(&cOutLaplaceVals[0]))
	}

	C.range_trace_header_plus_laplace(
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.int(bufSize),
		(*C.int)(unsafe.Pointer(&cHeaderBits[0])), (*C.int)(unsafe.Pointer(&cHeaderLogps[0])),
		laplaceValsPtr, laplaceFSPtr,
		laplaceDecayPtr,
		C.int(laplaceCount),
		(*C.RangeEncoderState)(unsafe.Pointer(&cStates[0])),
		outLaplaceValsPtr,
		(*C.uchar)(unsafe.Pointer(&outBuf[0])), &outLen,
	)

	states = make([]RangeEncoderStateSnapshot, totalStates)
	for i := 0; i < totalStates; i++ {
		states[i] = RangeEncoderStateSnapshot{
			Rng:        uint32(cStates[i].rng),
			Val:        uint32(cStates[i].val),
			Rem:        int(cStates[i].rem),
			Ext:        uint32(cStates[i].ext),
			Offs:       uint32(cStates[i].offs),
			NbitsTotal: int(cStates[i].nbits_total),
			Tell:       int(cStates[i].tell),
		}
	}

	if laplaceCount > 0 {
		outLaplaceVals = make([]int, laplaceCount)
		for i := 0; i < laplaceCount; i++ {
			outLaplaceVals[i] = int(cOutLaplaceVals[i])
		}
	} else {
		outLaplaceVals = []int{}
	}

	outBytes = make([]byte, int(outLen))
	copy(outBytes, outBuf[:int(outLen)])
	return states, outLaplaceVals, outBytes
}
