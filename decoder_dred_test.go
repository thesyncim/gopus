//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
)

func lpcnetplcTestQuantizePCMUpdateFloat(sample float32) float32 {
	return float32(float32ToInt16(sample)) * (1.0 / 32768.0)
}

func requireDREDRuntimeForTest(t *testing.T) {
	t.Helper()
	if !extsupport.DREDRuntime {
		t.Skip("DRED runtime disabled in this build")
	}
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

func makeValidDREDDecoderTestDNNBlob() []byte {
	var blob []byte
	for _, spec := range rdovae.DecoderLayerSpecs() {
		blob = appendDREDDecoderLayerTestRecords(blob, spec)
	}
	return blob
}

func makeValidDecoderControlWithDREDDecoderTestDNNBlob() []byte {
	blob := append([]byte(nil), makeValidDecoderTestDNNBlob()...)
	for _, spec := range rdovae.DecoderLayerSpecs() {
		blob = appendDREDDecoderLayerTestRecords(blob, spec)
	}
	return blob
}

func appendDREDDecoderLayerTestRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendDREDDecoderRecord(dst, spec.Bias, dnnblob.TypeFloat, encodeTestFloat32Payload(spec.NbOutputs))
	}
	if spec.Subias != "" {
		dst = appendDREDDecoderRecord(dst, spec.Subias, dnnblob.TypeFloat, encodeTestFloat32Payload(spec.NbOutputs))
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
		dst = appendDREDDecoderRecord(dst, spec.Scale, dnnblob.TypeFloat, encodeTestFloat32Payload(spec.NbOutputs))
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

func appendDREDDecoderRecord(dst []byte, name string, typ int32, payload []byte) []byte {
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

func encodeTestFloat32Payload(n int) []byte {
	return make([]byte, 4*n)
}

func encodeTestInt32Payload(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}

func setValidDREDDecoderBlobForTest(t *testing.T, dec *Decoder) {
	t.Helper()

	blob, err := dnnblob.Clone(makeValidDREDDecoderTestDNNBlob())
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
}

func makeValidCELTPacketForDREDTest(t *testing.T) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	pcm := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[2*i] = float32(0.45 * math.Sin(phase))
		pcm[2*i+1] = float32(0.35 * math.Sin(phase+0.37))
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode(CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(CELT) returned empty packet")
	}
	return packet
}

func makeValidMonoCELTPacketForDREDTest(t *testing.T) []byte {
	return makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 960)
}

func makeValidMonoCELTPacketForFrameSizeForDREDTest(t *testing.T, frameSize int) []byte {
	return makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthFullband)
}

func makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 1)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(128000)

	pcm := make([]float32, frameSize)
	for i := range pcm {
		phase := 2 * math.Pi * 823 * float64(i) / 48000.0
		pcm[i] = float32(0.41 * math.Sin(phase))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(mono CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(mono CELT) returned empty packet")
	}
	return packet
}

func makeValidStereoCELTPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(256000)
	enc.SetForceChannels(2)

	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		pcm[2*i] = float32(0.41 * math.Sin(2*math.Pi*823*tm))
		pcm[2*i+1] = float32(0.29 * math.Sin(2*math.Pi*617*tm+0.31))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(stereo CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(stereo CELT) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize || !toc.Stereo {
		t.Fatalf("Encode(stereo CELT) produced mode=%v bandwidth=%v frame=%d stereo=%t, want mode=%v bandwidth=%v frame=%d stereo=true", toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo, ModeCELT, bandwidth, frameSize)
	}
	return packet
}

func makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 {
		t.Fatalf("hybrid DRED test packet requires 10ms/20ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthSuperwideband && bandwidth != BandwidthFullband {
		t.Fatalf("hybrid DRED test packet requires SWB/FB bandwidth, got %v", bandwidth)
	}

	enc := internalenc.NewEncoder(48000, 1)
	enc.SetMode(internalenc.ModeHybrid)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(48000)

	pcm := make([]float32, frameSize)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = float32(0.28*math.Sin(2*math.Pi*173*tm) +
			0.17*math.Sin(2*math.Pi*347*tm+0.13) +
			0.09*math.Sin(2*math.Pi*521*tm+0.29))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(mono Hybrid): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(mono Hybrid) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeHybrid || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize {
		t.Fatalf("Encode(mono Hybrid) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeHybrid, bandwidth, frameSize)
	}
	return packet
}

func makeValidStereoHybridPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 {
		t.Fatalf("hybrid DRED test packet requires 10ms/20ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthSuperwideband && bandwidth != BandwidthFullband {
		t.Fatalf("hybrid DRED test packet requires SWB/FB bandwidth, got %v", bandwidth)
	}

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeHybrid)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(96000)
	enc.SetForceChannels(2)

	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		pcm[2*i] = float32(0.28*math.Sin(2*math.Pi*173*tm) +
			0.17*math.Sin(2*math.Pi*347*tm+0.13) +
			0.09*math.Sin(2*math.Pi*521*tm+0.29))
		pcm[2*i+1] = float32(0.25*math.Sin(2*math.Pi*211*tm+0.07) +
			0.14*math.Sin(2*math.Pi*431*tm+0.19) +
			0.07*math.Sin(2*math.Pi*653*tm+0.41))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(stereo Hybrid): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(stereo Hybrid) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeHybrid || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize || !toc.Stereo {
		t.Fatalf("Encode(stereo Hybrid) produced mode=%v bandwidth=%v frame=%d stereo=%t, want mode=%v bandwidth=%v frame=%d stereo=true", toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo, ModeHybrid, bandwidth, frameSize)
	}
	return packet
}

func makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t *testing.T, mode Mode, bandwidth Bandwidth, frameSize int) []byte {
	t.Helper()

	switch mode {
	case ModeCELT:
		return makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)
	case ModeHybrid:
		return makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, bandwidth)
	case ModeSILK:
		return makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, bandwidth)
	default:
		t.Fatalf("unsupported DRED test packet mode %v", mode)
		return nil
	}
}

func makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 && frameSize != 1920 && frameSize != 2880 {
		t.Fatalf("silk DRED test packet requires 10/20/40/60ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthNarrowband && bandwidth != BandwidthMediumband && bandwidth != BandwidthWideband {
		t.Fatalf("silk DRED test packet requires NB/MB/WB bandwidth, got %v", bandwidth)
	}

	enc := internalenc.NewEncoder(48000, 1)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(32000)

	pcm := make([]float32, frameSize)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = float32(0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(mono SILK): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(mono SILK) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize {
		t.Fatalf("Encode(mono SILK) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeSILK, bandwidth, frameSize)
	}
	return packet
}

func makeValidMono16kPacketForDREDTest(t *testing.T) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(16000, 1)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(24000)

	// One 20 ms frame at the native 16 kHz rate (320 samples). The encoder
	// honours the native-Fs frame-size contract, so the input length selects
	// the packet duration directly.
	const frameSize = 16000 / 50
	pcm := make([]float32, frameSize)
	for i := range pcm {
		phase := 2 * math.Pi * 613 * float64(i) / 16000.0
		pcm[i] = float32(0.42 * math.Sin(phase))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(16k mono): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(16k mono) returned empty packet")
	}
	return packet
}

func makeValidMono48kSILKPacketForDREDTest(t *testing.T) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 1)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(32000)

	const frameSize = 960
	pcm := make([]float32, frameSize)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = float32(0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(48k SILK): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(48k SILK) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.FrameSize != frameSize {
		t.Fatalf("Encode(48k SILK) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeSILK, BandwidthWideband, frameSize)
	}
	return packet
}

func buildSingleFramePacketWithExtensionsForDREDTest(t *testing.T, packet []byte, extensions []packetExtensionData) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	dst := make([]byte, len(packet)+64)
	n, err := buildPacketWithOptions(packet[0]&0xFC, [][]byte{packet[1:]}, dst, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions: %v", err)
	}
	return dst[:n]
}

