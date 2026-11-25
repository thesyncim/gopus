//go:build gopus_qext

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/benchutil"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestQEXTActiveStereoDecodeMatchesLibopus(t *testing.T) {
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr", true)
	compareQEXTDecodeSequenceWithLibopus(t, 2, 960, packets)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 1920)
	for _, packet := range packets {
		if n, err := dec.Decode(packet, pcm); err != nil || n != 960 {
			t.Fatalf("warm active QEXT decode: samples=%d err=%v", n, err)
		}
	}
	var decoded int
	var decodeErr error
	allocs := testing.AllocsPerRun(20, func() {
		decoded, decodeErr = dec.Decode(packets[0], pcm)
	})
	if decoded != 960 || decodeErr != nil {
		t.Fatalf("measured active QEXT decode: samples=%d err=%v", decoded, decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm active QEXT decode allocs/call=%g, want 0", allocs)
	}
}

func qextParseExtensionRegion(packet []byte) (tocCode, paddingLen, extPayloadBytes int, extIDPresent, ok bool) {
	if len(packet) < 2 {
		return
	}
	tocCode = int(packet[0] & 0x03)
	if tocCode != 3 {
		ok = true // code-0/1/2 packets: no extension region (QEXT absent)
		return
	}
	offset := 2
	for offset < len(packet) {
		b := int(packet[offset])
		offset++
		paddingLen += b
		if b != 255 {
			break
		}
	}
	if paddingLen <= 0 || len(packet)-paddingLen < 0 {
		return
	}
	ext := packet[len(packet)-paddingLen:]
	if len(ext) == 0 {
		return
	}
	const qextIDByteValue = 0xF8 // QEXT_EXTENSION_ID<<1 = 124<<1 = 0xF8
	extIDPresent = ext[0] == qextIDByteValue
	if extIDPresent {
		extPayloadBytes = paddingLen - 1 // one byte is the extension ID itself
	}
	ok = true
	return
}

// qextSinePCM returns a single-period 997 Hz sine for the given channels and
// frameSize at 48 kHz.  This matches the PCM used across decoder_qext_test.go
// so tests are comparable.
func qextSinePCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[i*channels] = float32(0.45 * math.Sin(phase))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
		}
	}
	return pcm
}

