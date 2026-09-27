//go:build gopus_dred && gopus_qext && !gopus_osce

package multistream

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	internalencoder "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

var (
	dredQEXTSurroundAnalysisHelper libopustest.HelperCache
	dredQEXTChildHelper            libopustest.HelperCache
)

func TestDREDQEXTFloatSurroundMasksMatchSelectedLibopus(t *testing.T) {
	const frameCount = 2
	const channels = 6
	pcm := dredQEXTPCM(channels, false)
	want := runDREDQEXTSurroundAnalysisReference(t, pcm[:frameCount*dredQEXTFrameSize*channels], channels, frameCount)
	enc := newDREDQEXTMultistreamEncoder(t, channels, false, dredQEXTHighBitrate, dredQEXTHighRateDuration)
	packet := make([]byte, dredQEXTPacketCapacity)
	for frame := range frameCount {
		start := frame * dredQEXTFrameSize * channels
		framePCM := pcm[start : start+dredQEXTFrameSize*channels]
		if n, err := enc.EncodeInt16(framePCM, dredQEXTFrameSize, packet); err != nil || n <= 0 {
			t.Fatalf("frame %d EncodeInt16: bytes=%d err=%v", frame, n, err)
		}
		got := enc.surroundAnalysis.bandSMR
		if len(got) != channels*surroundBands || len(want[frame]) != len(got) {
			t.Fatalf("frame %d surround mask lengths Go/C=%d/%d", frame, len(got), len(want[frame]))
		}
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[frame][i]) {
				channel, band := i/surroundBands, i%surroundBands
				t.Fatalf("frame %d surround bandSMR ch=%d band=%d Go=%g(%08x) C=%g(%08x)",
					frame, channel, band, got[i], math.Float32bits(got[i]), want[frame][i], math.Float32bits(want[frame][i]))
			}
		}
	}
}

func TestDREDQEXTSurroundCenterChildReplayMatchesSelectedLibopus(t *testing.T) {
	const frameCount = 2
	const channels = 6
	pcm := dredQEXTPCM(channels, false)
	analysis := runDREDQEXTSurroundAnalysisReference(t, pcm[:frameCount*dredQEXTFrameSize*channels], channels, frameCount)
	enc := newDREDQEXTMultistreamEncoder(t, channels, false, dredQEXTHighBitrate, dredQEXTHighRateDuration)
	stream := 2
	c1, c2 := streamSourceChannels(enc.mapping, enc.coupledStreams, stream)
	if c1 < 0 || c2 >= 0 {
		t.Fatalf("center child mapping=%d/%d want one input channel", c1, c2)
	}
	childBitrate := enc.encoders[stream].Bitrate()
	direct := internalencoder.NewEncoder(48000, 1)
	direct.SetAllocatedBitrate(childBitrate)
	direct.SetVBR(true)
	direct.SetVBRConstraint(true)
	direct.SetComplexity(10)
	direct.SetBandwidth(types.BandwidthFullband)
	direct.SetMaxBandwidth(types.BandwidthFullband)
	direct.SetSignalType(types.SignalMusic)
	direct.SetPacketLoss(20)
	direct.SetMode(internalencoder.ModeCELT)
	direct.SetQEXT(true)
	direct.SetDNNBlob(requireDREDQEXTEncoderModelBlob(t))
	if err := direct.SetDREDDuration(dredQEXTHighRateDuration); err != nil {
		t.Fatal(err)
	}
	childPCM := make([]int16, dredQEXTFrameSize*frameCount)
	for frame := range frameCount {
		start := frame * dredQEXTFrameSize * channels
		for sample := range dredQEXTFrameSize {
			childPCM[frame*dredQEXTFrameSize+sample] = pcm[start+sample*channels+c1]
		}
	}
	want := runDREDQEXTChildReference(t, childPCM, analysis, c1, childBitrate, frameCount)
	cParent := runDREDQEXTMultistreamReferenceFrames(t, dredQEXTSurround, channels,
		pcm[:frameCount*dredQEXTFrameSize*channels], dredQEXTHighBitrate, dredQEXTHighRateDuration, frameCount)
	for frame := range frameCount {
		cChildren, err := parseMultistreamPacket(cParent.frames[frame].packet, cParent.streams)
		if err != nil {
			t.Fatalf("frame %d parse selected C parent packet: %v", frame, err)
		}
		if !bytes.Equal(cChildren[stream], want[frame].packet) || cParent.frames[frame].streamRanges[stream] != want[frame].finalRange {
			t.Fatalf("frame %d standalone C child differs from selected C parent: bytes=%d/%d first=%d range=%08x/%08x",
				frame, len(want[frame].packet), len(cChildren[stream]), firstByteMismatch(want[frame].packet, cChildren[stream]),
				want[frame].finalRange, cParent.frames[frame].streamRanges[stream])
		}
	}
	packet := make([]byte, dredQEXTPacketCapacity)
	childFloat := make([]float32, dredQEXTFrameSize)
	for frame := range frameCount {
		mask := analysis[frame][c1*surroundBands : (c1+1)*surroundBands]
		direct.SetCELTEnergyMask(mask)
		for sample := range dredQEXTFrameSize {
			childFloat[sample] = float32(childPCM[frame*dredQEXTFrameSize+sample]) * (1.0 / 32768)
		}
		start := frame * dredQEXTFrameSize * channels
		parentPCM := pcm[start : start+dredQEXTFrameSize*channels]
		parentN, err := enc.EncodeInt16(parentPCM, dredQEXTFrameSize, packet)
		if err != nil {
			t.Fatalf("frame %d multistream encode: %v", frame, err)
		}
		childPackets, err := parseMultistreamPacket(packet[:parentN], enc.Streams())
		if err != nil {
			t.Fatalf("frame %d parse parent packet: %v", frame, err)
		}
		if gotMask := enc.encoders[stream].CELTEnergyMask(); !equalFloat32Bits(gotMask, mask) {
			t.Fatalf("frame %d parent center mask changed before child replay", frame)
		}
		got, err := direct.EncodeShortMixedWithAnalysisMaxBytes(childFloat, dredQEXTFrameSize, childFloat, dredQEXTPacketCapacity)
		if err != nil {
			t.Fatalf("frame %d direct child encode: %v", frame, err)
		}
		parentRange := enc.encoders[stream].FinalRange()
		if !bytes.Equal(got, want[frame].packet) || direct.FinalRange() != want[frame].finalRange {
			t.Fatalf("frame %d direct child differs from selected C: direct/C bytes=%d/%d first=%d range=%08x/%08x Go{%s} C{%s}; parent/direct bytes=%d/%d first=%d range=%08x/%08x equal=%t",
				frame, len(got), len(want[frame].packet), firstByteMismatch(got, want[frame].packet), direct.FinalRange(), want[frame].finalRange,
				describeDREDQEXTChildPacket(got), describeDREDQEXTChildPacket(want[frame].packet),
				len(childPackets[stream]), len(got), firstByteMismatch(childPackets[stream], got), parentRange, direct.FinalRange(),
				bytes.Equal(got, childPackets[stream]) && parentRange == direct.FinalRange())
		}
		if !bytes.Equal(got, childPackets[stream]) || direct.FinalRange() != parentRange {
			t.Fatalf("frame %d parent child differs from same-input direct Go replay: bytes=%d/%d range=%08x/%08x",
				frame, len(childPackets[stream]), len(got), parentRange, direct.FinalRange())
		}
	}
}

