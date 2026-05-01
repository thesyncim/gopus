//go:build gopus_unsupported_controls || gopus_dred
// +build gopus_unsupported_controls gopus_dred

package encoder

import (
	"encoding/binary"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/types"
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
	if enc.dred.runtime.latentsFill != 1 {
		t.Fatalf("runtime latentsFill=%d want 1", enc.dred.runtime.latentsFill)
	}

	if err := enc.SetDREDDuration(0); err != nil {
		t.Fatalf("SetDREDDuration(0) error: %v", err)
	}
	if enc.dred.runtime != nil {
		t.Fatal("disabling DRED did not drop the runtime back to dormant state")
	}
}

func TestEncoderDREDEncodingActiveRequiresModelAndDuration(t *testing.T) {
	enc := NewEncoder(16000, 1)
	if enc.dredEncodingActive() {
		t.Fatal("fresh encoder unexpectedly reports active DRED encoding")
	}
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if enc.dredEncodingActive() {
		t.Fatal("DRED encoding active with model loaded but duration unset")
	}
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	if !enc.dredEncodingActive() {
		t.Fatal("DRED encoding inactive after model and duration are armed")
	}
	if err := enc.SetDREDDuration(0); err != nil {
		t.Fatalf("SetDREDDuration(0) error: %v", err)
	}
	if enc.dredEncodingActive() {
		t.Fatal("DRED encoding active after duration cleared")
	}
}

func TestEncoderEncodeKeepsDREDRuntimeDormantUntilDurationArmed(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(40000)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if !enc.DREDModelLoaded() {
		t.Fatal("encoder did not retain loadable DRED models")
	}
	if enc.DREDReady() {
		t.Fatal("DREDReady()=true before SetDREDDuration")
	}

	frame := make([]float64, 960)
	for i := range frame {
		frame[i] = 0.1
	}
	for i := 0; i < 5; i++ {
		if packet, err := enc.Encode(frame, 960); err != nil {
			t.Fatalf("warm Encode error: %v", err)
		} else if len(packet) == 0 {
			t.Fatal("warm Encode returned empty packet")
		}
	}
	if enc.dred == nil {
		t.Fatal("encoder dropped retained DRED model state")
	}
	if enc.dred.runtime != nil {
		t.Fatalf("Encode woke DRED runtime before duration was armed: %+v", enc.dred.runtime)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if packet, err := enc.Encode(frame, 960); err != nil {
			t.Fatalf("Encode error: %v", err)
		} else if len(packet) == 0 {
			t.Fatal("Encode returned empty packet")
		}
	})
	if allocs != 0 {
		t.Fatalf("Encode with dormant DRED allocs/op = %.2f, want 0", allocs)
	}
	if enc.dred.runtime != nil {
		t.Fatalf("allocation guard woke DRED runtime before duration was armed: %+v", enc.dred.runtime)
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
		wantEmitted       int
	}{
		{name: "8k mono", sampleRate: 8000, channels: 1, frameSamplesPerCh: 160, wantEmitted: 1},
		{name: "12k mono", sampleRate: 12000, channels: 1, frameSamplesPerCh: 240, wantEmitted: 1},
		{name: "24k mono", sampleRate: 24000, channels: 1, frameSamplesPerCh: 480, wantEmitted: 1},
		{name: "48k mono", sampleRate: 48000, channels: 1, frameSamplesPerCh: 960, wantEmitted: 1},
		{name: "48k stereo", sampleRate: 48000, channels: 2, frameSamplesPerCh: 960, wantEmitted: 1},
		{name: "48k mono 60ms", sampleRate: 48000, channels: 1, frameSamplesPerCh: 2880, wantEmitted: 3},
		{name: "48k stereo 60ms", sampleRate: 48000, channels: 2, frameSamplesPerCh: 2880, wantEmitted: 3},
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

			if got := enc.processDREDLatents(frame, 0); got != tc.wantEmitted {
				t.Fatalf("processDREDLatents()=%d want %d", got, tc.wantEmitted)
			}
			if enc.dred == nil || enc.dred.runtime == nil {
				t.Fatal("DRED runtime did not materialize on supported sample-rate conversion path")
			}
			if enc.dred.runtime.emitted != tc.wantEmitted {
				t.Fatalf("runtime emitted=%d want %d", enc.dred.runtime.emitted, tc.wantEmitted)
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
	if enc.dred.runtime == nil {
		t.Fatal("10 ms DRED path did not materialize runtime")
	}
	if enc.dred.runtime.latentsFill != 0 {
		t.Fatalf("latentsFill after first 10 ms frame=%d want 0", enc.dred.runtime.latentsFill)
	}
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("second processDREDLatents()=%d want 1 after buffering two 10 ms frames", got)
	}
	if enc.dred.runtime.latentsFill != 1 {
		t.Fatalf("latentsFill after second 10 ms frame=%d want 1", enc.dred.runtime.latentsFill)
	}
}

