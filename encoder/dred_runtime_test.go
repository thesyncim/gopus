//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package encoder

import (
	"encoding/binary"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestEncoderDREDRuntimeStaysDormantUntilReady(t *testing.T) {
	enc := NewEncoder(16000, 1)
	if enc.dred != nil {
		t.Fatal("fresh encoder unexpectedly has DRED extras")
	}
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if !enc.DREDModelLoaded() {
		t.Fatal("encoder did not retain loadable DRED models")
	}
	if enc.dred == nil || enc.dred.runtime != nil {
		t.Fatal("encoder should keep DRED runtime dormant until DRED is armed")
	}

	frame := make([]float64, 320)
	if got := enc.processDREDLatents(frame, 0); got != 0 {
		t.Fatalf("processDREDLatents()=%d want 0 while duration is disabled", got)
	}
	if enc.dred.runtime != nil {
		t.Fatal("processDREDLatents created runtime while DRED was disabled")
	}

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("processDREDLatents()=%d want 1 once DRED is armed", got)
	}
	if enc.dred.runtime == nil {
		t.Fatal("armed DRED path did not materialize runtime")
	}
	if enc.dred.runtime.emitted != 1 {
		t.Fatalf("runtime emitted=%d want 1", enc.dred.runtime.emitted)
	}

	if err := enc.SetDREDDuration(0); err != nil {
		t.Fatalf("SetDREDDuration(0) error: %v", err)
	}
	if enc.dred.runtime != nil {
		t.Fatal("disabling DRED did not drop the runtime back to dormant state")
	}
}

func TestEncoderProcessDREDLatentsDoesNotAllocate(t *testing.T) {
	enc := NewEncoder(16000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	frame := make([]float64, 320)
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("warm processDREDLatents()=%d want 1", got)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		if got := enc.processDREDLatents(frame, 0); got != 1 {
			t.Fatalf("processDREDLatents()=%d want 1", got)
		}
	})
	if allocs != 0 {
		t.Fatalf("processDREDLatents allocs=%v want 0", allocs)
	}
	for i, v := range enc.dred.runtime.latestLatents {
		if v != 0 {
			t.Fatalf("latestLatents[%d]=%v want 0 for zeroed test model", i, v)
		}
	}
	for i, v := range enc.dred.runtime.latestState {
		if v != 0 {
			t.Fatalf("latestState[%d]=%v want 0 for zeroed test model", i, v)
		}
	}
}

func TestEncoderProcessDREDLatentsDownmixesStereo16k(t *testing.T) {
	enc := NewEncoder(16000, 2)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	frame := make([]float64, 640)
	for i := 0; i < len(frame); i += 2 {
		frame[i] = 0.25
		frame[i+1] = -0.25
	}
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("processDREDLatents()=%d want 1", got)
	}
	if enc.dred.runtime == nil {
		t.Fatal("stereo 16 kHz DRED path did not materialize runtime")
	}
	for i, v := range enc.dred.runtime.latestLatents {
		if v != 0 {
			t.Fatalf("latestLatents[%d]=%v want 0 for zeroed test model", i, v)
		}
	}
}

func TestEncoderProcessDREDLatentsSupportsRateConversion(t *testing.T) {
	tests := []struct {
		name              string
		sampleRate        int
		channels          int
		frameSamplesPerCh int
	}{
		{name: "8k mono", sampleRate: 8000, channels: 1, frameSamplesPerCh: 160},
		{name: "12k mono", sampleRate: 12000, channels: 1, frameSamplesPerCh: 240},
		{name: "24k mono", sampleRate: 24000, channels: 1, frameSamplesPerCh: 480},
		{name: "48k mono", sampleRate: 48000, channels: 1, frameSamplesPerCh: 960},
		{name: "48k stereo", sampleRate: 48000, channels: 2, frameSamplesPerCh: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.sampleRate, tc.channels)
			enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
			if err := enc.SetDREDDuration(4); err != nil {
				t.Fatalf("SetDREDDuration error: %v", err)
			}

			frame := make([]float64, tc.frameSamplesPerCh*tc.channels)
			if tc.channels == 1 {
				for i := range frame {
					frame[i] = 0.1
				}
			} else {
				for i := 0; i < len(frame); i += 2 {
					frame[i] = 0.25
					frame[i+1] = -0.25
				}
			}

			if got := enc.processDREDLatents(frame, 0); got != 1 {
				t.Fatalf("processDREDLatents()=%d want 1", got)
			}
			if enc.dred == nil || enc.dred.runtime == nil {
				t.Fatal("DRED runtime did not materialize on supported sample-rate conversion path")
			}
			if enc.dred.runtime.emitted != 1 {
				t.Fatalf("runtime emitted=%d want 1", enc.dred.runtime.emitted)
			}
		})
	}
}

func TestEncoderProcessDREDLatentsBuffers48k10msFrames(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	frame := make([]float64, 480)
	for i := range frame {
		frame[i] = 0.05
	}
	if got := enc.processDREDLatents(frame, 0); got != 0 {
		t.Fatalf("first processDREDLatents()=%d want 0 for 10 ms buffered input", got)
	}
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("second processDREDLatents()=%d want 1 after buffering two 10 ms frames", got)
	}
}

func TestEncoderProcessDREDLatentsDoesNotAllocate48k(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	frame := make([]float64, 960)
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("warm processDREDLatents()=%d want 1", got)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		if got := enc.processDREDLatents(frame, 0); got != 1 {
			t.Fatalf("processDREDLatents()=%d want 1", got)
		}
	})
	if allocs != 0 {
		t.Fatalf("processDREDLatents allocs=%v want 0", allocs)
	}
}

func mustMakeLoadableDREDEncoderBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	var raw []byte
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		raw = appendPitchLinearLayerSpecBlob(raw, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		raw = appendPitchConv2DLayerSpecBlob(raw, spec)
	}
	for _, spec := range rdovae.EncoderLayerSpecs() {
		raw = appendRDOVAEEncoderLayerSpecBlob(raw, spec)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	return blob
}

func appendPitchLinearLayerSpecBlob(dst []byte, spec lpcnetplc.LinearLayerSpec) []byte {
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
	}
	if spec.FloatWeights != "" {
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*spec.NbInputs*spec.NbOutputs)
	}
	return dst
}

func appendPitchConv2DLayerSpecBlob(dst []byte, spec lpcnetplc.Conv2DLayerSpec) []byte {
	if spec.Bias != "" {
		dst = appendTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.OutChannels)
	}
	if spec.FloatWeights != "" {
		size := 4 * spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, size)
	}
	return dst
}

func appendRDOVAEEncoderLayerSpecBlob(dst []byte, spec rdovae.LinearLayerSpec) []byte {
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
	}
	if spec.Scale != "" {
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
