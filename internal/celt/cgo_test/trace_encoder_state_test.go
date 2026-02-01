// Package cgo traces encoder state to find where gopus and libopus diverge.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceEncoderState traces the encoder state step by step.
func TestTraceEncoderState(t *testing.T) {
	// Create two encoders and compare their states after each operation
	buf1 := make([]byte, 256)
	buf2 := make([]byte, 256)

	enc1 := &rangecoding.Encoder{}
	enc1.Init(buf1)
	enc1.Shrink(159)

	enc2 := &rangecoding.Encoder{}
	enc2.Init(buf2)
	enc2.Shrink(159)

	logState := func(label string) {
		b1 := enc1.RangeBytes()
		b2 := enc2.RangeBytes()
		match := enc1.Range() == enc2.Range() && enc1.Val() == enc2.Val() &&
			enc1.Rem() == enc2.Rem() && enc1.Ext() == enc2.Ext()
		marker := ""
		if !match {
			marker = " <-- DIFFERS"
		}
		t.Logf("%s: tell=%d/%d rng=%08X/%08X val=%08X/%08X rem=%d/%d ext=%d/%d offs=%d/%d%s",
			label,
			enc1.Tell(), enc2.Tell(),
			enc1.Range(), enc2.Range(),
			enc1.Val(), enc2.Val(),
			enc1.Rem(), enc2.Rem(),
			enc1.Ext(), enc2.Ext(),
			b1, b2, marker)
	}

	logState("Init")

	// Encode identical bits
	// silence=0, logp=15
	enc1.EncodeBit(0, 15)
	enc2.EncodeBit(0, 15)
	logState("After silence(0, 15)")

	// postfilter=0, logp=1
	enc1.EncodeBit(0, 1)
	enc2.EncodeBit(0, 1)
	logState("After postfilter(0, 1)")

	// transient=1, logp=3
	enc1.EncodeBit(1, 3)
	enc2.EncodeBit(1, 3)
	logState("After transient(1, 3)")

	// intra=0, logp=3
	enc1.EncodeBit(0, 3)
	enc2.EncodeBit(0, 3)
	logState("After intra(0, 3)")

	// At this point, both encoders should be IDENTICAL
	// If they're not, something is wrong with the range encoder

	t.Log("")
	t.Log("Both encoders encode identical bits and should have identical state.")
	t.Log("If states match, the range encoder is working correctly.")
}

// TestTraceLibopusVsGopusEncode traces the actual encoding from libopus and gopus.
func TestTraceLibopusVsGopusEncode(t *testing.T) {
	_ = math.Pi // Use math package

	// Use libopus tracer for step-by-step encoding
	t.Log("Using libopus encoder tracer...")

	bufSize := 256
	libTracer := NewLibopusEncoderTracer(bufSize)
	defer libTracer.Destroy()

	// Also create a gopus range encoder
	buf := make([]byte, bufSize)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(buf)
	goEnc.Shrink(159)

	logCompare := func(label string, libState ECEncStateTrace) {
		match := libState.Rng == goEnc.Range() &&
			libState.Val == goEnc.Val() &&
			libState.Rem == goEnc.Rem() &&
			libState.Ext == goEnc.Ext()
		marker := ""
		if !match {
			marker = " <-- DIFFERS"
		}
		t.Logf("%s: lib(rng=%08X val=%08X rem=%d ext=%d offs=%d tell=%d) go(rng=%08X val=%08X rem=%d ext=%d offs=%d tell=%d)%s",
			label,
			libState.Rng, libState.Val, libState.Rem, libState.Ext, libState.Offs, libState.Tell,
			goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), goEnc.RangeBytes(), goEnc.Tell(),
			marker)
	}

	// Initial state
	libState := libTracer.GetState()
	logCompare("Init", libState)

	// Encode silence=0, logp=15
	_, libState = libTracer.EncodeBitLogp(0, 15)
	goEnc.EncodeBit(0, 15)
	logCompare("After silence", libState)

	// Encode postfilter=0, logp=1
	_, libState = libTracer.EncodeBitLogp(0, 1)
	goEnc.EncodeBit(0, 1)
	logCompare("After postfilter", libState)

	// Encode transient=1, logp=3
	_, libState = libTracer.EncodeBitLogp(1, 3)
	goEnc.EncodeBit(1, 3)
	logCompare("After transient", libState)

	// Encode intra=0, logp=3
	_, libState = libTracer.EncodeBitLogp(0, 3)
	goEnc.EncodeBit(0, 3)
	logCompare("After intra", libState)

	t.Log("")
	t.Log("If all states match, the issue is in coarse energy or later encoding.")
}
