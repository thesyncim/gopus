//go:build !gopus_unsupported_controls && !gopus_dred && !gopus_qext
// +build !gopus_unsupported_controls,!gopus_dred,!gopus_qext

package multistream

import (
	"encoding/binary"
	"reflect"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func exportedMethodNames(v any) map[string]struct{} {
	t := reflect.TypeOf(v)
	names := make(map[string]struct{}, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names[t.Method(i).Name] = struct{}{}
	}
	return names
}

func TestDefaultBuildQuarantinesUnsupportedControls(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}

	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "QEXT", "SetDREDDuration", "SetQEXT"} {
		if _, ok := exportedMethodNames(enc)[name]; ok {
			t.Fatalf("default build unexpectedly exposes encoder %s", name)
		}
	}
	for _, name := range []string{"DREDModelLoaded", "OSCEBWEModelLoaded", "OSCEModelsLoaded"} {
		if _, ok := exportedMethodNames(dec)[name]; ok {
			t.Fatalf("default build unexpectedly exposes decoder %s", name)
		}
	}
}

func TestNewDecoderLeavesDREDSidecarDormant(t *testing.T) {
	for _, channels := range []int{1, 2, 3, 6} {
		t.Run("channels="+strconv.Itoa(channels), func(t *testing.T) {
			dec, err := NewDecoderDefault(48000, channels)
			if err != nil {
				t.Fatalf("NewDecoderDefault error: %v", err)
			}
			if dec.dred != nil {
				t.Fatalf("default build allocated multistream DRED sidecar after construction: %+v", dec.dred)
			}

			dec.SetDNNBlob(nil)
			if dec.dred != nil {
				t.Fatalf("default SetDNNBlob(nil) allocated multistream DRED sidecar: %+v", dec.dred)
			}

			// Reset must not stir the dormant sidecar awake on default builds.
			dec.Reset()
			if dec.dred != nil {
				t.Fatalf("Reset awakened dormant DRED sidecar: %+v", dec.dred)
			}

			// A second SetDNNBlob(nil) / Reset cycle must remain dormant.
			dec.SetDNNBlob(nil)
			dec.Reset()
			if dec.dred != nil {
				t.Fatalf("repeated nil-blob/Reset cycle awakened DRED sidecar: %+v", dec.dred)
			}
		})
	}
}

// appendDefaultTestBlobRecord appends a libopus-style DNN weights record with a
// zero-filled payload of the requested size. Mirrors the framing used by the
// gopus_dred-tagged helpers but stays available in default builds so the
// dormancy contract can be exercised without the DRED runtime.
func appendDefaultTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
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

// makeValidDefaultDecoderTestBlob constructs a minimal but loader-valid decoder
// DNN blob containing every record the default-build libopus decoder loader
// expects. It is sufficient to flip the decoder's model-loaded flags during
// SetDNNBlob without requiring any DRED runtime machinery.
func makeValidDefaultDecoderTestBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	var raw []byte
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		raw = appendDefaultTestBlobRecord(raw, name, dnnblob.TypeFloat, 4)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	return blob
}

