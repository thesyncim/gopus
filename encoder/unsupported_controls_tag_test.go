//go:build gopus_unsupported_controls || gopus_dred
// +build gopus_unsupported_controls gopus_dred

package encoder_test

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
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

func appendTestBlobRecordWithPayload(dst []byte, name string, typ int32, payload []byte) []byte {
	const headerSize = 64
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	copy(out[headerSize:], payload)
	return append(dst, out...)
}

func encodeInt32s(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}

func appendLinearLayerSpecBlob(dst []byte, spec lpcnetplc.LinearLayerSpec) []byte {
	if spec.Bias != "" {
		dst = appendTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Subias != "" {
		dst = appendTestBlobRecord(dst, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Scale != "" {
		dst = appendTestBlobRecord(dst, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Weights != "" {
		dst = appendTestBlobRecord(dst, spec.Weights, dnnblob.TypeInt8, spec.NbInputs*spec.NbOutputs)
		return dst
	}
	if spec.FloatWeights != "" {
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*spec.NbInputs*spec.NbOutputs)
	}
	return dst
}

func appendConv2DLayerSpecBlob(dst []byte, spec lpcnetplc.Conv2DLayerSpec) []byte {
	if spec.Bias != "" {
		dst = appendTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.OutChannels)
	}
	if spec.FloatWeights != "" {
		size := 4 * spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, size)
	}
	return dst
}

func appendRDOVAEEncoderLayerRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Subias != "" {
		dst = appendTestBlobRecord(dst, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = appendTestBlobRecordWithPayload(dst, spec.WeightsIdx, dnnblob.TypeInt, encodeInt32s(idx))
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendTestBlobRecord(dst, spec.Weights, dnnblob.TypeInt8, size)
		dst = appendTestBlobRecord(dst, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*size)
	}
	return dst
}

func makeManifestOnlyDREDEncoderTestBlob(t *testing.T) *dnnblob.Blob {
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

func makeLoadableDREDEncoderTestBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	var raw []byte
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		raw = appendLinearLayerSpecBlob(raw, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		raw = appendConv2DLayerSpecBlob(raw, spec)
	}
	for _, spec := range rdovae.EncoderLayerSpecs() {
		raw = appendRDOVAEEncoderLayerRecords(raw, spec)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone(loadable): %v", err)
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

func TestDREDRuntimeBuildExposesEncoderControls(t *testing.T) {
	methods := exportedMethodNames(encoder.NewEncoder(48000, 1))
	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "SetDREDDuration"} {
		if _, ok := methods[name]; !ok {
			t.Fatalf("DRED runtime build does not expose %s", name)
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

	enc.SetDNNBlob(makeManifestOnlyDREDEncoderTestBlob(t))
	if !enc.DNNBlobLoaded() {
		t.Fatal("encoder did not retain manifest-only blob")
	}
	if enc.DREDModelLoaded() || enc.DREDReady() {
		t.Fatal("encoder treated manifest-only blob as loadable DRED model")
	}

	enc.SetDNNBlob(makeLoadableDREDEncoderTestBlob(t))
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