func TestDecoderCachesDREDPayloadWhenDREDModelLoaded(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDRecoveryState == nil {
		t.Fatal("Decode with cached DRED payload did not activate recovery state")
	}
	if state.dredCache.Len != len(body) {
		t.Fatalf("dredCache.Len=%d want %d", state.dredCache.Len, len(body))
	}
	if state.dredCache.Parsed.Header.DredOffset != 4 {
		t.Fatalf("dredCache.Parsed.Header.DredOffset=%d want 4", state.dredCache.Parsed.Header.DredOffset)
	}
	if state.dredCache.Parsed.Header.DredFrameOffset != 0 || state.dredCache.Parsed.Header.Q0 != 6 || state.dredCache.Parsed.Header.DQ != 3 || state.dredCache.Parsed.Header.QMax != 15 {
		t.Fatalf("dredCache.Parsed.Header=(frame=%d q0=%d dq=%d qmax=%d) want (0,6,3,15)", state.dredCache.Parsed.Header.DredFrameOffset, state.dredCache.Parsed.Header.Q0, state.dredCache.Parsed.Header.DQ, state.dredCache.Parsed.Header.QMax)
	}
	if !bytes.Equal(state.dredData[:state.dredCache.Len], body) {
		t.Fatalf("cached dred payload=%x want %x", state.dredData[:state.dredCache.Len], body)
	}
	result := dec.cachedDREDResult(960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != 480 || result.Availability.EndSamples != 0 || result.Availability.AvailableSamples != 0 {
		t.Fatalf("cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:480 EndSamples:0 AvailableSamples:0}", result)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples=%d want 0", got)
	}
	quant := make([]int32, 6)
	if n := dec.cachedDREDResult(10080).FillQuantizerLevels(quant); n != 0 {
		t.Fatalf("cachedDREDResult.FillQuantizerLevels count=%d want 0", n)
	}
	if want := []int32{0, 0, 0, 0, 0, 0}; !slices.Equal(quant, want) {
		t.Fatalf("cachedDREDResult.FillQuantizerLevels=%v want %v", quant, want)
	}
	window := dec.cachedDREDFeatureWindow(960, 960, 960, 0)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("cachedDREDFeatureWindow=%+v want base=1 recoverable=0 missing=2", window)
	}

	dec.Reset()
	if got := requireDecoderDREDState(t, dec).dredCache; got != (internaldred.Cache{}) {
		t.Fatalf("Reset left DRED cache=%+v want zero state", got)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples after Reset=%d want 0", got)
	}
}

func TestDecoderDREDRecoveryBlendFollowsLifecycle(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	state := requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload after successful decode")
	}
	if got := state.dredPLC.Blend(); got != 0 {
		t.Fatalf("Blend after good decode=%d want 0", got)
	}
	window := dec.cachedDREDRecoveryWindow(960, 960, 960)
	if window.FeatureOffsetBase != 3 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 4 {
		t.Fatalf("cachedDREDRecoveryWindow=%+v want base=3 recoverable=0 missing=4", window)
	}
	queued := dec.queueCachedDREDRecovery(960, 960, 960)
	if queued != window {
		t.Fatalf("queueCachedDREDRecovery=%+v want %+v", queued, window)
	}
	if state.dredPLC.FECFillPos() != 0 || state.dredPLC.FECSkip() != 4 {
		t.Fatalf("queued plc state=(fill=%d skip=%d) want (0,4)", state.dredPLC.FECFillPos(), state.dredPLC.FECSkip())
	}

	plcPCM := make([]float32, 960*2)
	if _, err := dec.Decode(nil, plcPCM); err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("Decode(nil) dropped cached DRED payload before recovery scheduling")
	}
	if got := state.dredPLC.Blend(); got != 1 {
		t.Fatalf("Blend after PLC=%d want 1", got)
	}

	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode after PLC error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload after re-decoding packet")
	}
	window = dec.cachedDREDRecoveryWindow(960, 960, 960)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("cachedDREDRecoveryWindow after PLC and re-decode=%+v want base=1 recoverable=0 missing=2", window)
	}
	queued = dec.queueCachedDREDRecovery(960, 960, 960)
	if queued != window {
		t.Fatalf("queueCachedDREDRecovery after PLC and re-decode=%+v want %+v", queued, window)
	}
	if state.dredPLC.FECFillPos() != 0 || state.dredPLC.FECSkip() != 2 {
		t.Fatalf("queued plc state after PLC and re-decode=(fill=%d skip=%d) want (0,2)", state.dredPLC.FECFillPos(), state.dredPLC.FECSkip())
	}
}

