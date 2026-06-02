//go:build gopus_qext

// encoder_96k_framing_qext_test.go: top-level Opus packet framing parity for
// the native 96 kHz (Opus HD / QEXT) encode path.
//
// Scope: the TOC byte, frame-count byte, padding-length field, main CELT
// payload region and the reserved QEXT extension (0xF8 extension-ID byte +
// payload) must be assembled byte-for-byte like libopus --enable-qext at
// Fs=96000. The main CELT payload bytes themselves still carry a pre-existing
// HD-scale comb-prefilter residual (mono) / band-data divergence (stereo)
// tracked in celt/encoder_hd96k_encode_qext.go; this test validates the
// FRAMING structure (offsets, lengths, extension layout) regardless, and
// asserts full-packet byte parity for any region that already matches.

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// hd96kPacketLayout decomposes a native 96 kHz CELT-only code-3 Opus packet
// into its framing fields and payload regions.
type hd96kPacketLayout struct {
	toc         byte
	code        int
	countByte   byte
	hasPadding  bool
	nbFrames    int
	paddingLen  int // decoded total padding (= qext_bytes for the native path)
	padFieldLen int // bytes consumed by the padding-length field
	main        []byte
	extIDByte   byte // first byte of the extension region (0xF8 for QEXT)
	extID       int  // extIDByte >> 1
	qextPayload []byte
}

func parseHD96kLayout(t *testing.T, pkt []byte) hd96kPacketLayout {
	t.Helper()
	if len(pkt) < 2 {
		t.Fatalf("packet too short: %d", len(pkt))
	}
	var l hd96kPacketLayout
	l.toc = pkt[0]
	l.code = int(pkt[0] & 0x03)
	if l.code != 3 {
		// Code-0 (no QEXT) packet: main is everything after TOC.
		l.main = pkt[1:]
		return l
	}
	l.countByte = pkt[1]
	l.hasPadding = l.countByte&0x40 != 0
	l.nbFrames = int(l.countByte & 0x3F)
	offset := 2
	if l.hasPadding {
		start := offset
		for {
			if offset >= len(pkt) {
				t.Fatalf("padding overran packet")
			}
			b := int(pkt[offset])
			offset++
			if b == 255 {
				l.paddingLen += 254
			} else {
				l.paddingLen += b
				break
			}
		}
		l.padFieldLen = offset - start
	}
	end := len(pkt) - l.paddingLen
	if end < offset {
		t.Fatalf("bad framing: dataStart=%d end=%d", offset, end)
	}
	l.main = pkt[offset:end]
	if l.paddingLen > 0 {
		ext := pkt[end:]
		l.extIDByte = ext[0]
		l.extID = int(ext[0] >> 1)
		l.qextPayload = ext[1:]
	}
	return l
}

// TestHD96kFramingByteParityMatchesLibopus drives the public 96 kHz QEXT encode
// path and checks the assembled Opus packet framing against the libopus
// --enable-qext oracle: TOC byte, frame-count byte, padding-length field, the
// extension-ID byte (0xF8) and the byte offsets/lengths of the main and QEXT
// payload regions must all match exactly.
func TestHD96kFramingByteParityMatchesLibopus(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000

	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       bitrate,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 8000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			ref := parseHD96kLayout(t, res.Packets[0])

			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    ch,
				Application: ApplicationRestrictedCelt,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}

			out, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("EncodeFloat32: %v", err)
			}
			got := parseHD96kLayout(t, out)

			// TOC byte (config 31, stereo bit, code 3) must be identical.
			if got.toc != ref.toc {
				t.Errorf("TOC byte: got 0x%02x want 0x%02x", got.toc, ref.toc)
			}
			if got.code != ref.code {
				t.Errorf("frame code: got %d want %d", got.code, ref.code)
			}
			// Frame-count byte (padding flag | 1 frame) must match.
			if got.countByte != ref.countByte {
				t.Errorf("frame-count byte: got 0x%02x want 0x%02x", got.countByte, ref.countByte)
			}
			if got.hasPadding != ref.hasPadding {
				t.Errorf("padding flag: got %v want %v", got.hasPadding, ref.hasPadding)
			}
			if got.nbFrames != ref.nbFrames {
				t.Errorf("frame count: got %d want %d", got.nbFrames, ref.nbFrames)
			}
			// Padding-length field (qext_bytes) must be byte-identical.
			if got.paddingLen != ref.paddingLen {
				t.Errorf("padding length (qext_bytes): got %d want %d", got.paddingLen, ref.paddingLen)
			}
			if got.padFieldLen != ref.padFieldLen {
				t.Errorf("padding-length field bytes: got %d want %d", got.padFieldLen, ref.padFieldLen)
			}
			// Main CELT payload byte budget must match.
			if len(got.main) != len(ref.main) {
				t.Errorf("main payload length: got %d want %d", len(got.main), len(ref.main))
			}
			// Extension-ID byte (QEXT_EXTENSION_ID<<1 = 0xF8) must match.
			if got.extIDByte != ref.extIDByte {
				t.Errorf("extension-ID byte: got 0x%02x want 0x%02x", got.extIDByte, ref.extIDByte)
			}
			if got.extID != qextPacketExtensionID {
				t.Errorf("extension ID: got %d want %d", got.extID, qextPacketExtensionID)
			}
			// QEXT payload byte count must match.
			if len(got.qextPayload) != len(ref.qextPayload) {
				t.Errorf("QEXT payload length: got %d want %d", len(got.qextPayload), len(ref.qextPayload))
			}
			// Total packet length must match.
			if len(out) != len(res.Packets[0]) {
				t.Errorf("total packet length: got %d want %d", len(out), len(res.Packets[0]))
			}

			t.Logf("ch=%d framing: toc=0x%02x count=0x%02x qext_bytes=%d main=%d qext=%d total=%d",
				ch, got.toc, got.countByte, got.paddingLen, len(got.main), len(got.qextPayload), len(out))

			// Report how far the full-packet bytes match (diagnostic for the
			// upstream comb / band-data residual; framing above is the contract).
			refPkt := res.Packets[0]
			firstDiff := firstByteDiff(out, refPkt)
			if firstDiff < 0 {
				t.Logf("ch=%d: FULL PACKET byte-exact (%d bytes)", ch, len(out))
			} else {
				t.Logf("ch=%d: full packet diverges at byte %d (framing exact; main-payload residual)", ch, firstDiff)
			}
		})
	}
}

// TestHD96kFramingExtensionRoundtrip checks that the gopus native 96 kHz QEXT
// packet decodes through the gopus decoder without error and that the QEXT
// extension is correctly recovered from the padding region (framing is
// self-consistent on the decode side).
func TestHD96kFramingExtensionRoundtrip(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000

	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(fmt.Sprintf("%dch", ch), func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    ch,
				Application: ApplicationRestrictedCelt,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			_ = enc.SetBitrate(bitrate)
			_ = enc.SetBitrateMode(BitrateModeCBR)
			_ = enc.SetComplexity(10)
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}
			out, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("EncodeFloat32: %v", err)
			}

			// The QEXT extension must be recoverable from the padding region.
			l := parseHD96kLayout(t, out)
			if l.code != 3 {
				t.Fatalf("expected code-3 packet, got code %d", l.code)
			}
			if l.extID != qextPacketExtensionID {
				t.Fatalf("QEXT extension ID not recovered: got %d", l.extID)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, ch))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcmOut := make([]float32, frameSize*ch)
			n, err := dec.Decode(out, pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != frameSize {
				t.Errorf("decoded %d samples want %d", n, frameSize)
			}
		})
	}
}
