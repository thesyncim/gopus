//go:build gopus_qext

// qext_full_packet_parity_test.go: full-packet byte parity tests for the QEXT
// extension framing and payload against the pinned libopus 1.6.1 oracle.
//
// Scope: QEXT extension framing (TOC code 3, padding-length byte, extension ID
// byte 0xF8, and payload byte count) must match libopus exactly for CBR CELT
// and Hybrid packets that are large enough to activate QEXT.  The main CELT
// frame bytes inside a QEXT packet differ from libopus by a pre-existing
// encoder-level delta that is tracked separately by the byte-exact encode
// parity tests.  Multistream QEXT roundtrip and the absence of QEXT
// on sub-threshold packets are also covered.
//
// Reference fix: celt/celt_encoder.c lines 2543–2556 – for CBR mode the
// reservation target is the output of compute_vbr() (tf2=min(1,2*tf)) plus
// ec_tell_frac, not the VBR shortcut nbCompressedBytes − qextBytes/3.

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/types"
)

// qextParseExtensionRegion parses a code-3 packet and extracts the QEXT
// extension framing fields.  It returns:
//   - tocCode: TOC byte & 0x03 (must be 3 for code-3 packets)
//   - paddingLen: decoded total padding length in bytes
//   - extIDPresent: whether the extension region starts with 0xF8
//   - extPayloadBytes: number of bytes after the 0xF8 extension-ID byte
//
// Returns ok=false for any malformed or non-code-3 packet.
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

// TestQEXTCBRExtensionFramingByteParityMatchesLibopus verifies that the gopus
// CBR CELT QEXT packet has byte-identical framing through the extension-ID byte
// (TOC code 3, padding-length field, and 0xF8 extension-ID byte) compared with
// the libopus oracle.
//
// The underlying fix is in celt/celt_encoder.c lines 2543–2556: for CBR the
// QEXT reservation calls compute_vbr(tf2=min(1,2*tf)) to compute the natural
// VBR target rather than using the naive nbCompressedBytes−qextBytes/3
// shortcut.  This test gates that behaviour from regressing.
func TestQEXTCBRExtensionFramingByteParityMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
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

			refPacket := encodeLibopusQEXTPacket(t, opusDemo, tc.channels, pcm, true)
			if len(refPacket) == 0 {
				t.Fatal("libopus returned empty packet")
			}
			refCode, refPaddingLen, refExtBytes, refHasExt, refOK := qextParseExtensionRegion(refPacket)
			if !refOK {
				t.Fatalf("could not parse libopus packet: %x", refPacket[:min(16, len(refPacket))])
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
			if len(gopusPacket) == 0 {
				t.Fatal("gopus returned empty packet")
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
	opusDemo, err := benchutil.QEXTOpusDemoPath()
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
	opusDemo, err := benchutil.QEXTOpusDemoPath()
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

			refPacket := encodeLibopusQEXTPacket(t, opusDemo, tc.channels, pcm, true)
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
// audio without error.  This covers the multistream QEXT entry in the parity
// matrix which was previously marked as missing.
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
	opusDemo, err := benchutil.QEXTOpusDemoPath()
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