func TestDecoderPrimeDREDCELTEntryHistoryUsesCELTBridge(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode returned no audio")
	}

	var want [4 * lpcnetplc.FrameSize]float32
	if got := dec.celtDecoder.FillPLCUpdate16kMono(want[:]); got != len(want) {
		t.Fatalf("FillPLCUpdate16kMono()=%d want %d", got, len(want))
	}

	beforeAnalysis := lpcnetplc.PLCBufSize
	beforePredict := lpcnetplc.PLCBufSize
	if state := dec.dredState(); state != nil && state.decoderDREDRecoveryState != nil {
		beforeAnalysis = state.dredPLC.AnalysisPos()
		beforePredict = state.dredPLC.PredictPos()
	}
	if got := dec.primeDREDCELTEntryHistory(ModeCELT, false); got != len(want) {
		t.Fatalf("primeDREDCELTEntryHistory()=%d want %d", got, len(want))
	}
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.AnalysisPos(); got != max(0, beforeAnalysis-len(want)) {
		t.Fatalf("AnalysisPos=%d want %d", got, max(0, beforeAnalysis-len(want)))
	}
	if got := state.dredPLC.PredictPos(); got != max(0, beforePredict-len(want)) {
		t.Fatalf("PredictPos=%d want %d", got, max(0, beforePredict-len(want)))
	}

	var history [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(history[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory()=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	for i := range want {
		if history[lpcnetplc.PLCBufSize-len(want)+i] != want[i] {
			t.Fatalf("history tail[%d]=%v want %v", i, history[lpcnetplc.PLCBufSize-len(want)+i], want[i])
		}
	}
}

func TestDecoderDecodePLCAppliesNeuralConcealmentWhenReady(t *testing.T) {
	requireDREDRuntimeForTest(t)

	packet := makeValidMono16kPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	frameSize := 16000 / 50
	pcm := make([]float32, frameSize)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode()=%d want %d", n, frameSize)
	}
	if state := dec.dredState(); state != nil {
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("good decode eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("good decode eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
	}
	setValidDREDDecoderBlobForTest(t, dec)

	n, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode(nil)=%d want %d", n, frameSize)
	}
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.Blend(); got != 1 {
		t.Fatalf("Blend after neural PLC=%d want 1", got)
	}
	if got := state.dredPLC.PredictPos(); got != lpcnetplc.PLCBufSize {
		t.Fatalf("PredictPos after neural PLC=%d want %d", got, lpcnetplc.PLCBufSize)
	}
}

func TestDecoderCachesDREDSampleTimingForLaterFrame(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := testStereoCELTPacket()
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}
	body := makeExperimentalDREDPayloadBodyForTest(t, 8, -4)
	packet := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, packet, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 1920*2)
	got, err := dec.Decode(packet[:n], pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got != 1920 {
		t.Fatalf("Decode samples=%d want 1920", got)
	}
	state := requireDecoderDREDState(t, dec)
	if state.dredCache.Parsed.Header.DredOffset != -4 {
		t.Fatalf("dredCache.Parsed.Header.DredOffset=%d want -4", state.dredCache.Parsed.Header.DredOffset)
	}
	if state.dredCache.Parsed.Header.DredFrameOffset != 8 {
		t.Fatalf("dredCache.Parsed.Header.DredFrameOffset=%d want 8", state.dredCache.Parsed.Header.DredFrameOffset)
	}
	if state.dredCache.Parsed.Header.QMax != 15 {
		t.Fatalf("dredCache.Parsed.Header.QMax=%d want 15", state.dredCache.Parsed.Header.QMax)
	}
	result := dec.cachedDREDResult(960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", result)
	}
	window := dec.cachedDREDFeatureWindow(960, 3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("cachedDREDFeatureWindow=%+v want base=5 recoverable=0 missing=2", window)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 480 {
		t.Fatalf("cachedDREDMaxAvailableSamples=%d want 480", got)
	}
}

func TestDecoderDREDCacheFollowsStandaloneModelAndIgnoreExtensions(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(extended, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	state := requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload before main blob change")
	}

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(non_dred) error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if !state.dredModelLoaded {
		t.Fatal("main decoder SetDNNBlob cleared standalone DRED model state")
	}
	if state.dredCache.Empty() {
		t.Fatal("main decoder SetDNNBlob cleared cached DRED payload")
	}
	dec.setDREDDecoderBlob(nil)
	if state := dec.dredState(); state != nil && state.decoderDREDPayloadState != nil {
		t.Fatalf("clearing standalone DRED model left payload sidecar=%+v", state.decoderDREDPayloadState)
	}
	if dec.dredCachedPayloadActive() {
		t.Fatal("clearing standalone DRED model left cached payload active")
	}

	setValidDREDDecoderBlobForTest(t, dec)
	if _, err := dec.Decode(extended, pcm); err != nil {
		t.Fatalf("Decode after standalone rearm error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload before ignore toggle")
	}
	dec.SetIgnoreExtensions(true)
	if got := requireDecoderDREDState(t, dec).dredCache; got != (internaldred.Cache{}) {
		t.Fatalf("SetIgnoreExtensions(true) left DRED cache=%+v want zero state", got)
	}
}

// minimalHybridTestPacket20ms creates a test packet for Hybrid FB 20ms mono (config 15).
// This is a manually constructed packet that produces valid decoder output.
// The TOC byte (0x78) indicates: config=15 (Hybrid FB 20ms), mono, code 0 (single frame).
