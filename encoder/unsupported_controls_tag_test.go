//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package encoder_test

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
)

func appendTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	return append(dst, out...)
}

func makeDREDEncoderTestBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	var raw []byte
	for _, name := range dnnblob.RequiredEncoderControlRecordNames() {
		raw = appendTestBlobRecord(raw, name, 0, 4)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	return blob
}

func exportedMethodNames(v any) map[string]struct{} {
	t := reflect.TypeOf(v)
	names := make(map[string]struct{}, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names[t.Method(i).Name] = struct{}{}
	}
	return names
}

func TestUnsupportedControlsBuildExposesQuarantinedControls(t *testing.T) {
	methods := exportedMethodNames(encoder.NewEncoder(48000, 1))
	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "SetDREDDuration"} {
		if _, ok := methods[name]; !ok {
			t.Fatalf("unsupported-controls build does not expose %s", name)
		}
	}
}

func TestEncoderDREDDuration(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)

	for _, duration := range []int{0, 1, internaldred.MaxFrames} {
		if err := enc.SetDREDDuration(duration); err != nil {
			t.Fatalf("SetDREDDuration(%d) error: %v", duration, err)
		}
		if got := enc.DREDDuration(); got != duration {
			t.Fatalf("DREDDuration()=%d want %d", got, duration)
		}
	}

	for _, duration := range []int{-1, internaldred.MaxFrames + 1} {
		if err := enc.SetDREDDuration(duration); err != encoder.ErrInvalidDREDDuration {
			t.Fatalf("SetDREDDuration(%d) error=%v want=%v", duration, err, encoder.ErrInvalidDREDDuration)
		}
	}
}

func TestEncoderResetClearsDREDDuration(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	enc.Reset()

	if got := enc.DREDDuration(); got != 0 {
		t.Fatalf("DREDDuration() after Reset=%d want 0", got)
	}
}

func TestEncoderDREDReadyRequiresModelAndDuration(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	if enc.DNNBlobLoaded() || enc.DREDModelLoaded() || enc.DREDReady() {
		t.Fatal("fresh encoder unexpectedly reports DRED readiness")
	}

	enc.SetDNNBlob(makeDREDEncoderTestBlob(t))
	if !enc.DNNBlobLoaded() || !enc.DREDModelLoaded() {
		t.Fatal("encoder did not retain DRED-capable blob")
	}
	if enc.DREDReady() {
		t.Fatal("encoder DREDReady()=true without dred duration")
	}

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration(4) error: %v", err)
	}
	if !enc.DREDReady() {
		t.Fatal("encoder DREDReady()=false after model+duration")
	}

	enc.Reset()
	if !enc.DREDModelLoaded() {
		t.Fatal("encoder lost DRED model across Reset")
	}
	if enc.DREDReady() {
		t.Fatal("encoder DREDReady()=true after Reset with duration cleared")
	}

	enc.SetDNNBlob(nil)
	if enc.DNNBlobLoaded() || enc.DREDModelLoaded() || enc.DREDReady() {
		t.Fatal("encoder retained DRED readiness after clearing blob")
	}
}
