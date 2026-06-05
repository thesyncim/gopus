package ogg

import (
	"bytes"
	"errors"
	"testing"
)

// TestWriterClose_Idempotent verifies that calling Close twice is a no-op on the
// second call: the EOS page is written exactly once and the second Close returns
// nil without emitting another page.
func TestWriterClose_Idempotent(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}
	if err := w.WritePacket(make([]byte, 50), 960); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	pagesAfterFirst := w.PageCount()
	lenAfterFirst := buf.Len()

	if err := w.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
	if got := w.PageCount(); got != pagesAfterFirst {
		t.Errorf("second Close wrote a page: PageCount=%d want %d", got, pagesAfterFirst)
	}
	if got := buf.Len(); got != lenAfterFirst {
		t.Errorf("second Close emitted %d extra bytes", got-lenAfterFirst)
	}
}

// TestWriterWritePacketAfterClose verifies that WritePacket after Close returns
// ErrUnexpectedEOS specifically (not just any error).
func TestWriterWritePacketAfterClose(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	err = w.WritePacket(make([]byte, 50), 960)
	if !errors.Is(err, ErrUnexpectedEOS) {
		t.Fatalf("WritePacket after Close: got %v, want ErrUnexpectedEOS", err)
	}
}

// TestWriterClose_WritePageError verifies that a write failure while emitting the
// terminal EOS page surfaces as an error from Close and leaves the writer not
// marked closed (so the error is observable rather than silently swallowed).
//
// NewWriterWithConfig emits the OpusHead and OpusTags pages during construction
// (writes 1 and 2), so the EOS page is the third Write.
func TestWriterClose_WritePageError(t *testing.T) {
	sw := &shortWriteWriter{shortAt: 3, shortBytes: 0}
	w, err := NewWriterWithConfig(sw, WriterConfig{
		SampleRate:    48000,
		Channels:      1,
		MappingFamily: MappingFamilyRTP,
		StreamCount:   1,
	})
	if err != nil {
		t.Fatalf("NewWriterWithConfig failed: %v", err)
	}

	if err := w.Close(); err == nil {
		t.Fatal("Close succeeded despite a short write on the EOS page")
	}
}
