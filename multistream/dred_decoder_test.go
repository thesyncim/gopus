//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package multistream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/types"
)

func dredSidecarLengthsForTest(dec *Decoder) (cache, data, plc int) {
	if dec == nil || dec.dred == nil {
		return 0, 0, 0
	}
	return len(dec.dred.dredCache), len(dec.dred.dredData), len(dec.dred.dredPLC)
}

func TestNewDecoderLeavesDREDSidecarDormant(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	if cache, data, plc := dredSidecarLengthsForTest(dec); cache != 0 || data != 0 || plc != 0 {
		t.Fatalf("dormant multistream DRED sidecar unexpectedly allocated: cache=%d data=%d plc=%d", cache, data, plc)
	}

	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))
	if cache, data, plc := dredSidecarLengthsForTest(dec); cache != 0 || data != 0 || plc != 0 {
		t.Fatalf("main decoder SetDNNBlob eagerly allocated multistream DRED sidecar: cache=%d data=%d plc=%d", cache, data, plc)
	}

	setStandaloneDREDDecoderBlobForTest(t, dec)
	if cache, data, plc := dredSidecarLengthsForTest(dec); cache != 0 || data != 0 || plc != 0 {
		t.Fatalf("standalone DRED arm eagerly allocated multistream sidecar: cache=%d data=%d plc=%d", cache, data, plc)
	}

	dec.setDREDDecoderBlob(nil)
	if cache, data, plc := dredSidecarLengthsForTest(dec); cache != 0 || data != 0 || plc != 0 {
		t.Fatalf("standalone DRED clear left multistream sidecar allocated: cache=%d data=%d plc=%d", cache, data, plc)
	}
}
func appendDNNBlobRecordForTest(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[4:8], 0)
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	return append(dst, out...)
}

func appendDREDDecoderRecord(dst []byte, name string, typ int32, payload []byte) []byte {
	const headerSize = 64
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[4:8], 0)
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	copy(out[headerSize:], payload)
	return append(dst, out...)
}

func makeDecoderBlobForDREDTest(t *testing.T, withDRED bool) *dnnblob.Blob {
	t.Helper()

	var raw []byte
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		raw = appendDNNBlobRecordForTest(raw, name, 0, 4)
	}
	if withDRED {
		for _, spec := range rdovae.DecoderLayerSpecs() {
			raw = appendDREDDecoderLayerTestRecords(raw, spec)
		}
	}

	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	return blob
}

func makeLoadableDecoderDREDControlTestBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()

	var raw []byte
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		raw = appendLinearLayerSpecBlob(raw, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		raw = appendConv2DLayerSpecBlob(raw, spec)
	}
	for _, spec := range lpcnetplc.ModelLayerSpecs() {
		raw = appendLinearLayerSpecBlob(raw, spec)
	}
	for _, spec := range lpcnetplc.FARGANModelLayerSpecs() {
		raw = appendLinearLayerSpecBlob(raw, spec)
	}
	for _, spec := range rdovae.DecoderLayerSpecs() {
		raw = appendDREDDecoderLayerTestRecords(raw, spec)
	}

	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone(loadable decoder DRED control): %v", err)
	}
	if err := blob.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(loadable decoder DRED control): %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(loadable decoder DRED control): %v", err)
	}
	return blob
}

func appendDREDDecoderLayerTestRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendDREDDecoderRecord(dst, spec.Bias, dnnblob.TypeFloat, make([]byte, 4*spec.NbOutputs))
	}
	if spec.Subias != "" {
		dst = appendDREDDecoderRecord(dst, spec.Subias, dnnblob.TypeFloat, make([]byte, 4*spec.NbOutputs))
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = appendDREDDecoderRecord(dst, spec.WeightsIdx, dnnblob.TypeInt, encodeTestInt32Payload(idx))
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendDREDDecoderRecord(dst, spec.Weights, dnnblob.TypeInt8, make([]byte, size))
		dst = appendDREDDecoderRecord(dst, spec.Scale, dnnblob.TypeFloat, make([]byte, 4*spec.NbOutputs))
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendDREDDecoderRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, make([]byte, 4*size))
	}
	return dst
}