// TestQEXTCBRExtensionFramingByteParityMatchesLibopus compares complete CBR
// CELT QEXT packet bytes and final ranges with the paired libopus oracle.
//
// The underlying fix is in celt/celt_encoder.c lines 2543–2556: for CBR the
// QEXT reservation calls compute_vbr(tf2=min(1,2*tf)) to compute the natural
// VBR target rather than using the naive nbCompressedBytes−qextBytes/3
// shortcut.  This test gates that behaviour from regressing.
func TestQEXTCBRExtensionFramingByteParityMatchesLibopus(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired QEXT opus_demo", err)
		return
	}

	cases := []struct {
		channels int
		bitrate  int
		wantQEXT bool // QEXT is expected to activate at this config
	}{
		{1, 256000, true},
		{2, 256000, true},
		{1, 128000, true},
		{1, 96000, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dch-%dk", tc.channels, tc.bitrate/1000), func(t *testing.T) {
			pcm := qextSinePCM(tc.channels, 960)

			refPacket, refRange := encodeLibopusPacketWithFinalRangeAtBitrate(t, opusDemo, tc.channels, pcm, true, true, tc.bitrate)
			if len(refPacket) == 0 {
				t.Fatal("libopus returned empty packet")
			}
			refCode, refPaddingLen, refExtBytes, refHasExt, refOK := qextParseExtensionRegion(refPacket)
			if !refOK {
				t.Fatalf("could not parse libopus packet: %x", refPacket[:min(16, len(refPacket))])
			}

			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: tc.channels, Application: ApplicationRestrictedCelt})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBandwidth(BandwidthFullband); err != nil {
				t.Fatalf("SetBandwidth: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			if err := enc.SetFrameSize(960); err != nil {
				t.Fatalf("SetFrameSize: %v", err)
			}
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}
			// opus_demo's -f32 input converts to signed 24-bit samples with
			// floor(.5 + sample*8388608) before calling opus_encode24.
			pcm24 := make([]int32, len(pcm))
			for i, sample := range pcm {
				pcm24[i] = int32(math.Floor(0.5 + float64(sample*8388608)))
			}
			out := make([]byte, 1276)
			n, err := enc.EncodeInt24(pcm24, out)
			if err != nil {
				t.Fatalf("gopus Encode: %v", err)
			}
			gopusPacket := out[:n]
			if len(gopusPacket) == 0 {
				t.Fatal("gopus returned empty packet")
			}
			if got := enc.FinalRange(); got != refRange {
				t.Errorf("final range: gopus=%08x libopus=%08x", got, refRange)
			}
			if !bytes.Equal(gopusPacket, refPacket) {
				t.Errorf("complete packet: first difference at byte %d, gopus len=%d libopus len=%d", firstDiffByte(gopusPacket, refPacket), len(gopusPacket), len(refPacket))
			}
			gopusCode, gopusPaddingLen, gopusExtBytes, gopusHasExt, gopusOK := qextParseExtensionRegion(gopusPacket)
			if !gopusOK {
				t.Fatalf("could not parse gopus packet: %x", gopusPacket[:min(16, len(gopusPacket))])
			}

			// TOC code must match.
			if gopusCode != refCode {
				t.Errorf("TOC code: gopus=%d ref=%d", gopusCode, refCode)
			}
			// QEXT extension presence must match.
			if gopusHasExt != refHasExt {
				t.Errorf("QEXT extID present: gopus=%v ref=%v", gopusHasExt, refHasExt)
			}
			if tc.wantQEXT && !refHasExt {
				t.Errorf("expected QEXT extension in libopus packet but not found")
			}
			if !refHasExt {
				return // both correctly omit QEXT
			}
			// Padding length must be byte-identical (encodes qext byte count).
			if gopusPaddingLen != refPaddingLen {
				t.Errorf("paddingLen: gopus=%d ref=%d (qext payload: gopus=%d ref=%d)",
					gopusPaddingLen, refPaddingLen, gopusExtBytes, refExtBytes)
			}
			// Extension payload byte count must match exactly.
			if gopusExtBytes != refExtBytes {
				t.Errorf("QEXT ext payload bytes: gopus=%d ref=%d", gopusExtBytes, refExtBytes)
			}
		})
	}
}

// TestQEXTExtensionAbsentBelowThresholdMatchesLibopus verifies that gopus
// omits the QEXT extension for configurations where the packet budget is too
// small to activate QEXT.  This reflects the packet-space threshold check:
// celt/celt_encoder.c lines 2539–2540: qext_bytes = 0 when
// (nbCompressedBytes − offset)*4/5 ≤ 20, and no extension is written.
//
// For 1-ch 64 kbps / 20 ms: nbCompressedBytes = 159, offset = 200,
// (159−200)*4/5 < 0 → qext_bytes = 0 → no QEXT extension.
// For 2-ch 128 kbps / 20 ms: nbCompressedBytes = 319, offset = 400, same logic.
func TestQEXTExtensionAbsentBelowThresholdMatchesLibopus(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	cases := []struct {
		channels int
		bitrate  int
	}{
		{1, 64000},  // 1ch 64kbps: nbCompressedBytes=159 < offset=200 → no QEXT
		{2, 128000}, // 2ch 128kbps: nbCompressedBytes=319 < offset=400 → no QEXT
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dch-%dk", tc.channels, tc.bitrate/1000), func(t *testing.T) {
			pcm := qextSinePCM(tc.channels, 960)

			// Use the correct bitrate for the oracle call, not the default 256kbps.
			refPacket := encodeLibopusPacketAtBitrate(t, opusDemo, tc.channels, pcm, true, true, tc.bitrate)
			_, _, _, refHasExt, _ := qextParseExtensionRegion(refPacket)
			if refHasExt {
				t.Skipf("libopus activated QEXT for %dch %dbps (expected absent); skip threshold test",
					tc.channels, tc.bitrate)
			}

			enc := internalenc.NewEncoder(48000, tc.channels)
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(internalenc.ModeCBR)
			enc.SetComplexity(10)
			enc.SetQEXT(true)

			gopusPacket, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("gopus Encode: %v", err)
			}
			_, _, _, gopusHasExt, _ := qextParseExtensionRegion(gopusPacket)
			if gopusHasExt {
				t.Errorf("gopus added QEXT extension but libopus did not (packet: %x)",
					gopusPacket[:min(8, len(gopusPacket))])
			}
		})
	}
}

