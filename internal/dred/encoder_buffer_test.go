package dred

import "testing"

func TestEncoderBufferReset(t *testing.T) {
	var b EncoderBuffer
	b.Reset()

	if got := b.InputBufferFill(); got != SilkEncoderDelay {
		t.Fatalf("InputBufferFill()=%d want %d", got, SilkEncoderDelay)
	}
	if got := b.DREDOffset(); got != 0 {
		t.Fatalf("DREDOffset()=%d want 0", got)
	}
	if got := b.LatentOffset(); got != 0 {
		t.Fatalf("LatentOffset()=%d want 0", got)
	}
	for i, v := range b.inputBuffer {
		if v != 0 {
			t.Fatalf("inputBuffer[%d]=%g want 0", i, v)
		}
	}
}

func TestEncoderBufferAppend16kWithoutEmission(t *testing.T) {
	var b EncoderBuffer
	b.Reset()

	pcm := makeRampF32(100)
	if emitted := b.Append16k(pcm, 0, nil); emitted != 0 {
		t.Fatalf("Append16k emitted=%d want 0", emitted)
	}
	if got := b.InputBufferFill(); got != SilkEncoderDelay+len(pcm) {
		t.Fatalf("InputBufferFill()=%d want %d", got, SilkEncoderDelay+len(pcm))
	}
	if got := b.DREDOffset(); got != 1 {
		t.Fatalf("DREDOffset()=%d want 1", got)
	}
	if got := b.LatentOffset(); got != 0 {
		t.Fatalf("LatentOffset()=%d want 0", got)
	}
	for i := 0; i < SilkEncoderDelay; i++ {
		if b.inputBuffer[i] != 0 {
			t.Fatalf("inputBuffer[%d]=%g want 0", i, b.inputBuffer[i])
		}
	}
	for i, want := range pcm {
		if got := b.inputBuffer[SilkEncoderDelay+i]; got != want {
			t.Fatalf("inputBuffer[%d]=%g want %g", SilkEncoderDelay+i, got, want)
		}
	}
}

func TestEncoderBufferAppend16kEmitsOneFrameAndRetainsTail(t *testing.T) {
	var b EncoderBuffer
	b.Reset()

	pcm := makeRampF32(DFrameSize)
	var frame [DFrameSize]float32
	emitted := b.Append16k(pcm, 0, func(got []float32) {
		copy(frame[:], got)
	})
	if emitted != 1 {
		t.Fatalf("Append16k emitted=%d want 1", emitted)
	}
	for i := 0; i < SilkEncoderDelay; i++ {
		if frame[i] != 0 {
			t.Fatalf("frame[%d]=%g want 0", i, frame[i])
		}
	}
	for i := SilkEncoderDelay; i < DFrameSize; i++ {
		want := pcm[i-SilkEncoderDelay]
		if got := frame[i]; got != want {
			t.Fatalf("frame[%d]=%g want %g", i, got, want)
		}
	}
	if got := b.InputBufferFill(); got != SilkEncoderDelay {
		t.Fatalf("InputBufferFill()=%d want %d", got, SilkEncoderDelay)
	}
	for i := 0; i < SilkEncoderDelay; i++ {
		want := pcm[DFrameSize-SilkEncoderDelay+i]
		if got := b.inputBuffer[i]; got != want {
			t.Fatalf("tail[%d]=%g want %g", i, got, want)
		}
	}
	if got := b.DREDOffset(); got != 9 {
		t.Fatalf("DREDOffset()=%d want 9", got)
	}
	if got := b.LatentOffset(); got != 0 {
		t.Fatalf("LatentOffset()=%d want 0", got)
	}
}

func TestEncoderBufferAppend16kEmitsTwoFramesAndAdvancesLatentOffset(t *testing.T) {
	var b EncoderBuffer
	b.Reset()

	pcm := makeRampF32(2 * DFrameSize)
	var frames [2][DFrameSize]float32
	index := 0
	emitted := b.Append16k(pcm, 0, func(got []float32) {
		copy(frames[index][:], got)
		index++
	})
	if emitted != 2 {
		t.Fatalf("Append16k emitted=%d want 2", emitted)
	}
	for i := 0; i < SilkEncoderDelay; i++ {
		want := pcm[DFrameSize-SilkEncoderDelay+i]
		if got := frames[1][i]; got != want {
			t.Fatalf("second frame prefix[%d]=%g want %g", i, got, want)
		}
	}
	for i := SilkEncoderDelay; i < DFrameSize; i++ {
		want := pcm[DFrameSize+(i-SilkEncoderDelay)]
		if got := frames[1][i]; got != want {
			t.Fatalf("second frame[%d]=%g want %g", i, got, want)
		}
	}
	if got := b.InputBufferFill(); got != SilkEncoderDelay {
		t.Fatalf("InputBufferFill()=%d want %d", got, SilkEncoderDelay)
	}
	if got := b.DREDOffset(); got != 9 {
		t.Fatalf("DREDOffset()=%d want 9", got)
	}
	if got := b.LatentOffset(); got != 1 {
		t.Fatalf("LatentOffset()=%d want 1", got)
	}
}

func makeRampF32(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(i + 1)
	}
	return out
}