func encodeTestInt32Payload(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}

func setStandaloneDREDDecoderBlobForTest(t *testing.T, dec *Decoder) {
	t.Helper()

	blob := makeDecoderBlobForDREDTest(t, true)
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
}

func addDREDExtensionToOpusPacketForTest(t *testing.T, packet []byte, body []byte) []byte {
	t.Helper()

	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		t.Fatalf("parseOpusPacket error: %v", err)
	}
	padding := append(append([]byte(nil), parsed.padding...), byte(internaldred.ExtensionID<<1), 'D', internaldred.ExperimentalVersion)
	padding = append(padding, body...)
	dst := make([]byte, len(packet)+len(padding)+8)
	n, err := buildOpusPacketFromFramesAndPadding(parsed.tocBase, parsed.frames, padding, false, dst)
	if err != nil {
		t.Fatalf("buildOpusPacketFromFramesAndPadding error: %v", err)
	}
	return dst[:n]
}

func addDREDExtensionToOpusPacketFrameForTest(t *testing.T, packet []byte, frame int, body []byte) []byte {
	t.Helper()

	if frame == 0 {
		return addDREDExtensionToOpusPacketForTest(t, packet, body)
	}
	if frame != 1 {
		t.Fatalf("unsupported test frame index %d", frame)
	}
	if len(packet) < 2 {
		t.Fatal("packet too short for frame extension test")
	}

	dst := make([]byte, len(packet)*2+16)
	padding := append([]byte{0x02, byte(internaldred.ExtensionID << 1), 'D', internaldred.ExperimentalVersion}, body...)
	n, err := buildOpusPacketFromFramesAndPadding(packet[0]&0xFC, [][]byte{packet[1:], packet[1:]}, padding, false, dst)
	if err != nil {
		t.Fatalf("buildOpusPacketFromFramesAndPadding(frame=%d) error: %v", frame, err)
	}
	return dst[:n]
}

func makeExperimentalDREDPayloadBodyForTest(t *testing.T, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	rawOffset := 16 - dredOffset + dredFrameOffset
	if rawOffset < 0 || rawOffset >= 32 {
		t.Fatalf("rawOffset=%d out of range for dredOffset=%d frameOffset=%d", rawOffset, dredOffset, dredFrameOffset)
	}

	var enc rangecoding.Encoder
	enc.Init(make([]byte, internaldred.MinBytes))
	enc.EncodeUniform(6, 16)
	enc.EncodeUniform(3, 8)
	enc.EncodeUniform(0, 2)
	enc.EncodeUniform(uint32(rawOffset), 32)
	enc.Shrink(internaldred.MinBytes)
	return enc.Done()
}

func rebuildMultistreamPacketForTest(t *testing.T, packets [][]byte) []byte {
	t.Helper()

	out := make([]byte, 0, 256)
	for i := 0; i < len(packets)-1; i++ {
		sd, err := makeSelfDelimitedPacket(packets[i])
		if err != nil {
			t.Fatalf("makeSelfDelimitedPacket(stream=%d) error: %v", i, err)
		}
		out = append(out, sd...)
	}
	out = append(out, packets[len(packets)-1]...)
	return out
}

func makeMultistreamPacketWithDREDForTest(t *testing.T, channels, targetStream int, body []byte) []byte {
	return makeMultistreamPacketWithDREDFrameForTest(t, channels, targetStream, 0, body)
}

func makeCELTMultistreamPacketWithDREDForTest(t *testing.T, channels, targetStream int, body []byte) []byte {
	t.Helper()

	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, 960, 48000, 997), 960)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	packets, err := parseMultistreamPacket(packet, enc.Streams())
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}
	if targetStream < 0 || targetStream >= len(packets) {
		t.Fatalf("targetStream=%d out of range for %d packets", targetStream, len(packets))
	}
	if toc := parseStreamTOC(packets[targetStream][0]); toc.mode != streamModeCELT {
		t.Fatalf("target stream mode=%d want CELT", toc.mode)
	}
	packets[targetStream] = addDREDExtensionToOpusPacketFrameForTest(t, packets[targetStream], 0, body)
	return rebuildMultistreamPacketForTest(t, packets)
}