func TestEncoderProcessDREDLatentsTracksHistoryWindow(t *testing.T) {
	enc := NewEncoder(16000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	frame := make([]float64, 320)
	for i := 0; i < internaldred.NumRedundancyFrames+4; i++ {
		if got := enc.processDREDLatents(frame, 0); got != 1 {
			t.Fatalf("iteration %d processDREDLatents()=%d want 1", i, got)
		}
	}
	if enc.dred.runtime.latentsFill != internaldred.NumRedundancyFrames {
		t.Fatalf("latentsFill=%d want %d", enc.dred.runtime.latentsFill, internaldred.NumRedundancyFrames)
	}
	if enc.dred.runtime.emitted != internaldred.NumRedundancyFrames+4 {
		t.Fatalf("emitted=%d want %d", enc.dred.runtime.emitted, internaldred.NumRedundancyFrames+4)
	}
}

func TestEncoderProcessDREDLatentsTracksOffsets(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	frame := make([]float64, 480)
	if got := enc.processDREDLatents(frame, 0); got != 0 {
		t.Fatalf("first processDREDLatents()=%d want 0", got)
	}
	// Mirrors libopus dred_compute_latents() with extra_delay=0 and the
	// reset DRED_SILK_ENCODER_DELAY fill.
	if enc.dred.runtime.dredOffset != 1 {
		t.Fatalf("dredOffset after first 10 ms frame=%d want 1", enc.dred.runtime.dredOffset)
	}
	if enc.dred.runtime.latentOffset != 0 {
		t.Fatalf("latentOffset after first 10 ms frame=%d want 0", enc.dred.runtime.latentOffset)
	}
	if got := enc.processDREDLatents(frame, 0); got != 1 {
		t.Fatalf("second processDREDLatents()=%d want 1", got)
	}
	if enc.dred.runtime.dredOffset != 5 {
		t.Fatalf("dredOffset after second 10 ms frame=%d want 5", enc.dred.runtime.dredOffset)
	}
	if enc.dred.runtime.latentOffset != 0 {
		t.Fatalf("latentOffset after second 10 ms frame=%d want 0", enc.dred.runtime.latentOffset)
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

func TestEncoderBuildDREDExperimentalPayloadUsesRuntimeHistory(t *testing.T) {
	enc := NewEncoder(16000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	runtime := enc.ensureActiveDREDRuntime()
	if runtime == nil {
		t.Fatal("ensureActiveDREDRuntime() returned nil")
	}
	for i := 0; i < internaldred.StateDim; i++ {
		runtime.stateBuffer[i] = 0.02 * float32((i%7)-3)
	}
	for i := 0; i < 4*internaldred.LatentDim; i++ {
		runtime.latentsBuffer[i] = 0.03 * float32((i%9)-4)
	}
	for i := 0; i < 24; i++ {
		runtime.activity[i] = 1
	}
	runtime.latentsFill = 4
	runtime.dredOffset = 12
	runtime.latentOffset = 0

	var payload [internaldred.MaxDataSize]byte
	n := enc.buildDREDExperimentalPayload(payload[:], 2, 6, 3, 15)
	if n <= internaldred.ExperimentalHeaderBytes {
		t.Fatalf("buildDREDExperimentalPayload()=%d want > %d", n, internaldred.ExperimentalHeaderBytes)
	}
	if payload[0] != 'D' || payload[1] != internaldred.ExperimentalVersion {
		t.Fatalf("payload prefix=%q,%d want D,%d", payload[0], payload[1], internaldred.ExperimentalVersion)
	}
}

func TestEncoderBuildDREDExperimentalPayloadDoesNotAllocate(t *testing.T) {
	enc := NewEncoder(16000, 1)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	runtime := enc.ensureActiveDREDRuntime()
	if runtime == nil {
		t.Fatal("ensureActiveDREDRuntime() returned nil")
	}
	for i := 0; i < internaldred.StateDim; i++ {
		runtime.stateBuffer[i] = 0.015 * float32((i%5)-2)
	}
	for i := 0; i < 4*internaldred.LatentDim; i++ {
		runtime.latentsBuffer[i] = 0.025 * float32((i%11)-5)
	}
	for i := 0; i < 24; i++ {
		runtime.activity[i] = 1
	}
	runtime.latentsFill = 4
	runtime.dredOffset = 12
	runtime.latentOffset = 0

	var payload [internaldred.MaxDataSize]byte
	if n := enc.buildDREDExperimentalPayload(payload[:], 2, 6, 3, 15); n == 0 {
		t.Fatal("warm buildDREDExperimentalPayload() returned 0")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		runtime.lastExtraDREDOffset = 0
		if n := enc.buildDREDExperimentalPayload(payload[:], 2, 6, 3, 15); n == 0 {
			t.Fatal("buildDREDExperimentalPayload() returned 0")
		}
	})
	if allocs != 0 {
		t.Fatalf("buildDREDExperimentalPayload allocs=%v want 0", allocs)
	}
}

func TestMaybeBuildSingleFrameDREDPacketCarriesExtension(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(64000)
	enc.SetPacketLoss(20)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(8); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}

	runtime := enc.ensureActiveDREDRuntime()
	if runtime == nil {
		t.Fatal("ensureActiveDREDRuntime() returned nil")
	}
	for i := 0; i < internaldred.StateDim; i++ {
		runtime.stateBuffer[i] = 0.02 * float32((i%7)-3)
	}
	for i := 0; i < 6*internaldred.LatentDim; i++ {
		runtime.latentsBuffer[i] = 0.03 * float32((i%9)-4)
	}
	for i := 0; i < 48; i++ {
		runtime.activity[i] = 1
	}
	runtime.latentsFill = 6
	runtime.dredOffset = 12
	runtime.latentOffset = 0

	frameData := make([]byte, 40)
	packet, ok, err := enc.maybeBuildSingleFrameDREDPacket(frameData, ModeSILK, types.BandwidthWideband, 960, false)
	if err != nil {
		t.Fatalf("maybeBuildSingleFrameDREDPacket() error: %v", err)
	}
	if !ok {
		t.Fatal("maybeBuildSingleFrameDREDPacket()=false want true")
	}
	if len(packet) == 0 {
		t.Fatal("packet is empty")
	}
	if packet[0]&0x03 != 0x03 {
		t.Fatalf("toc code=%d want 3", packet[0]&0x03)
	}
	if packet[1]&0x40 == 0 {
		t.Fatalf("count byte=0x%02x missing padding flag", packet[1])
	}
	foundDREDHeader := false
	for i := 0; i+2 < len(packet); i++ {
		if packet[i] == byte(internaldred.ExtensionID<<1) && packet[i+1] == 'D' && packet[i+2] == internaldred.ExperimentalVersion {
			foundDREDHeader = true
			break
		}
	}
	if !foundDREDHeader {
		t.Fatalf("packet does not contain DRED experimental extension header: %x", packet)
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