// TestDefaultBuildMultistreamDecoderRealBlobDormant guards the contract that a
// fully validated decoder DNN blob still keeps the DRED sidecar nil on default
// builds, both at SetDNNBlob time and across real Decode / PLC / Reset cycles.
func TestDefaultBuildMultistreamDecoderRealBlobDormant(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)

	blob := makeValidDefaultDecoderTestBlob(t)

	dec, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}

	dec.SetDNNBlob(blob)
	if dec.dnnBlob == nil {
		t.Fatal("SetDNNBlob did not retain the validated multistream decoder blob")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("SetDNNBlob did not flip model-loaded flags for a validated decoder blob")
	}
	if dec.dred != nil {
		t.Fatalf("default build SetDNNBlob(real blob) allocated DRED sidecar: %+v", dec.dred)
	}

	// Encode a real multistream packet so we can drive Decode end-to-end.
	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	pcm := generateTestSignal(channels, frameSize, sampleRate, 997)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	out, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(out) != frameSize*channels {
		t.Fatalf("Decode produced %d samples, want %d", len(out), frameSize*channels)
	}
	if dec.dred != nil {
		t.Fatalf("default build Decode after real-blob SetDNNBlob allocated DRED sidecar: %+v", dec.dred)
	}

	// Drive PLC and confirm the dormant sidecar stays nil.
	if _, err := dec.Decode(nil, frameSize); err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if dec.dred != nil {
		t.Fatalf("default build Decode(nil) after real-blob SetDNNBlob allocated DRED sidecar: %+v", dec.dred)
	}

	// Reset must not stir the dormant sidecar, and the retained blob/flags
	// must survive Reset just like the top-level decoder contract.
	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("Reset cleared retained multistream decoder blob")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("Reset cleared multistream decoder model-loaded flags")
	}
	if dec.dred != nil {
		t.Fatalf("default build Reset after real-blob SetDNNBlob allocated DRED sidecar: %+v", dec.dred)
	}

	// A second Decode/Decode(nil) cycle after Reset must remain dormant.
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode after Reset error: %v", err)
	}
	if _, err := dec.Decode(nil, frameSize); err != nil {
		t.Fatalf("Decode(nil) after Reset error: %v", err)
	}
	if dec.dred != nil {
		t.Fatalf("default build Decode cycle after Reset awakened DRED sidecar: %+v", dec.dred)
	}

	// Allocation guard: Decode(nil) on the armed decoder must not allocate any
	// DRED-flavoured state. We compare against a baseline decoder with no blob
	// installed so that benign per-call allocations don't fail the test.
	baseline, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("baseline NewDecoderDefault error: %v", err)
	}
	if _, err := baseline.Decode(packet, frameSize); err != nil {
		t.Fatalf("baseline Decode error: %v", err)
	}
	baselineAllocs := testing.AllocsPerRun(50, func() {
		if _, err := baseline.Decode(nil, frameSize); err != nil {
			t.Fatalf("baseline Decode(nil): %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(50, func() {
		if _, err := dec.Decode(nil, frameSize); err != nil {
			t.Fatalf("armed Decode(nil): %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("default build Decode(nil) after SetDNNBlob allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
	if dec.dred != nil {
		t.Fatalf("default build allocation guard awakened DRED sidecar: %+v", dec.dred)
	}
}

// TestDefaultBuildMultistreamDecoderDecodeAllocGuard pins the steady-state
// allocation contract for the multistream decoder Decode hot path on default
// builds: an armed decoder (real DNN blob installed) must not allocate more
// per Decode call than an identical baseline decoder with no blob installed.
// This mirrors the encoder-side guard in
// TestDefaultBuildEncoderDNNBlobKeepsDREDDormant and complements the dormancy
// pin in TestDefaultBuildMultistreamDecoderRealBlobDormant.
func TestDefaultBuildMultistreamDecoderDecodeAllocGuard(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)

	blob := makeValidDefaultDecoderTestBlob(t)

	baseline, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("baseline NewDecoderDefault error: %v", err)
	}
	armed, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("armed NewDecoderDefault error: %v", err)
	}
	armed.SetDNNBlob(blob)
	if armed.dred != nil {
		t.Fatalf("default build SetDNNBlob(real blob) allocated multistream DRED sidecar: %+v", armed.dred)
	}

	// Build a real stereo CELT packet via the multistream encoder default
	// build so Decode exercises the same hot path real callers hit.
	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	pcm := generateTestSignal(channels, frameSize, sampleRate, 997)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Prime both decoders so any one-shot allocations are paid before
	// measurement.
	if _, err := baseline.Decode(packet, frameSize); err != nil {
		t.Fatalf("baseline Decode warmup error: %v", err)
	}
	if _, err := armed.Decode(packet, frameSize); err != nil {
		t.Fatalf("armed Decode warmup error: %v", err)
	}

	baselineAllocs := testing.AllocsPerRun(50, func() {
		if _, err := baseline.Decode(packet, frameSize); err != nil {
			t.Fatalf("baseline Decode: %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(50, func() {
		if _, err := armed.Decode(packet, frameSize); err != nil {
			t.Fatalf("armed Decode: %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("default build Decode after SetDNNBlob allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
	if armed.dred != nil {
		t.Fatalf("default build Decode alloc guard awakened multistream DRED sidecar: %+v", armed.dred)
	}
}

// childDREDNil reports whether the unexported `dred` field of the given
// *encoder.Encoder is nil. Reflection is required because `dred` is
// unexported in the encoder package and lives outside the multistream
// package's accessible surface.
func childDREDNil(v any) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	field := rv.FieldByName("dred")
	if !field.IsValid() {
		return true
	}
	return field.IsNil()
}

// TestDefaultBuildMultistreamEncoderDNNBlobKeepsAllocsFlat mirrors the
// top-level TestDefaultBuildEncoderDNNBlobKeepsDREDDormant contract for the
// multistream encoder: SetDNNBlob on a default build must not allocate any
// hidden state that leaks into the steady-state Encode hot path. Constructing
// a real encoder DNN blob requires DRED-tagged helpers that aren't reachable
// from default-build tests, so we exercise the SetDNNBlob(nil) path which is
// the only operation guaranteed to be safe in default builds. The point is
// that the SetDNNBlob call itself must not arm any DRED-flavoured machinery
// that would inflate the per-Encode allocation count.
func TestDefaultBuildMultistreamEncoderDNNBlobKeepsAllocsFlat(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)

	baseline, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("baseline NewEncoderDefault error: %v", err)
	}
	armed, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("armed NewEncoderDefault error: %v", err)
	}

	// SetDNNBlob(nil) is the only multistream encoder blob path safe to call
	// without DRED runtime helpers. It must remain a no-op for default builds.
	armed.SetDNNBlob(nil)
	if armed.DNNBlobLoaded() {
		t.Fatal("SetDNNBlob(nil) left multistream encoder reporting DNN blob loaded")
	}
	for i, child := range armed.encoders {
		if !childDREDNil(child) {
			t.Fatalf("SetDNNBlob(nil) woke child DRED runtime for stream %d", i)
		}
	}

	pcm := generateTestSignal(channels, frameSize, sampleRate, 997)

	// Warm both encoders so steady-state Encode allocations are measured.
	if _, err := baseline.Encode(pcm, frameSize); err != nil {
		t.Fatalf("baseline Encode warmup error: %v", err)
	}
	if _, err := armed.Encode(pcm, frameSize); err != nil {
		t.Fatalf("armed Encode warmup error: %v", err)
	}

	baselineAllocs := testing.AllocsPerRun(50, func() {
		if _, err := baseline.Encode(pcm, frameSize); err != nil {
			t.Fatalf("baseline Encode: %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(50, func() {
		if _, err := armed.Encode(pcm, frameSize); err != nil {
			t.Fatalf("armed Encode: %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("default build multistream Encode after SetDNNBlob allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
	for i, child := range armed.encoders {
		if !childDREDNil(child) {
			t.Fatalf("Encode loop awakened child DRED runtime for stream %d", i)
		}
	}
}

func TestNewEncoderLeavesDREDDormant(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	if got, want := len(enc.encoders), 2; got != want {
		t.Fatalf("expected %d child stream encoders for 3-channel fanout, got %d", want, got)
	}
	for i, child := range enc.encoders {
		if !childDREDNil(child) {
			t.Fatalf("default build allocated child DRED runtime for stream %d before SetDNNBlob", i)
		}
		if child.DNNBlobLoaded() {
			t.Fatalf("default build child stream %d unexpectedly reports DNN blob loaded before SetDNNBlob", i)
		}
	}

	// Nil-blob smoke: must not panic and must not wake any child DRED runtime.
	enc.SetDNNBlob(nil)
	if enc.DNNBlobLoaded() {
		t.Fatal("SetDNNBlob(nil) left multistream encoder reporting DNN blob loaded")
	}
	for i, child := range enc.encoders {
		if !childDREDNil(child) {
			t.Fatalf("SetDNNBlob(nil) woke child DRED runtime for stream %d", i)
		}
		if child.DNNBlobLoaded() {
			t.Fatalf("SetDNNBlob(nil) left child stream %d reporting DNN blob loaded", i)
		}
	}
}