func makeModeMultistreamPacketWithDREDForTest(t *testing.T, channels, targetStream int, mode internalenc.Mode, bandwidth types.Bandwidth, bitrate int, body []byte) []byte {
	t.Helper()

	streams, coupledStreams, _, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping error: %v", err)
	}
	if targetStream < 0 || targetStream >= streams {
		t.Fatalf("targetStream=%d out of range for %d streams", targetStream, streams)
	}

	packets := make([][]byte, streams)
	for stream := 0; stream < streams; stream++ {
		streamChannels := streamChannels(stream, coupledStreams)
		streamMode := internalenc.ModeCELT
		streamBandwidth := types.BandwidthFullband
		streamBitrate := 128000
		if stream == targetStream {
			streamMode = mode
			streamBandwidth = bandwidth
			streamBitrate = bitrate
		}
		streamEnc := internalenc.NewEncoder(48000, streamChannels)
		streamEnc.SetMode(streamMode)
		streamEnc.SetBandwidth(streamBandwidth)
		streamEnc.SetBitrate(streamBitrate)
		if streamChannels == 2 {
			streamEnc.SetForceChannels(2)
		}
		packet, err := streamEnc.Encode(generateTestSignal(streamChannels, 960, 48000, float64(997+stream*101)), 960)
		if err != nil {
			t.Fatalf("stream %d Encode error: %v", stream, err)
		}
		packets[stream] = packet
	}
	wantMode := streamModeCELT
	switch mode {
	case internalenc.ModeSILK:
		wantMode = streamModeSILK
	case internalenc.ModeHybrid:
		wantMode = streamModeHybrid
	case internalenc.ModeCELT:
		wantMode = streamModeCELT
	}
	if toc := parseStreamTOC(packets[targetStream][0]); toc.mode != wantMode {
		t.Fatalf("target stream mode=%d want %d", toc.mode, wantMode)
	}
	packets[targetStream] = addDREDExtensionToOpusPacketFrameForTest(t, packets[targetStream], 0, body)
	return rebuildMultistreamPacketForTest(t, packets)
}

func makeMultistreamPacketWithDREDFrameForTest(t *testing.T, channels, targetStream, frame int, body []byte) []byte {
	t.Helper()

	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, 960, 48000, 997), 960)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	packets, err := parseMultistreamPacket(packet, enc.Streams())
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}
	if targetStream < 0 || targetStream >= len(packets) {
		t.Fatalf("targetStream=%d out of range for %d packets", targetStream, len(packets))
	}
	packets[targetStream] = addDREDExtensionToOpusPacketFrameForTest(t, packets[targetStream], frame, body)
	return rebuildMultistreamPacketForTest(t, packets)
}