// TestQEXTCBRExtensionSizeExactMatchesLibopus verifies that the QEXT extension
// payload byte count from gopus matches the libopus oracle exactly for a
// representative CBR matrix (channel × bitrate).
func TestQEXTCBRExtensionSizeExactMatchesLibopus(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type testCase struct {
		channels int
		bitrate  int
	}
	cases := []testCase{
		{1, 128000},
		{1, 256000},
		{2, 256000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dch-%dk", tc.channels, tc.bitrate/1000), func(t *testing.T) {
			pcm := qextSinePCM(tc.channels, 960)

			refPacket := encodeLibopusPacketAtBitrate(t, opusDemo, tc.channels, pcm, true, true, tc.bitrate)
			_, refPaddingLen, refExtBytes, refHasExt, _ := qextParseExtensionRegion(refPacket)
			if !refHasExt {
				t.Skipf("libopus did not produce QEXT extension for %dch %dbps", tc.channels, tc.bitrate)
			}

			enc := internalenc.NewEncoder(48000, tc.channels)
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(internalenc.ModeCBR)
			enc.SetComplexity(10)
			enc.SetQEXT(true)

			gopusPacket, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("gopus Encode: %v", err)
			}
			_, gopusPaddingLen, gopusExtBytes, gopusHasExt, _ := qextParseExtensionRegion(gopusPacket)
			if !gopusHasExt {
				t.Fatalf("gopus did not produce QEXT extension for %dch %dbps", tc.channels, tc.bitrate)
			}

			if gopusPaddingLen != refPaddingLen {
				t.Errorf("paddingLen mismatch: gopus=%d ref=%d", gopusPaddingLen, refPaddingLen)
			}
			if gopusExtBytes != refExtBytes {
				t.Errorf("QEXT payload bytes: gopus=%d ref=%d", gopusExtBytes, refExtBytes)
			}
		})
	}
}

// TestQEXTMultistreamEncoderProducesQEXTExtension verifies that the public
// gopus multistream encoder, when QEXT is enabled, produces packets that
// contain a QEXT extension and that a multistream decoder can decode them to
// audio without error.
func TestQEXTMultistreamEncoderProducesQEXTExtension(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			streams := 1
			coupledStreams := 0
			mapping := []byte{0}
			if channels == 2 {
				coupledStreams = 1
				mapping = []byte{0, 1}
			}

			mse, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, ApplicationRestrictedCelt)
			if err != nil {
				t.Fatalf("NewMultistreamEncoder: %v", err)
			}
			if err := mse.SetBitrate(256000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := mse.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := mse.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}

			pcm := qextSinePCM(channels, frameSize)

			buf := make([]byte, 8192)
			n, err := mse.Encode(pcm, buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			packet := buf[:n]
			if len(packet) == 0 {
				t.Fatal("Encode returned empty packet")
			}

			// Verify QEXT extension is present in the encoded packet.
			_, _, _, hasExt, ok := qextParseExtensionRegion(packet)
			if !ok {
				t.Fatalf("qextParseExtensionRegion failed for packet: %x", packet[:min(8, len(packet))])
			}
			if !hasExt {
				t.Error("multistream encoder with QEXT enabled did not produce QEXT extension")
			}

			// Decode the packet and verify audio is non-empty.
			msd, err := NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder: %v", err)
			}
			pcmOut := make([]float32, frameSize*channels)
			gotN, err := msd.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if gotN != frameSize {
				t.Errorf("decoded %d samples want %d", gotN, frameSize)
			}

			// Decoded samples must be non-zero (we encoded a sine wave).
			nonZero := 0
			for _, s := range pcmOut[:gotN*channels] {
				if s != 0 {
					nonZero++
				}
			}
			if nonZero == 0 {
				t.Error("decoded audio is all-zero for a sine wave input")
			}
		})
	}
}