func TestDREDQEXTSurroundCoupledChildReplayMatchesSelectedLibopus(t *testing.T) {
	const frameCount = 11
	const channels = 6
	const stream = 1
	pcm := dredQEXTPCM(channels, false)
	pcm = pcm[:frameCount*dredQEXTFrameSize*channels]
	analysis := runDREDQEXTSurroundAnalysisReference(t, pcm, channels, frameCount)
	cParent := runDREDQEXTMultistreamReferenceFrames(t, dredQEXTSurround, channels,
		pcm, dredQEXTHighBitrate, dredQEXTHighRateDuration, frameCount)
	enc := newDREDQEXTMultistreamEncoder(t, channels, false, dredQEXTHighBitrate, dredQEXTHighRateDuration)
	if enc.Streams() != cParent.streams || !bytes.Equal(enc.mapping, cParent.mapping) {
		t.Fatalf("surround layout Go=%d/%v C=%d/%v", enc.Streams(), enc.mapping, cParent.streams, cParent.mapping)
	}
	c1, c2 := streamSourceChannels(enc.mapping, enc.coupledStreams, stream)
	if c1 < 0 || c2 < 0 {
		t.Fatalf("stream %d mapping=%d/%d want two source channels", stream, c1, c2)
	}
	childBitrate := enc.encoders[stream].Bitrate()
	direct := internalencoder.NewEncoder(48000, 2)
	direct.SetAllocatedBitrate(childBitrate)
	direct.SetVBR(true)
	direct.SetVBRConstraint(true)
	direct.SetComplexity(10)
	direct.SetBandwidth(types.BandwidthFullband)
	direct.SetMaxBandwidth(types.BandwidthFullband)
	direct.SetSignalType(types.SignalMusic)
	direct.SetPacketLoss(20)
	direct.SetMode(internalencoder.ModeCELT)
	direct.SetQEXT(true)
	direct.SetDNNBlob(requireDREDQEXTEncoderModelBlob(t))
	if err := direct.SetDREDDuration(dredQEXTHighRateDuration); err != nil {
		t.Fatal(err)
	}
	childFloat := make([]float32, dredQEXTFrameSize*2)
	packet := make([]byte, dredQEXTPacketCapacity)
	goMask := make([]float32, 2*surroundBands)
	for frame := range frameCount {
		start := frame * dredQEXTFrameSize * channels
		parentPCM := pcm[start : start+dredQEXTFrameSize*channels]
		parentN, err := enc.EncodeInt16(parentPCM, dredQEXTFrameSize, packet)
		if err != nil {
			t.Fatalf("frame %d multistream encode: %v", frame, err)
		}
		goChildren, err := parseMultistreamPacket(packet[:parentN], enc.Streams())
		if err != nil {
			t.Fatalf("frame %d parse Go parent packet: %v", frame, err)
		}
		cChildren, err := parseMultistreamPacket(cParent.frames[frame].packet, cParent.streams)
		if err != nil {
			t.Fatalf("frame %d parse C parent packet: %v", frame, err)
		}
		mask := enc.encoders[stream].CELTEnergyMask()
		copy(goMask[:surroundBands], analysis[frame][c1*surroundBands:(c1+1)*surroundBands])
		copy(goMask[surroundBands:], analysis[frame][c2*surroundBands:(c2+1)*surroundBands])
		if !equalFloat32Bits(mask, goMask) {
			t.Fatalf("frame %d coupled stream mask differs from selected C analysis: Go=%08x/%08x C=%08x/%08x",
				frame, math.Float32bits(mask[0]), math.Float32bits(mask[surroundBands]),
				math.Float32bits(goMask[0]), math.Float32bits(goMask[surroundBands]))
		}
		direct.SetCELTEnergyMask(mask)
		for sample := range dredQEXTFrameSize {
			childFloat[sample*2] = float32(pcm[start+sample*channels+c1]) * (1.0 / 32768)
			childFloat[sample*2+1] = float32(pcm[start+sample*channels+c2]) * (1.0 / 32768)
		}
		got, err := direct.EncodeShortMixedWithAnalysisMaxBytes(childFloat, dredQEXTFrameSize, childFloat, dredQEXTPacketCapacity)
		if err != nil {
			t.Fatalf("frame %d direct coupled child encode: %v", frame, err)
		}
		goRange := enc.encoders[stream].FinalRange()
		if !bytes.Equal(got, goChildren[stream]) || direct.FinalRange() != goRange {
			t.Fatalf("frame %d direct child differs from Go parent: bytes=%d/%d range=%08x/%08x",
				frame, len(got), len(goChildren[stream]), direct.FinalRange(), goRange)
		}
		cRange := cParent.frames[frame].streamRanges[stream]
		if !bytes.Equal(got, cChildren[stream]) || direct.FinalRange() != cRange {
			t.Fatalf("frame %d direct coupled child differs from selected C: first=%d bytes=%d/%d range=%08x/%08x Go{%s} C{%s} extensionDiff={%s}",
				frame, firstByteMismatch(got, cChildren[stream]), len(got), len(cChildren[stream]), direct.FinalRange(), cRange,
				describeDREDQEXTChildPacket(got), describeDREDQEXTChildPacket(cChildren[stream]),
				describeDREDQEXTChildPayloadDifference(got, cChildren[stream]))
		}
	}
}