func makeMultistreamTwoFramePacketWithDREDForTest(t *testing.T, channels, targetStream, frame int, body []byte) []byte {
	t.Helper()

	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, 960, 48000, 997), 960)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	packets, err := parseMultistreamPacket(packet, enc.Streams())
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}
	if targetStream < 0 || targetStream >= len(packets) {
		t.Fatalf("targetStream=%d out of range for %d packets", targetStream, len(packets))
	}

	for i := range packets {
		base := packets[i]
		if len(base) < 2 {
			t.Fatalf("stream %d packet too short", i)
		}
		dst := make([]byte, len(base)*2+16)
		padding := []byte(nil)
		if i == targetStream {
			if frame != 1 {
				t.Fatalf("unsupported frame=%d for two-frame test", frame)
			}
			padding = append([]byte{0x02, byte(internaldred.ExtensionID << 1), 'D', internaldred.ExperimentalVersion}, body...)
		}
		n, err := buildOpusPacketFromFramesAndPadding(base[0]&0xFC, [][]byte{base[1:], base[1:]}, padding, false, dst)
		if err != nil {
			t.Fatalf("buildOpusPacketFromFramesAndPadding(stream=%d): %v", i, err)
		}
		packets[i] = dst[:n]
	}

	return rebuildMultistreamPacketForTest(t, packets)
}
func TestDecoderCachesDREDPayloadPerStreamWhenModelLoaded(t *testing.T) {
	const channels = 3
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	setStandaloneDREDDecoderBlobForTest(t, dec)

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*channels {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*channels)
	}
	if dec.dred.dredCache[0] != (internaldred.Cache{}) {
		t.Fatalf("stream 0 cached DRED cache=%+v want zero state", dec.dred.dredCache[0])
	}
	if dec.dred.dredCache[targetStream].Len != len(body) {
		t.Fatalf("stream %d dredCache.Len=%d want %d", targetStream, dec.dred.dredCache[targetStream].Len, len(body))
	}
	if dec.dred.dredCache[targetStream].Parsed.Header.DredOffset != 4 {
		t.Fatalf("stream %d dredCache.Parsed.Header.DredOffset=%d want 4", targetStream, dec.dred.dredCache[targetStream].Parsed.Header.DredOffset)
	}
	if dec.dred.dredCache[targetStream].Parsed.Header.DredFrameOffset != 0 || dec.dred.dredCache[targetStream].Parsed.Header.Q0 != 6 || dec.dred.dredCache[targetStream].Parsed.Header.DQ != 3 || dec.dred.dredCache[targetStream].Parsed.Header.QMax != 15 {
		t.Fatalf("stream %d dredCache.Parsed.Header=(frame=%d q0=%d dq=%d qmax=%d) want (0,6,3,15)", targetStream, dec.dred.dredCache[targetStream].Parsed.Header.DredFrameOffset, dec.dred.dredCache[targetStream].Parsed.Header.Q0, dec.dred.dredCache[targetStream].Parsed.Header.DQ, dec.dred.dredCache[targetStream].Parsed.Header.QMax)
	}
	if !bytes.Equal(dec.dred.dredData[targetStream][:dec.dred.dredCache[targetStream].Len], body) {
		t.Fatalf("stream %d cached DRED payload=%x want %x", targetStream, dec.dred.dredData[targetStream][:dec.dred.dredCache[targetStream].Len], body)
	}
	result := dec.cachedDREDResult(targetStream, 960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != 480 || result.Availability.EndSamples != 0 || result.Availability.AvailableSamples != 0 {
		t.Fatalf("stream %d cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:480 EndSamples:0 AvailableSamples:0}", targetStream, result)
	}
	if got := dec.cachedDREDMaxAvailableSamples(targetStream, 960); got != 0 {
		t.Fatalf("stream %d cachedDREDMaxAvailableSamples=%d want 0", targetStream, got)
	}
	quant := make([]int, 6)
	if n := dec.cachedDREDResult(targetStream, 10080).FillQuantizerLevels(quant); n != 0 {
		t.Fatalf("stream %d cachedDREDResult.FillQuantizerLevels count=%d want 0", targetStream, n)
	}
	if want := []int{0, 0, 0, 0, 0, 0}; !slices.Equal(quant, want) {
		t.Fatalf("stream %d cachedDREDResult.FillQuantizerLevels=%v want %v", targetStream, quant, want)
	}
	window := dec.cachedDREDFeatureWindow(targetStream, 960, 960, 960, 0)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("stream %d cachedDREDFeatureWindow=%+v want base=1 recoverable=0 missing=2", targetStream, window)
	}

	dec.Reset()
	for i := range dec.dred.dredCache {
		if dec.dred.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("Reset left stream %d DRED cache=%+v want zero state", i, dec.dred.dredCache[i])
		}
		if got := dec.cachedDREDMaxAvailableSamples(i, 960); got != 0 {
			t.Fatalf("stream %d cachedDREDMaxAvailableSamples after Reset=%d want 0", i, got)
		}
	}
}