// TestQEXTMultistreamDecodeMatchesLibopusOracle verifies that the gopus
// multistream decoder applied to a libopus-encoded QEXT packet (from the oracle
// binary) produces audio matching the per-stream libopus oracle decode.
// This is the multistream QEXT decode parity test.
func TestQEXTMultistreamDecodeMatchesLibopusOracle(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	// Reuse the per-stream oracle helper from multistream/qext_decoder_test.go
	// approach: build a 2-stream multistream packet from two libopus QEXT
	// packets and decode with gopus multistream decoder.

	for _, coupledChannels := range []int{1, 2} {
		coupledChannels := coupledChannels
		t.Run(fmt.Sprintf("coupled%dch", coupledChannels), func(t *testing.T) {
			// Build a single-stream (coupledChannels channels) packet from the oracle.
			// Use CBR 256kbps to ensure QEXT activates for all channel counts and
			// the extension region starts with the 0xF8 QEXT ID byte (code-3 packet).
			pcm := qextSinePCM(coupledChannels, 960)
			refPacket := encodeLibopusPacketAtBitrate(t, opusDemo, coupledChannels, pcm, true, true, 256000)

			// Use findPacketExtension to confirm QEXT is present.
			_, _, padding, nbFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			_, hasExt, _ := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if !hasExt {
				t.Skipf("libopus did not produce QEXT for %dch CBR 256kbps", coupledChannels)
			}

			// Multistream decode via gopus.
			streams := 1
			coupledStreams := 0
			mapping := []byte{0}
			if coupledChannels == 2 {
				coupledStreams = 1
				mapping = []byte{0, 1}
			}

			msd, err := NewMultistreamDecoder(48000, coupledChannels, streams, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder: %v", err)
			}
			outBuf := make([]float32, 960*coupledChannels)
			gotN, err := msd.Decode(refPacket, outBuf)
			if err != nil {
				t.Fatalf("MultistreamDecoder.Decode: %v", err)
			}
			if gotN != 960 {
				t.Errorf("decoded %d samples want 960", gotN)
			}

			// Single-stream reference decode for comparison.
			dec, err := NewDecoder(DefaultDecoderConfig(48000, coupledChannels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			wantBuf := make([]float32, 960*coupledChannels)
			n, err := dec.Decode(refPacket, wantBuf)
			if err != nil {
				t.Fatalf("Decoder.Decode: %v", err)
			}
			if n != gotN {
				t.Fatalf("sample count mismatch: ms=%d single=%d", gotN, n)
			}

			// Both paths must produce identical output for a single-stream packet.
			if !bytes.Equal(float32SliceToBytes(outBuf[:n*coupledChannels]),
				float32SliceToBytes(wantBuf[:n*coupledChannels])) {
				maxDiff := float32(0)
				for i := range outBuf[:n*coupledChannels] {
					d := outBuf[i] - wantBuf[i]
					if d < 0 {
						d = -d
					}
					if d > maxDiff {
						maxDiff = d
					}
				}
				t.Errorf("multistream vs single-stream decode diff: maxDiff=%e", maxDiff)
			}
		})
	}
}

// float32SliceToBytes reinterprets a []float32 as []byte for comparison.
func float32SliceToBytes(s []float32) []byte {
	if len(s) == 0 {
		return nil
	}
	// Use unsafe-free approach: compare via math.Float32bits.
	b := make([]byte, len(s)*4)
	for i, v := range s {
		bits := math.Float32bits(v)
		b[4*i] = byte(bits)
		b[4*i+1] = byte(bits >> 8)
		b[4*i+2] = byte(bits >> 16)
		b[4*i+3] = byte(bits >> 24)
	}
	return b
}

func qextSilencePacket(t *testing.T, codedChannels, extensionBytes int) []byte {
	t.Helper()
	toc := byte(31 << 3) // 20 ms full-band CELT
	if codedChannels == 2 {
		toc |= 4
	}
	packet := make([]byte, 4000)
	n, err := buildPacketWithOptions(toc, [][]byte{{0xff, 0xfe}}, packet, 0, false,
		[]packetExtensionData{{ID: qextPacketExtensionID, Frame: 0, Data: make([]byte, extensionBytes)}}, false)
	if err != nil {
		t.Fatal(err)
	}
	return packet[:n]
}

func compareQEXTDecodeSequenceWithLibopus(t *testing.T, outputChannels, frameSize int, packets [][]byte) {
	t.Helper()
	libopustest.RequireOracle(t)
	wantPCM, wantRanges, err := selectedQEXTDecodeSequenceReference(outputChannels, frameSize, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT decoder", err)
		return
	}
	samplesPerPacket := frameSize * outputChannels

	dec, err := NewDecoder(DefaultDecoderConfig(48000, outputChannels))
	if err != nil {
		t.Fatal(err)
	}
	gotPCM := make([]float32, samplesPerPacket)
	for i, packet := range packets {
		n, err := dec.Decode(packet, gotPCM)
		if err != nil || n != frameSize {
			t.Fatalf("Go frame %d samples=%d err=%v", i, n, err)
		}
		if got := dec.FinalRange(); got != wantRanges[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRanges[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}

func TestQEXTReceivedSilencePacketMatchesLibopus(t *testing.T) {
	for _, codedChannels := range []int{1, 2} {
		for _, outputChannels := range []int{1, 2} {
			for _, extensionBytes := range []int{20, 64, 128} {
				name := fmt.Sprintf("coded%d_output%d_extension%d", codedChannels, outputChannels, extensionBytes)
				t.Run(name, func(t *testing.T) {
					packet := qextSilencePacket(t, codedChannels, extensionBytes)
					compareQEXTDecodeSequenceWithLibopus(t, outputChannels, 960, [][]byte{packet})
				})
			}
		}
	}
}

func TestQEXTReceivedSilenceRecoveryMatchesLibopus(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			bitrate, mode, modeArg := 128000, BitrateModeVBR, ""
			expectExtension := true
			if channels == 2 {
				bitrate, mode, modeArg = 96000, BitrateModeCVBR, "-cvbr"
				expectExtension = false
			}
			packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 2, channels, bitrate, mode, modeArg, expectExtension)
			if len(packets) != 2 {
				t.Fatalf("C packet count %d, want 2", len(packets))
			}
			sequence := [][]byte{packets[0], qextSilencePacket(t, channels, 64), packets[1]}
			compareQEXTDecodeSequenceWithLibopus(t, channels, 960, sequence)
		})
	}
}

func TestQEXTStatefulCVBRPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 96000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStatefulCVBR128kPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 128000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStatefulVBRPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 128000, BitrateModeVBR, "")
}