func describeDREDQEXTChildPayloadDifference(got, want []byte) string {
	gotPacket, gotErr := parseOpusPacket(got, false)
	wantPacket, wantErr := parseOpusPacket(want, false)
	if gotErr != nil || wantErr != nil {
		return fmt.Sprintf("packet-parse got=%v want=%v", gotErr, wantErr)
	}
	var gotMain, wantMain []byte
	for _, frame := range gotPacket.frames {
		gotMain = append(gotMain, frame...)
	}
	for _, frame := range wantPacket.frames {
		wantMain = append(wantMain, frame...)
	}
	gotExtensions, gotErr := parsePacketExtensionList(gotPacket.padding, gotPacket.paddingFrameCount)
	wantExtensions, wantErr := parsePacketExtensionList(wantPacket.padding, wantPacket.paddingFrameCount)
	if gotErr != nil || wantErr != nil {
		return fmt.Sprintf("extension-parse got=%v want=%v mainFirst=%d", gotErr, wantErr, firstByteMismatch(gotMain, wantMain))
	}
	for i := range max(len(gotExtensions), len(wantExtensions)) {
		if i >= len(gotExtensions) || i >= len(wantExtensions) {
			return fmt.Sprintf("extension-count Go/C=%d/%d mainFirst=%d", len(gotExtensions), len(wantExtensions), firstByteMismatch(gotMain, wantMain))
		}
		gotExt, wantExt := gotExtensions[i], wantExtensions[i]
		if gotExt.ID != wantExt.ID || gotExt.Frame != wantExt.Frame || !bytes.Equal(gotExt.Data, wantExt.Data) {
			return fmt.Sprintf("mainFirst=%d paddingFirst=%d extension[%d] ID/frame=%d/%d vs %d/%d payloadLen=%d/%d payloadFirst=%d",
				firstByteMismatch(gotMain, wantMain), firstByteMismatch(gotPacket.padding, wantPacket.padding),
				i, gotExt.ID, gotExt.Frame, wantExt.ID, wantExt.Frame, len(gotExt.Data), len(wantExt.Data), firstByteMismatch(gotExt.Data, wantExt.Data))
		}
	}
	return fmt.Sprintf("mainFirst=%d paddingFirst=%d extensions-equal", firstByteMismatch(gotMain, wantMain), firstByteMismatch(gotPacket.padding, wantPacket.padding))
}