func TestDecoderCachesDREDSampleTimingForLaterStreamFrame(t *testing.T) {
	const channels = 3
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 8, -4)
	packet := makeMultistreamTwoFramePacketWithDREDForTest(t, channels, targetStream, 1, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	setStandaloneDREDDecoderBlobForTest(t, dec)

	samples, err := dec.Decode(packet, 1920)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 1920*channels {
		t.Fatalf("len(samples)=%d want %d", len(samples), 1920*channels)
	}
	if dec.dred.dredCache[targetStream].Parsed.Header.DredOffset != -4 {
		t.Fatalf("stream %d dredCache.Parsed.Header.DredOffset=%d want -4", targetStream, dec.dred.dredCache[targetStream].Parsed.Header.DredOffset)
	}
	if dec.dred.dredCache[targetStream].Parsed.Header.DredFrameOffset != 8 {
		t.Fatalf("stream %d dredCache.Parsed.Header.DredFrameOffset=%d want 8", targetStream, dec.dred.dredCache[targetStream].Parsed.Header.DredFrameOffset)
	}
	if dec.dred.dredCache[targetStream].Parsed.Header.QMax != 15 {
		t.Fatalf("stream %d dredCache.Parsed.Header.QMax=%d want 15", targetStream, dec.dred.dredCache[targetStream].Parsed.Header.QMax)
	}
	result := dec.cachedDREDResult(targetStream, 960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("stream %d cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", targetStream, result)
	}
	window := dec.cachedDREDFeatureWindow(targetStream, 960, 3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("stream %d cachedDREDFeatureWindow=%+v want base=5 recoverable=0 missing=2", targetStream, window)
	}
	if got := dec.cachedDREDMaxAvailableSamples(targetStream, 960); got != 480 {
		t.Fatalf("stream %d cachedDREDMaxAvailableSamples=%d want 480", targetStream, got)
	}
}

func TestDecoderDREDRecoveryBlendFollowsLifecycle(t *testing.T) {
	const channels = 3
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	setStandaloneDREDDecoderBlobForTest(t, dec)

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*channels {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*channels)
	}
	if dec.dred.dredCache[targetStream].Empty() {
		t.Fatal("expected cached DRED payload after successful multistream decode")
	}
	if got := dec.dred.dredPLC[targetStream].Blend(); got != 0 {
		t.Fatalf("stream %d blend after good decode=%d want 0", targetStream, got)
	}
	window := dec.cachedDREDRecoveryWindow(targetStream, 960, 960, 960)
	if window.FeatureOffsetBase != 3 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 4 {
		t.Fatalf("stream %d cachedDREDRecoveryWindow=%+v want base=3 recoverable=0 missing=4", targetStream, window)
	}
	queued := dec.queueCachedDREDRecovery(targetStream, 960, 960, 960)
	if queued != window {
		t.Fatalf("stream %d queueCachedDREDRecovery=%+v want %+v", targetStream, queued, window)
	}
	if dec.dred.dredPLC[targetStream].FECFillPos() != 0 || dec.dred.dredPLC[targetStream].FECSkip() != 4 {
		t.Fatalf("stream %d queued plc state=(fill=%d skip=%d) want (0,4)", targetStream, dec.dred.dredPLC[targetStream].FECFillPos(), dec.dred.dredPLC[targetStream].FECSkip())
	}

	plcSamples, err := dec.Decode(nil, 960)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if len(plcSamples) != 960*channels {
		t.Fatalf("len(plcSamples)=%d want %d", len(plcSamples), 960*channels)
	}
	if dec.dred.dredCache[targetStream].Empty() {
		t.Fatal("Decode(nil) dropped cached DRED payload before recovery scheduling")
	}
	if got := dec.dred.dredPLC[targetStream].Blend(); got != 1 {
		t.Fatalf("stream %d blend after PLC=%d want 1", targetStream, got)
	}

	samples, err = dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode after PLC error: %v", err)
	}
	if len(samples) != 960*channels {
		t.Fatalf("len(samples) after PLC=%d want %d", len(samples), 960*channels)
	}
	if dec.dred.dredCache[targetStream].Empty() {
		t.Fatal("expected cached DRED payload after re-decoding multistream packet")
	}
	window = dec.cachedDREDRecoveryWindow(targetStream, 960, 960, 960)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("stream %d cachedDREDRecoveryWindow after PLC and re-decode=%+v want base=1 recoverable=0 missing=2", targetStream, window)
	}
	queued = dec.queueCachedDREDRecovery(targetStream, 960, 960, 960)
	if queued != window {
		t.Fatalf("stream %d queueCachedDREDRecovery after PLC and re-decode=%+v want %+v", targetStream, queued, window)
	}
	if dec.dred.dredPLC[targetStream].FECFillPos() != 0 || dec.dred.dredPLC[targetStream].FECSkip() != 2 {
		t.Fatalf("stream %d queued plc state after PLC and re-decode=(fill=%d skip=%d) want (0,2)", targetStream, dec.dred.dredPLC[targetStream].FECFillPos(), dec.dred.dredPLC[targetStream].FECSkip())
	}
}