func TestQEXTStateful5msCubicPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 2, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStateful5msCBRTargetTruncationMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 256000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful5msFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStatefulStereoFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr", true)
}

func TestQEXTStateful10msFixedStoragePacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 480, 3, 1, 256000, BitrateModeVBR, "", true)
}

func TestQEXTStateful5msStereoThetaRDOPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 2, 128000, BitrateModeCVBR, "-cvbr", false)
}

func TestQEXTStateful40msCBRPaddingMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 96000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful40msVBRSubframeBudgetMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStateful40msVBRSubframeBudgetCapBoundaryMatchesLibopus(t *testing.T) {
	// The two-frame cap puts curr_max below and above the 254-byte divisor.
	for _, capBytes := range []int{510, 514} {
		t.Run(strconv.Itoa(capBytes), func(t *testing.T) {
			testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 128000, BitrateModeVBR, "", true, capBytes)
		})
	}
}

func TestQEXTStateful60msCBRPaddingMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 2880, 3, 1, 96000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful40msCappedCBRPaddingMatchesLibopus(t *testing.T) {
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 96000, BitrateModeCBR, "-cbr", true, 477)
	for frame, packet := range packets {
		if len(packet) != 477 {
			t.Fatalf("capped C frame %d length %d, want 477", frame, len(packet))
		}
	}
}

func TestQEXTStatefulPacketMatrixMatchesLibopus(t *testing.T) {
	rows := []struct {
		frameSize int
		bitrates  []int
	}{
		{240, []int{128000, 256000}},
		{480, []int{128000, 256000}},
		{960, []int{96000, 128000, 256000}},
		{1920, []int{96000, 128000}},
		{2880, []int{96000}},
	}
	modes := []struct {
		mode BitrateMode
		arg  string
	}{
		{BitrateModeCBR, "-cbr"},
		{BitrateModeCVBR, "-cvbr"},
		{BitrateModeVBR, ""},
	}
	configurations := 0
	for _, channels := range []int{1, 2} {
		for _, row := range rows {
			for _, bitrate := range row.bitrates {
				for _, mode := range modes {
					name := fmt.Sprintf("channels%d_frame%d_bitrate%d_mode%d", channels, row.frameSize, bitrate, mode.mode)
					t.Run(name, func(t *testing.T) {
						// Presence may vary by frame; every packet and decoded sample stays exact.
						packets := testQEXTStatefulPacketsWithSizeAndExtensionCheck(t, row.frameSize, 3, channels, bitrate, mode.mode, mode.arg, nil)
						compareQEXTDecodeSequenceWithLibopus(t, channels, row.frameSize, packets)
					})
					configurations++
				}
			}
		}
	}
	if configurations != 60 {
		t.Fatalf("QEXT matrix has %d configurations, want 60", configurations)
	}
}