type dredQEXTChildFrame struct {
	finalRange uint32
	packet     []byte
}

func runDREDQEXTChildReference(t *testing.T, pcm []int16, masks [][]float32, maskChannel, bitrate, frameCount int) []dredQEXTChildFrame {
	t.Helper()
	path, err := dredQEXTChildHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "combined DRED-QEXT elementary child encode",
		OutputBase:  "gopus_libopus_refencode_dred_qext_child",
		SourceFile:  "libopus_refencode_dred_qext_child.c",
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		RefIncludes: []string{"celt", "src", "dnn"},
		DREDQEXTRef: true,
		Libs:        []string{libopustest.DREDQEXTRefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "combined DRED-QEXT elementary child encode", err)
	}
	payload := libopustest.NewOraclePayloadVersion("GQCI", 1,
		48000, uint32(bitrate), uint32(dredQEXTHighRateDuration), 10, 1105,
		uint32(types.SignalMusic), 20, dredQEXTFrameSize, uint32(frameCount), dredQEXTPacketCapacity)
	for frame := range frameCount {
		start := frame * dredQEXTFrameSize
		for _, value := range masks[frame][maskChannel*surroundBands : (maskChannel+1)*surroundBands] {
			payload.Float32s(value)
		}
		for _, sample := range pcm[start : start+dredQEXTFrameSize] {
			payload.I16(sample)
		}
	}
	reader, err := libopustest.RunOracleVersion(path, payload.Bytes(), "combined DRED-QEXT elementary child encode", "GQCO", 1)
	if err != nil {
		t.Fatalf("run selected C elementary child: %v", err)
	}
	if got := int(reader.U32()); got != frameCount {
		t.Fatalf("C child frames=%d want %d", got, frameCount)
	}
	result := make([]dredQEXTChildFrame, frameCount)
	for frame := range result {
		result[frame].finalRange = reader.U32()
		n := int(reader.U32())
		if n <= 0 || n > dredQEXTPacketCapacity {
			t.Fatalf("C child frame %d packet length=%d", frame, n)
		}
		result[frame].packet = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return result
}

func equalFloat32Bits(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

func runDREDQEXTSurroundAnalysisReference(t *testing.T, pcm []int16, channels, frameCount int) [][]float32 {
	t.Helper()
	path, err := dredQEXTSurroundAnalysisHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "combined DRED-QEXT float surround analysis",
		OutputBase:  "gopus_libopus_surround_analysis_float_info",
		SourceFile:  "libopus_surround_analysis_float_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		RefIncludes: []string{"celt", "src", "dnn"},
		DREDQEXTRef: true,
		Libs:        []string{libopustest.DREDQEXTRefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "combined DRED-QEXT float surround analysis", err)
	}
	payload := libopustest.NewOraclePayloadVersion("GSFI", 1, 48000, uint32(channels), dredQEXTFrameSize, uint32(frameCount))
	for frame := range frameCount {
		payload.U32(0)
		start := frame * dredQEXTFrameSize * channels
		for _, sample := range pcm[start : start+dredQEXTFrameSize*channels] {
			payload.I16(sample)
		}
	}
	reader, err := libopustest.RunOracleVersion(path, payload.Bytes(), "combined DRED-QEXT float surround analysis", "GSFO", 1)
	if err != nil {
		t.Fatalf("run selected C float surround analysis: %v", err)
	}
	if got := int(reader.U32()); got != channels {
		t.Fatalf("C analysis channels=%d want %d", got, channels)
	}
	if got := int(reader.U32()); got != frameCount {
		t.Fatalf("C analysis frames=%d want %d", got, frameCount)
	}
	result := make([][]float32, frameCount)
	for frame := range result {
		result[frame] = make([]float32, channels*surroundBands)
		for i := range result[frame] {
			result[frame][i] = math.Float32frombits(reader.U32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return result
}