func TestDecoderLeavesDREDPayloadDormantWithoutDREDModel(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, false))

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	if dec.dred != nil && len(dec.dred.dredCache) != 0 {
		t.Fatalf("decoder cached dormant DRED sidecar=%+v want nil/empty", dec.dred)
	}
	if got := dec.cachedDREDMaxAvailableSamples(0, 960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples without model=%d want 0", got)
	}
}

func TestDecoderLeavesDREDStateDormantWithoutAnySidecar(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	if dec.dred != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar=%+v want nil", dec.dred)
	}

	samples, err = dec.Decode(nil, 960)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	if dec.dred != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar after PLC=%+v want nil", dec.dred)
	}
}

func TestDecoderLeavesDREDStateDormantWithOnlyMainDNNBlob(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, false))

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	if dec.dred != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar=%+v want nil", dec.dred)
	}

	samples, err = dec.Decode(nil, 960)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	if dec.dred != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar after PLC=%+v want nil", dec.dred)
	}
}

func TestDecoderPublicSetDNNBlobArmsDREDDecoderWhenBlobContainsModel(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeLoadableDecoderDREDControlTestBlob(t))
	if dec.dred == nil || !dec.dred.dredModelLoaded {
		t.Fatal("public decoder SetDNNBlob did not arm DRED decoder from combined blob")
	}

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	if dec.dred.dredCache[1].Empty() {
		t.Fatal("public SetDNNBlob did not cache target-stream DRED payload")
	}
}

func TestDecoderDecodeNilConsumesMultistreamDREDNeuralStream(t *testing.T) {
	const channels = 3
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeCELTMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	assertDecoderDecodeNilConsumesMultistreamDREDNeuralMode(t, "celt mono", packet, channels, targetStream)
}

func extractMappedStreamSamplesForTest(t *testing.T, samples []float64, frameSize, outputChannels int, mapping []byte, coupledStreams, targetStream int) []float64 {
	t.Helper()

	streamCh := streamChannels(targetStream, coupledStreams)
	if len(samples) != frameSize*outputChannels {
		t.Fatalf("len(samples)=%d want %d", len(samples), frameSize*outputChannels)
	}

	outputByStreamChannel := make([]int, streamCh)
	for i := range outputByStreamChannel {
		outputByStreamChannel[i] = -1
	}
	for outCh, mapIdx := range mapping {
		stream, ch := resolveMapping(mapIdx, coupledStreams)
		if stream != targetStream {
			continue
		}
		if ch < 0 || ch >= streamCh {
			t.Fatalf("mapping output channel %d resolved target stream channel %d outside %d", outCh, ch, streamCh)
		}
		if outputByStreamChannel[ch] >= 0 {
			t.Fatalf("target stream channel %d mapped by both output channels %d and %d", ch, outputByStreamChannel[ch], outCh)
		}
		outputByStreamChannel[ch] = outCh
	}
	for ch, outCh := range outputByStreamChannel {
		if outCh < 0 {
			t.Fatalf("target stream %d channel %d is not mapped to output", targetStream, ch)
		}
	}

	out := make([]float64, frameSize*streamCh)
	for i := 0; i < frameSize; i++ {
		for ch, outCh := range outputByStreamChannel {
			out[i*streamCh+ch] = samples[i*outputChannels+outCh]
		}
	}
	return out
}