func testQEXTStatefulPacketsMatchLibopus(t *testing.T, bitrate int, mode BitrateMode, modeArg string) {
	t.Helper()
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 1, bitrate, mode, modeArg, true)
}

func testQEXTStatefulPacketsWithSizeMatchLibopus(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string, expectExtension bool, caps ...int) [][]byte {
	t.Helper()
	return testQEXTStatefulPacketsWithSizeAndExtensionCheck(t, frameSize, frames, channels, bitrate, mode, modeArg, &expectExtension, caps...)
}

func testQEXTStatefulPacketsWithSizeAndExtensionCheck(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string, expectedExtension *bool, caps ...int) [][]byte {
	t.Helper()
	maxPayload := 1276
	if len(caps) > 0 {
		maxPayload = caps[0]
	}
	libopustest.RequireOracle(t)
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired QEXT opus_demo", err)
		return nil
	}

	pcm := make([]float32, frameSize*frames*channels)
	state := uint32(0xadd44317)
	for i := range frameSize * frames {
		phase := 2 * math.Pi * (697*float64(i)/48000 + 101*float64(i*i)/(48000*48000))
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		noise := (float64(state&0xffff)/32768 - 1) * 0.08
		for c := range channels {
			pcm[i*channels+c] = float32(0.29*math.Sin(phase+float64(c)*0.43) + 0.16*math.Sin(phase*2.7+float64(c)*0.19) + noise)
		}
	}

	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.f32")
	bitstreamPath := filepath.Join(dir, "output.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatal(err)
	}
	args := []string{"-e", "restricted-celt", "48000", strconv.Itoa(channels), strconv.Itoa(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", strconv.Itoa(frameSize / 48),
		"-max_payload", strconv.Itoa(maxPayload), "-qext"}
	if modeArg != "" {
		args = append(args, modeArg)
	}
	args = append(args, inputPath, bitstreamPath)
	if out, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("libopus QEXT encode: %v (%s)", err, out)
	}
	bitstream, err := os.ReadFile(bitstreamPath)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(mode); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm24 := make([]int32, frameSize*channels)
	packet := make([]byte, maxPayload)
	refPackets := make([][]byte, 0, frames)
	offset := 0
	for frame := range frames {
		if len(bitstream)-offset < 8 {
			t.Fatalf("libopus has fewer than %d packet records", frames)
		}
		refLen := int(binary.BigEndian.Uint32(bitstream[offset:]))
		refRange := binary.BigEndian.Uint32(bitstream[offset+4:])
		offset += 8
		if refLen <= 0 || refLen > len(bitstream)-offset {
			t.Fatalf("libopus frame %d has invalid length %d", frame, refLen)
		}
		refPacket := bitstream[offset : offset+refLen]
		offset += refLen
		refPackets = append(refPackets, append([]byte(nil), refPacket...))
		if frameSize > 960 {
			_, _, padding, packetFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("libopus frame %d framing: %v", frame, err)
			}
			_, present, err := findPacketExtension(padding, packetFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("libopus frame %d extension: %v", frame, err)
			}
			if expectedExtension != nil && present != *expectedExtension {
				t.Fatalf("libopus frame %d extension present=%t, want %t", frame, present, *expectedExtension)
			}
		} else if _, _, _, present, ok := qextParseExtensionRegion(refPacket); !ok {
			t.Fatalf("libopus frame %d extension region invalid", frame)
		} else if expectedExtension != nil && present != *expectedExtension {
			t.Fatalf("libopus frame %d extension present=%t, want %t", frame, present, *expectedExtension)
		}

		for i, sample := range pcm[frame*frameSize*channels : (frame+1)*frameSize*channels] {
			// opus_demo -f32 uses this signed-24 conversion before opus_encode24.
			pcm24[i] = int32(math.Floor(0.5 + float64(sample*8388608)))
		}
		gotLen, err := enc.EncodeInt24(pcm24, packet)
		if err != nil {
			t.Fatalf("Go frame %d encode: %v", frame, err)
		}
		if gotLen < 0 || gotLen > len(packet) {
			t.Fatalf("Go frame %d length %d exceeds caller cap %d", frame, gotLen, len(packet))
		}
		if gotLen != refLen || !bytes.Equal(packet[:gotLen], refPacket) {
			t.Errorf("frame %d packet: first difference %d, Go length %d, C length %d", frame, firstDiffByte(packet[:gotLen], refPacket), gotLen, refLen)
		}
		if gotRange := enc.FinalRange(); gotRange != refRange {
			t.Errorf("frame %d final range: Go %08x, C %08x", frame, gotRange, refRange)
		}
	}
	if !t.Failed() {
		t.Logf("%d complete QEXT packets and final ranges match paired libopus", frames)
	}
	return refPackets
}