func assertFloat64ExactForTest(t *testing.T, got, want []float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	maxAbs := 0.0
	maxIdx := -1
	for i := range got {
		if got[i] == want[i] {
			continue
		}
		d := math.Abs(got[i] - want[i])
		if d > maxAbs {
			maxAbs = d
			maxIdx = i
		}
	}
	if maxIdx >= 0 {
		t.Fatalf("%s mismatch at %d: got=%g want=%g maxAbs=%g", label, maxIdx, got[maxIdx], want[maxIdx], maxAbs)
	}
}

func assertDecoderDecodeNilConsumesMultistreamDREDNeuralMode(t *testing.T, label string, packet []byte, channels, targetStream int) {
	t.Helper()

	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("%s DefaultMapping error: %v", label, err)
	}
	packets, err := parseMultistreamPacket(packet, streams)
	if err != nil {
		t.Fatalf("%s parseMultistreamPacket error: %v", label, err)
	}
	if targetStream < 0 || targetStream >= len(packets) {
		t.Fatalf("%s targetStream=%d out of range for %d packets", label, targetStream, len(packets))
	}
	targetChannels := streamChannels(targetStream, coupledStreams)
	targetCoupled := 0
	targetMapping := []byte{0}
	if targetChannels == 2 {
		targetCoupled = 1
		targetMapping = []byte{0, 1}
	}
	oracle, err := NewDecoder(48000, targetChannels, 1, targetCoupled, targetMapping)
	if err != nil {
		t.Fatalf("%s oracle NewDecoder error: %v", label, err)
	}

	modelBlob := makeLoadableDecoderDREDControlTestBlob(t)
	oracle.SetDNNBlob(modelBlob)
	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("%s NewDecoderDefault error: %v", label, err)
	}
	dec.SetDNNBlob(modelBlob)

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("%s Decode error: %v", label, err)
	}
	if len(samples) != 960*channels {
		t.Fatalf("%s len(samples)=%d want %d", label, len(samples), 960*channels)
	}
	oracleSamples, err := oracle.Decode(packets[targetStream], 960)
	if err != nil {
		t.Fatalf("%s oracle Decode error: %v", label, err)
	}
	targetSamples := extractMappedStreamSamplesForTest(t, samples, 960, channels, mapping, coupledStreams, targetStream)
	assertFloat64ExactForTest(t, targetSamples, oracleSamples, label+" target good packet vs single-stream oracle")
	if dec.dred == nil || dec.dred.dredCache[targetStream].Empty() {
		t.Fatalf("%s Decode did not cache target-stream DRED payload", label)
	}

	plcSamples, err := dec.Decode(nil, 960)
	if err != nil {
		t.Fatalf("%s Decode(nil) error: %v", label, err)
	}
	if len(plcSamples) != 960*channels {
		t.Fatalf("%s len(plcSamples)=%d want %d", label, len(plcSamples), 960*channels)
	}
	oraclePLCSamples, err := oracle.Decode(nil, 960)
	if err != nil {
		t.Fatalf("%s oracle Decode(nil) error: %v", label, err)
	}
	targetPLCSamples := extractMappedStreamSamplesForTest(t, plcSamples, 960, channels, mapping, coupledStreams, targetStream)
	assertFloat64ExactForTest(t, targetPLCSamples, oraclePLCSamples, label+" target DRED PLC vs single-stream oracle")
	if got := dec.dred.dredRecovery[targetStream]; got != 960 {
		t.Fatalf("%s target stream dredRecovery=%d want 960", label, got)
	}
	for stream, got := range dec.dred.dredRecovery {
		if stream == targetStream {
			continue
		}
		if got != 0 {
			t.Fatalf("%s non-target stream %d dredRecovery=%d want 0", label, stream, got)
		}
	}
	if got := dec.dred.dredPLC[targetStream].Blend(); got != 1 {
		t.Fatalf("%s target stream blend after DRED PLC=%d want 1", label, got)
	}
}

func TestDecoderDecodeNilConsumesMultistreamDREDNeuralCELTStereoStream(t *testing.T) {
	const channels = 3
	const targetStream = 0
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeCELTMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	assertDecoderDecodeNilConsumesMultistreamDREDNeuralMode(t, "celt stereo", packet, channels, targetStream)
}

func TestDecoderDecodeNilConsumesMultistreamDREDNeuralCELTSecondCoupledStream(t *testing.T) {
	const channels = 5
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeCELTMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	assertDecoderDecodeNilConsumesMultistreamDREDNeuralMode(t, "celt second coupled", packet, channels, targetStream)
}

func TestDecoderDecodeNilConsumesMultistreamDREDNeuralHybridStereoStream(t *testing.T) {
	const channels = 3
	const targetStream = 0
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeModeMultistreamPacketWithDREDForTest(t, channels, targetStream, internalenc.ModeHybrid, types.BandwidthFullband, 128000, body)

	assertDecoderDecodeNilConsumesMultistreamDREDNeuralMode(t, "hybrid stereo", packet, channels, targetStream)
}

func TestDecoderDecodeNilConsumesMultistreamDREDNeuralSILKStereoStream(t *testing.T) {
	const channels = 3
	const targetStream = 0
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeModeMultistreamPacketWithDREDForTest(t, channels, targetStream, internalenc.ModeSILK, types.BandwidthWideband, 96000, body)

	assertDecoderDecodeNilConsumesMultistreamDREDNeuralMode(t, "silk stereo", packet, channels, targetStream)
}

func TestDecoderLeavesDREDPayloadDormantWhenIgnoringExtensions(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	setStandaloneDREDDecoderBlobForTest(t, dec)
	dec.SetIgnoreExtensions(true)

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	for i := range dec.dred.dredCache {
		if dec.dred.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("stream %d cached ignored DRED cache=%+v want zero state", i, dec.dred.dredCache[i])
		}
		if got := dec.cachedDREDMaxAvailableSamples(i, 960); got != 0 {
			t.Fatalf("stream %d cachedDREDMaxAvailableSamples while ignoring=%d want 0", i, got)
		}
	}
}

func TestDecoderDREDCacheFollowsStandaloneModelAndIgnoreExtensions(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	setStandaloneDREDDecoderBlobForTest(t, dec)

	if _, err := dec.Decode(packet, 960); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.dred.dredCache[1].Empty() {
		t.Fatal("expected cached DRED payload before main blob change")
	}

	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, false))
	if dec.dred == nil || !dec.dred.dredModelLoaded {
		t.Fatal("main decoder SetDNNBlob cleared standalone DRED model state")
	}
	if dec.dred.dredCache[1].Empty() {
		t.Fatal("main decoder SetDNNBlob cleared cached DRED payload")
	}
	dec.setDREDDecoderBlob(nil)
	if dec.dred != nil && len(dec.dred.dredCache) != 0 {
		t.Fatalf("clearing standalone DRED model left sidecar=%+v want nil/empty", dec.dred)
	}

	setStandaloneDREDDecoderBlobForTest(t, dec)
	if _, err := dec.Decode(packet, 960); err != nil {
		t.Fatalf("Decode after standalone rearm error: %v", err)
	}
	if dec.dred.dredCache[1].Empty() {
		t.Fatal("expected cached DRED payload before ignore toggle")
	}
	dec.SetIgnoreExtensions(true)
	for i := range dec.dred.dredCache {
		if dec.dred.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("SetIgnoreExtensions(true) left stream %d DRED cache=%+v want zero state", i, dec.dred.dredCache[i])
		}
	}
}

func TestDecoderDoesNotCachePartialDREDStateWhenLaterStreamFails(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 0, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))
	dec.decoders[0] = streamDecoderStub{
		channels: 2,
		decode: func(_ []byte, frameSize int) ([]float64, error) {
			return make([]float64, frameSize*2), nil
		},
	}
	dec.decoders[1] = streamDecoderStub{
		channels: 1,
		decode: func(_ []byte, _ int) ([]float64, error) {
			return nil, errors.New("boom")
		},
	}

	if _, err := dec.Decode(packet, 960); err == nil {
		t.Fatal("Decode error=nil want non-nil")
	}
	if dec.dred != nil && len(dec.dred.dredCache) != 0 {
		t.Fatalf("failed decode left DRED sidecar=%+v want nil/empty", dec.dred)
	}
}