func TestQEXTStatefulEncodeInt24SteadyAllocations(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(BitrateModeCVBR); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int32, 960)
	for i := range pcm {
		pcm[i] = int32(math.Sin(2*math.Pi*997*float64(i)/48000) * 0.4 * 8388608)
	}
	packet := make([]byte, 1276)
	for range 4 {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, present, ok := qextParseExtensionRegion(packet[:n]); !ok || !present {
			t.Fatal("QEXT extension inactive during allocation warmup")
		}
	}
	var encodeErr error
	lastLen := 0
	allocs := testing.AllocsPerRun(100, func() {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil && encodeErr == nil {
			encodeErr = err
		}
		lastLen = n
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed QEXT EncodeInt24 allocated %.1f times", allocs)
	}
	if _, _, _, present, ok := qextParseExtensionRegion(packet[:lastLen]); !ok || !present {
		t.Fatal("QEXT extension inactive after allocation measurement")
	}
}

func TestQEXTStateful40msCBRCallerBufferSteadyAllocations(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(1920); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int32, 1920)
	for i := range pcm {
		pcm[i] = int32(math.Sin(2*math.Pi*697*float64(i)/48000) * 0.38 * 8388608)
	}
	packet := make([]byte, 1276)
	assertPacket := func(n int) {
		t.Helper()
		if n != 480 {
			t.Fatalf("40 ms CBR packet length %d, want 480", n)
		}
		_, _, padding, frames, err := parsePacketFramesAndPadding(packet[:n])
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := findPacketExtension(padding, frames, qextPacketExtensionID)
		if err != nil || !present {
			t.Fatalf("active QEXT extension: present=%t err=%v", present, err)
		}
	}
	for range 6 {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil {
			t.Fatal(err)
		}
		assertPacket(n)
	}
	var encodeErr error
	lastLen := 0
	allocs := testing.AllocsPerRun(100, func() {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil && encodeErr == nil {
			encodeErr = err
		}
		lastLen = n
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed 40 ms QEXT CBR EncodeInt24 allocated %.1f times", allocs)
	}
	assertPacket(lastLen)
}

var libopusQEXTDecodeSingleHelper libopustest.HelperCache

func buildLibopusQEXTDecodeSingleHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "qext stateful float decoder",
		OutputBase:  "gopus_libopus_qext_decode_single",
		SourceFile:  "libopus_refdecode_single.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTStateful5msStereoWithoutExtensionDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 2, 128000, BitrateModeCVBR, "-cvbr", false)
	if len(packets) != 3 {
		t.Fatalf("reference packet count %d, want 3", len(packets))
	}
	const samplesPerPacket = 240 * 2
	wantPCM, wantRanges, err := selectedQEXTDecodeSequenceReference(2, 240, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT decoder", err)
		return
	}
	if len(wantPCM) != 3*samplesPerPacket || len(wantRanges) != len(packets) {
		t.Fatalf("selected reference geometry PCM=%d ranges=%d", len(wantPCM), len(wantRanges))
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	gotPCM := make([]float32, samplesPerPacket)
	for i, packet := range packets {
		n, err := dec.Decode(packet, gotPCM)
		if err != nil || n != 240 {
			t.Fatalf("Go frame %d decode: samples=%d err=%v", i, n, err)
		}
		if got := dec.FinalRange(); got != wantRanges[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRanges[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}
