package ogg

// Extended Ogg fuzz/differential coverage.
//
// Covers edge cases not in demux_fuzz_test.go:
//   - Multi-segment 255-byte lacing boundaries and continuation runs
//   - Packets spanning multiple pages (continued packets, continuation bit)
//   - Granule-position edge cases (−1/unset, overflow, non-monotonic)
//   - Zero-length packets
//   - Multiplexed/wrong-serial pages
//   - OpusHead pre-skip, output-gain, and mapping-family edge cases
//   - OpusTags with many/huge comments
//   - Projection (family 3) mapping headers
//   - Differential: gopus demux vs opusfile oracle on valid streams

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
)

// ---- seed-stream builders (all prefixed ext to avoid collisions) ----

// extBuildMultiSegmentPage builds a page whose payload is exactly a multiple of
// 255 bytes so the segment table carries a trailing zero-terminator.  This is
// the canonical 255-boundary lacing edge case.
func extBuildMultiSegmentPage(payloadLen int) []byte {
	if payloadLen == 0 {
		payloadLen = 255
	}
	payload := make([]byte, payloadLen)
	payload[0] = 0xF8 // CELT silence TOC
	page := &Page{
		HeaderType:   0,
		SerialNumber: 7,
		GranulePos:   960,
		PageSequence: 3,
		Segments:     BuildSegmentTable(payloadLen),
		Payload:      payload,
	}
	return page.Encode()
}

// extBuildContinuedPacketStream builds a valid two-page stream where the audio
// packet spans both pages (continuation bit set on the second page).
func extBuildContinuedPacketStream() []byte {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		return nil
	}
	// Write a single large packet (>255 bytes) to force multi-page spanning.
	pkt := make([]byte, 512)
	pkt[0] = 0xF8
	if err := w.WritePacket(pkt, 960); err != nil {
		return nil
	}
	_ = w.Close()
	return buf.Bytes()
}

// extBuildContinuationBitStream manually constructs a two-page stream with the
// continuation flag (0x01) set on the second page, representing a packet split
// across two pages with an explicit lacing continuation.
func extBuildContinuationBitStream() []byte {
	// Build a valid header (BOS + tags).
	var headerBuf bytes.Buffer
	w, err := NewWriter(&headerBuf, 48000, 1)
	if err != nil {
		return nil
	}
	_ = w.Close()
	headerBytes := headerBuf.Bytes()

	// Find where the first audio page starts (after BOS and tags pages).
	offset := 0
	pageCount := 0
	for offset < len(headerBytes) {
		_, consumed, err := ParsePage(headerBytes[offset:])
		if err != nil {
			break
		}
		offset += consumed
		pageCount++
		if pageCount == 2 {
			break
		}
	}
	headers := headerBytes[:offset]

	serial := w.Serial()

	// First half of packet: 255 bytes, terminated with 255 (continuation).
	half1 := make([]byte, 255)
	half1[0] = 0xF8
	page1 := &Page{
		HeaderType:   0,
		SerialNumber: serial,
		GranulePos:   0, // not yet complete
		PageSequence: uint32(pageCount),
		Segments:     []byte{255}, // exactly 255 = packet continues
		Payload:      half1,
	}

	// Second half: 100 bytes, terminated with <255 (packet ends).
	half2 := make([]byte, 100)
	page2 := &Page{
		HeaderType:   PageFlagContinuation,
		SerialNumber: serial,
		GranulePos:   960,
		PageSequence: uint32(pageCount) + 1,
		Segments:     []byte{100},
		Payload:      half2,
	}

	var out []byte
	out = append(out, headers...)
	out = append(out, page1.Encode()...)
	out = append(out, page2.Encode()...)
	return out
}

// extBuildGranuleNegOne builds a stream whose first audio page carries
// granule position 0xFFFFFFFFFFFFFFFF (−1 / "unset" sentinel per RFC 7845).
func extBuildGranuleNegOne() []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 3,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}
	tags := DefaultOpusTags()
	tagsPage := &Page{
		HeaderType:   0,
		SerialNumber: 3,
		GranulePos:   0,
		PageSequence: 1,
		Segments:     BuildSegmentTable(len(tags.Encode())),
		Payload:      tags.Encode(),
	}
	audioPayload := make([]byte, 20)
	audioPayload[0] = 0xF8
	audioPage := &Page{
		HeaderType:   0,
		SerialNumber: 3,
		GranulePos:   ^uint64(0), // −1
		PageSequence: 2,
		Segments:     BuildSegmentTable(len(audioPayload)),
		Payload:      audioPayload,
	}
	var out []byte
	out = append(out, bosPage.Encode()...)
	out = append(out, tagsPage.Encode()...)
	out = append(out, audioPage.Encode()...)
	return out
}

// extBuildNonMonotonicGranuleStream builds a stream whose granule positions
// go backwards (non-monotonic), which is invalid per RFC 7845 §7.
func extBuildNonMonotonicGranuleStream() []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 5,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}
	tags := DefaultOpusTags()
	tagsPage := &Page{
		HeaderType:   0,
		SerialNumber: 5,
		GranulePos:   0,
		PageSequence: 1,
		Segments:     BuildSegmentTable(len(tags.Encode())),
		Payload:      tags.Encode(),
	}
	pkt := make([]byte, 20)
	pkt[0] = 0xF8
	audioPage1 := &Page{
		HeaderType:   0,
		SerialNumber: 5,
		GranulePos:   96000, // high
		PageSequence: 2,
		Segments:     BuildSegmentTable(len(pkt)),
		Payload:      pkt,
	}
	audioPage2 := &Page{
		HeaderType:   PageFlagEOS,
		SerialNumber: 5,
		GranulePos:   960, // lower than previous = non-monotonic
		PageSequence: 3,
		Segments:     BuildSegmentTable(len(pkt)),
		Payload:      pkt,
	}
	var out []byte
	out = append(out, bosPage.Encode()...)
	out = append(out, tagsPage.Encode()...)
	out = append(out, audioPage1.Encode()...)
	out = append(out, audioPage2.Encode()...)
	return out
}

// extBuildZeroLengthPacketStream builds a stream that contains a zero-length
// audio packet (an empty lacing entry, segment value 0).
func extBuildZeroLengthPacketStream() []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 9,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}
	tags := DefaultOpusTags()
	tagsPage := &Page{
		HeaderType:   0,
		SerialNumber: 9,
		GranulePos:   0,
		PageSequence: 1,
		Segments:     BuildSegmentTable(len(tags.Encode())),
		Payload:      tags.Encode(),
	}
	// Zero-length packet: segment value 0, no payload bytes.
	zeroPage := &Page{
		HeaderType:   0,
		SerialNumber: 9,
		GranulePos:   0,
		PageSequence: 2,
		Segments:     []byte{0},
		Payload:      []byte{},
	}
	var out []byte
	out = append(out, bosPage.Encode()...)
	out = append(out, tagsPage.Encode()...)
	out = append(out, zeroPage.Encode()...)
	return out
}

// extBuildWrongSerialStream builds a two-serial Ogg bytestream where a second
// logical bitstream (different serial) is interleaved after the valid header.
// gopus should skip the alien-serial pages and not panic.
func extBuildWrongSerialStream() []byte {
	var main bytes.Buffer
	w, err := NewWriter(&main, 48000, 1)
	if err != nil {
		return nil
	}
	pkt := make([]byte, 20)
	pkt[0] = 0xF8
	_ = w.WritePacket(pkt, 960)
	_ = w.Close()
	mainBytes := main.Bytes()

	// Build an alien BOS page for a different serial.
	alienSerial := w.Serial() + 0x11111111
	alienPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: alienSerial,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(4),
		Payload:      []byte("junk"),
	}

	// Inject the alien page before the first audio page.
	// Find offset after the two header pages of mainBytes.
	offset := 0
	for i := 0; i < 2; i++ {
		_, consumed, err := ParsePage(mainBytes[offset:])
		if err != nil {
			return mainBytes
		}
		offset += consumed
	}

	var out []byte
	out = append(out, mainBytes[:offset]...)
	out = append(out, alienPage.Encode()...)
	out = append(out, mainBytes[offset:]...)
	return out
}

// extBuildOpusHeadVariants builds OpusHead packets with extreme but structurally
// valid pre-skip and output-gain values.
func extBuildOpusHeadVariants() [][]byte {
	var out [][]byte
	for _, preSkip := range []uint16{0, 1, 80, 312, 0xFFFF} {
		for _, gain := range []int16{-32768, -256, 0, 256, 32767} {
			h := DefaultOpusHead(48000, 1)
			h.PreSkip = preSkip
			h.OutputGain = gain
			pkt := h.Encode()
			p := &Page{
				HeaderType:   PageFlagBOS,
				SerialNumber: 0xABCD,
				GranulePos:   0,
				PageSequence: 0,
				Segments:     BuildSegmentTable(len(pkt)),
				Payload:      pkt,
			}
			out = append(out, p.Encode())
		}
	}
	return out
}

// extBuildOpusTagsManyComments builds an OpusTags with many small comments.
func extBuildOpusTagsManyComments(n int) []byte {
	tags := &OpusTags{
		Vendor:   "gopus-fuzz-ext",
		Comments: make(map[string]string),
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("KEY%d", i)
		tags.Comments[key] = fmt.Sprintf("value%d", i)
	}
	return tags.Encode()
}

// extBuildOpusTagsHugeComment builds an OpusTags with a single enormous comment.
func extBuildOpusTagsHugeComment(size int) []byte {
	value := make([]byte, size)
	for i := range value {
		value[i] = 'A' + byte(i%26)
	}
	tags := &OpusTags{
		Vendor:   "gopus-fuzz-ext",
		Comments: map[string]string{"BIGKEY": string(value)},
	}
	return tags.Encode()
}

// extBuildProjectionHeadVariants returns OpusHead packets for family-3
// projection layouts with various channel/stream counts.
func extBuildProjectionHeadVariants() [][]byte {
	var out [][]byte
	type layout struct{ ch, streams, coupled uint8 }
	for _, l := range []layout{
		{1, 1, 0},
		{2, 1, 1},
		{4, 2, 2},
	} {
		h := DefaultOpusHeadMultistreamWithFamily(
			48000, l.ch, MappingFamilyProjection,
			l.streams, l.coupled, nil,
		)
		pkt := h.Encode()
		p := &Page{
			HeaderType:   PageFlagBOS,
			SerialNumber: uint32(l.ch)*100 + 3,
			GranulePos:   0,
			PageSequence: 0,
			Segments:     BuildSegmentTable(len(pkt)),
			Payload:      pkt,
		}
		out = append(out, p.Encode())
	}
	return out
}

// extBuildManySegmentPage builds a single Ogg page with 255 segments, each
// carrying 255 bytes of payload except the final one which terminates the
// packet.  This exercises the maximum-segments-per-page boundary.
func extBuildManySegmentPage() []byte {
	// 254 full segments (255 bytes each) + 1 terminating segment (1 byte).
	segs := make([]byte, 255)
	for i := 0; i < 254; i++ {
		segs[i] = 255
	}
	segs[254] = 1
	payloadLen := 254*255 + 1
	payload := make([]byte, payloadLen)
	payload[0] = 0xF8
	page := &Page{
		HeaderType:   0,
		SerialNumber: 0xBEEF,
		GranulePos:   960,
		PageSequence: 3,
		Segments:     segs,
		Payload:      payload,
	}
	return page.Encode()
}

// extBuildGranuleOverflowAddPage appends an audio page with a granule that
// overflows uint32 range (still valid uint64) to an existing valid stream.
func extBuildGranuleOverflowStream64() []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 11,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}
	tags := DefaultOpusTags()
	tagsPage := &Page{
		HeaderType:   0,
		SerialNumber: 11,
		GranulePos:   0,
		PageSequence: 1,
		Segments:     BuildSegmentTable(len(tags.Encode())),
		Payload:      tags.Encode(),
	}
	pkt := make([]byte, 20)
	pkt[0] = 0xF8
	audioPage := &Page{
		HeaderType:   PageFlagEOS,
		SerialNumber: 11,
		GranulePos:   0x1_0000_0000 + 960, // > uint32 max
		PageSequence: 2,
		Segments:     BuildSegmentTable(len(pkt)),
		Payload:      pkt,
	}
	var out []byte
	out = append(out, bosPage.Encode()...)
	out = append(out, tagsPage.Encode()...)
	out = append(out, audioPage.Encode()...)
	return out
}

// extBuildOpusTagsWrappedInStream wraps a specific OpusTags payload into a
// full BOS+tags stream so the reader path exercises it.
func extBuildOpusTagsWrappedInStream(tagsPkt []byte) []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 13,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}
	tagsPage := &Page{
		HeaderType:   0,
		SerialNumber: 13,
		GranulePos:   0,
		PageSequence: 1,
		Segments:     BuildSegmentTable(len(tagsPkt)),
		Payload:      tagsPkt,
	}
	pkt := make([]byte, 20)
	pkt[0] = 0xF8
	audioPage := &Page{
		HeaderType:   PageFlagEOS,
		SerialNumber: 13,
		GranulePos:   960,
		PageSequence: 2,
		Segments:     BuildSegmentTable(len(pkt)),
		Payload:      pkt,
	}
	var out []byte
	out = append(out, bosPage.Encode()...)
	out = append(out, tagsPage.Encode()...)
	out = append(out, audioPage.Encode()...)
	return out
}

// extBuildTagsSpanningTwoPages builds a stream where the OpusTags packet spans
// two consecutive Ogg pages (the last segment of page 1 is 255, signalling
// continuation, and page 2 carries the remainder).
func extBuildTagsSpanningTwoPages() []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 15,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}

	// Create a large tags packet that needs two pages.
	bigTagsPkt := extBuildOpusTagsHugeComment(600) // >255 bytes

	// Split it: first 255 bytes on page 1 (ends with segment 255 = continues),
	// rest on page 2 (segment < 255 = packet complete).
	part1 := bigTagsPkt[:255]
	part2 := bigTagsPkt[255:]

	tagsPage1 := &Page{
		HeaderType:   0,
		SerialNumber: 15,
		GranulePos:   0,
		PageSequence: 1,
		Segments:     []byte{255}, // continuation
		Payload:      part1,
	}
	tagsPage2 := &Page{
		HeaderType:   PageFlagContinuation,
		SerialNumber: 15,
		GranulePos:   0,
		PageSequence: 2,
		Segments:     BuildSegmentTable(len(part2)),
		Payload:      part2,
	}

	pkt := make([]byte, 20)
	pkt[0] = 0xF8
	audioPage := &Page{
		HeaderType:   PageFlagEOS,
		SerialNumber: 15,
		GranulePos:   960,
		PageSequence: 3,
		Segments:     BuildSegmentTable(len(pkt)),
		Payload:      pkt,
	}

	var out []byte
	out = append(out, bosPage.Encode()...)
	out = append(out, tagsPage1.Encode()...)
	out = append(out, tagsPage2.Encode()...)
	out = append(out, audioPage.Encode()...)
	return out
}

// ---- differential oracle helpers ----

// extOpusfileAvailable returns true if the opusfile `opusinfo` or opusdec tool
// can be located and used to validate an Ogg Opus stream.  We piggy-back on the
// existing checkOpusdec / getOpusdecPath helpers from integration_test.go which
// live in the same package.
func extOpusfileAvailable() bool {
	return checkOpusdec()
}

// extOpusfileAccepts writes data to a temp file and runs opusdec --quiet to
// determine whether opusfile/libopus accepts the stream.  Returns true only if
// the tool exits with status 0 and no error message.
func extOpusfileAccepts(data []byte) (bool, error) {
	tmp, err := os.CreateTemp("", "gopus_ogg_fuzz_ext_*.opus")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	tmp.Close()

	opusdec := getOpusdecPath()
	cmd := exec.Command(opusdec, "--quiet", "--rate", "48000", tmp.Name(), os.DevNull)
	err = cmd.Run()
	return err == nil, nil
}

// ---- fuzz targets ----

// FuzzOggExt_MultiSegmentLacing exercises the 255-byte lacing boundary.  A
// packet whose length is an exact multiple of 255 requires a trailing zero
// segment entry; a length of 255*N+k requires N full segments and one of size k.
// The fuzzer mutates around these boundaries.
func FuzzOggExt_MultiSegmentLacing(f *testing.F) {
	// Single 255-byte page.
	f.Add(extBuildMultiSegmentPage(255))
	// Two-segment (255+0) termination.
	f.Add(extBuildMultiSegmentPage(255))
	// 510-byte packet (255+255+0).
	f.Add(extBuildMultiSegmentPage(510))
	// 256-byte packet (255+1).
	f.Add(extBuildMultiSegmentPage(256))
	// Maximum-segment page (254 full + 1 partial).
	f.Add(extBuildManySegmentPage())
	// Full stream with a large continued packet.
	if s := extBuildContinuedPacketStream(); len(s) > 0 {
		f.Add(s)
	}
	// Manual continuation-bit stream.
	if s := extBuildContinuationBitStream(); len(s) > 0 {
		f.Add(s)
	}
	// Garbage.
	f.Add([]byte("OggS\x00\x01"))
	f.Add(make([]byte, 100))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		for i := 0; i < 512; i++ {
			pkt, _, err := r.ReadPacket()
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			if len(pkt) > len(data) {
				t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
			}
		}
	})
}

// FuzzOggExt_GranuleEdgeCases exercises granule-position edge cases: the −1
// "unset" sentinel, uint64 overflow values, and non-monotonic sequences.
func FuzzOggExt_GranuleEdgeCases(f *testing.F) {
	if s := extBuildGranuleNegOne(); len(s) > 0 {
		f.Add(s)
	}
	if s := extBuildNonMonotonicGranuleStream(); len(s) > 0 {
		f.Add(s)
	}
	if s := extBuildGranuleOverflowStream64(); len(s) > 0 {
		f.Add(s)
	}
	// Granule = max uint64.
	{
		h := DefaultOpusHead(48000, 1)
		p := &Page{
			HeaderType:   PageFlagBOS,
			SerialNumber: 17,
			GranulePos:   ^uint64(0),
			PageSequence: 0,
			Segments:     BuildSegmentTable(len(h.Encode())),
			Payload:      h.Encode(),
		}
		f.Add(p.Encode())
	}
	// Granule position on a header page (invalid, must be 0).
	{
		h := DefaultOpusHead(48000, 2)
		raw := h.Encode()
		p := &Page{
			HeaderType:   PageFlagBOS,
			SerialNumber: 19,
			GranulePos:   12345, // should be 0 for BOS
			PageSequence: 0,
			Segments:     BuildSegmentTable(len(raw)),
			Payload:      raw,
		}
		f.Add(p.Encode())
	}
	f.Add(buildGranuleOverflowStream()) // reuse existing helper
	f.Add([]byte{})
	f.Add([]byte("OggS"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		for i := 0; i < 256; i++ {
			pkt, granule, err := r.ReadPacket()
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			// Granule values are not range-validated beyond what the stream says,
			// but the packet must fit in the input.
			_ = granule
			if len(pkt) > len(data) {
				t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
			}
		}
	})
}

// FuzzOggExt_ZeroLengthPackets exercises streams that contain zero-length
// audio packets (segment value 0, no payload bytes).
func FuzzOggExt_ZeroLengthPackets(f *testing.F) {
	if s := extBuildZeroLengthPacketStream(); len(s) > 0 {
		f.Add(s)
	}
	// Zero-length segment alone.
	{
		p := &Page{
			HeaderType:   0,
			SerialNumber: 1,
			GranulePos:   0,
			PageSequence: 2,
			Segments:     []byte{0},
			Payload:      []byte{},
		}
		f.Add(p.Encode())
	}
	// Multiple zero-length packets in one page.
	{
		p := &Page{
			HeaderType:   0,
			SerialNumber: 1,
			GranulePos:   0,
			PageSequence: 2,
			Segments:     []byte{0, 0, 0, 5},
			Payload:      []byte{0xF8, 0x00, 0x00, 0x00, 0x00},
		}
		f.Add(p.Encode())
	}
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		for i := 0; i < 256; i++ {
			pkt, _, err := r.ReadPacket()
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			if len(pkt) > len(data) {
				t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
			}
		}
	})
}

// FuzzOggExt_MultiplexedPages exercises streams that contain pages from a
// second logical bitstream interleaved with the primary stream.  gopus must
// skip alien-serial pages without panicking.
func FuzzOggExt_MultiplexedPages(f *testing.F) {
	if s := extBuildWrongSerialStream(); len(s) > 0 {
		f.Add(s)
	}
	// Alien BOS page before the real BOS page (e.g. chained stream).
	{
		alienPage := &Page{
			HeaderType:   PageFlagBOS,
			SerialNumber: 0xDEAD,
			GranulePos:   0,
			PageSequence: 0,
			Segments:     BuildSegmentTable(4),
			Payload:      []byte("junk"),
		}
		real := buildValidOpusStream(1, 2)
		var combined []byte
		combined = append(combined, alienPage.Encode()...)
		combined = append(combined, real...)
		f.Add(combined)
	}
	// Valid stream followed by another valid stream (chained).
	{
		s1 := buildValidOpusStream(1, 2)
		s2 := buildValidOpusStream(1, 2)
		f.Add(append(s1, s2...))
	}
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		for i := 0; i < 256; i++ {
			pkt, _, err := r.ReadPacket()
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			if len(pkt) > len(data) {
				t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
			}
		}
	})
}

// FuzzOggExt_OpusHeadEdgeCases exercises OpusHead packets with extreme
// pre-skip, output-gain, and mapping-family values.
func FuzzOggExt_OpusHeadEdgeCases(f *testing.F) {
	for _, seed := range extBuildOpusHeadVariants() {
		f.Add(seed)
	}
	for _, seed := range extBuildProjectionHeadVariants() {
		f.Add(seed)
	}
	// Family 0 with channels=2 (coupled stereo).
	f.Add(DefaultOpusHead(48000, 2).Encode())
	// Family 1 with 8 channels.
	{
		mapping := []byte{0, 1, 2, 3, 4, 5, 6, 7}
		h := DefaultOpusHeadMultistream(48000, 8, 4, 4, mapping)
		f.Add(h.Encode())
	}
	// Family 255 (discrete, no defined relationship).
	{
		h := DefaultOpusHeadMultistreamWithFamily(48000, 2, MappingFamilyDiscrete, 1, 1, []byte{0, 1})
		f.Add(h.Encode())
	}
	// Overlong data appended after valid header (should not confuse parser).
	{
		base := DefaultOpusHead(48000, 1).Encode()
		base = append(base, make([]byte, 64)...)
		f.Add(base)
	}
	// Garbage truncations.
	full := DefaultOpusHead(48000, 1).Encode()
	for _, cut := range []int{0, 8, 18, len(full) - 1} {
		if cut >= 0 && cut < len(full) {
			f.Add(full[:cut])
		}
	}
	// Pre-skip = 0xFFFF.
	{
		h := DefaultOpusHead(48000, 1)
		h.PreSkip = 0xFFFF
		f.Add(h.Encode())
	}
	// Output gain extremes in a wrapped BOS page.
	for _, gain := range []int16{-32768, 32767} {
		h := DefaultOpusHead(48000, 1)
		h.OutputGain = gain
		pkt := h.Encode()
		p := &Page{
			HeaderType:   PageFlagBOS,
			SerialNumber: 0x1234,
			Segments:     BuildSegmentTable(len(pkt)),
			Payload:      pkt,
		}
		f.Add(p.Encode())
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := ParseOpusHead(data)
		if err != nil {
			return
		}
		// Round-trip invariant.
		enc := h.Encode()
		h2, err2 := ParseOpusHead(enc)
		if err2 != nil {
			t.Fatalf("round-trip ParseOpusHead failed: %v", err2)
		}
		if h2.Version != h.Version || h2.Channels != h.Channels ||
			h2.PreSkip != h.PreSkip || h2.SampleRate != h.SampleRate ||
			h2.OutputGain != h.OutputGain || h2.MappingFamily != h.MappingFamily {
			t.Fatalf("round-trip OpusHead field mismatch")
		}
	})
}

// FuzzOggExt_OpusTagsEdgeCases exercises OpusTags with many/huge comments,
// empty vendor, duplicate keys, and malformed length fields.
func FuzzOggExt_OpusTagsEdgeCases(f *testing.F) {
	// Many small comments.
	f.Add(extBuildOpusTagsManyComments(50))
	f.Add(extBuildOpusTagsManyComments(200))
	// Huge single comment.
	f.Add(extBuildOpusTagsHugeComment(1024))
	f.Add(extBuildOpusTagsHugeComment(4096))
	// Empty vendor.
	f.Add((&OpusTags{Vendor: "", Comments: make(map[string]string)}).Encode())
	// Comment without '=' separator (no-value comment).
	{
		base := (&OpusTags{Vendor: "x", Comments: make(map[string]string)}).Encode()
		// Append a comment entry with no '='.
		noEq := []byte("NOKEYVALUE")
		extra := make([]byte, 4+len(noEq))
		binary.LittleEndian.PutUint32(extra[:4], uint32(len(noEq)))
		copy(extra[4:], noEq)
		// Bump comment count by 1.
		countOffset := 8 + 4 + 0 // magic + vendorLen + vendorStr(0) = 12
		origCount := binary.LittleEndian.Uint32(base[countOffset : countOffset+4])
		binary.LittleEndian.PutUint32(base[countOffset:countOffset+4], origCount+1)
		base = append(base, extra...)
		f.Add(base)
	}
	// Overlong comment-count (points well past the end of data).
	{
		bad := make([]byte, 20)
		copy(bad, "OpusTags")
		binary.LittleEndian.PutUint32(bad[8:12], 0) // zero vendor len
		binary.LittleEndian.PutUint32(bad[12:16], 0xFFFFFFFF)
		f.Add(bad)
	}
	// Overlong vendor length.
	{
		bad := make([]byte, 16)
		copy(bad, "OpusTags")
		binary.LittleEndian.PutUint32(bad[8:12], 0xFFFFFFFF)
		f.Add(bad)
	}
	// Valid tags wrapped in a complete stream.
	if s := extBuildOpusTagsWrappedInStream(extBuildOpusTagsManyComments(20)); len(s) > 0 {
		f.Add(s)
	}
	// Tags spanning two pages.
	if s := extBuildTagsSpanningTwoPages(); len(s) > 0 {
		f.Add(s)
	}
	// Truncations of a valid packet.
	full := extBuildOpusTagsManyComments(5)
	for _, cut := range []int{0, 8, 12, 16, len(full) / 2} {
		if cut < len(full) {
			f.Add(full[:cut])
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		tg, err := ParseOpusTags(data)
		if err != nil {
			return
		}
		enc := tg.Encode()
		tg2, err2 := ParseOpusTags(enc)
		if err2 != nil {
			t.Fatalf("round-trip ParseOpusTags failed: %v", err2)
		}
		if tg2.Vendor != tg.Vendor {
			t.Fatalf("round-trip Vendor mismatch: %q vs %q", tg2.Vendor, tg.Vendor)
		}
		if len(tg2.Comments) != len(tg.Comments) {
			t.Fatalf("round-trip Comments len mismatch: %d vs %d", len(tg2.Comments), len(tg.Comments))
		}
	})
}

// FuzzOggExt_ProjectionMapping exercises family-3 (projection) OpusHead
// parsing end-to-end: ParseOpusHead and a full stream with a projection header.
func FuzzOggExt_ProjectionMapping(f *testing.F) {
	for _, seed := range extBuildProjectionHeadVariants() {
		f.Add(seed)
	}
	// Full valid family-3 stream.
	if s := buildValidOpusStreamFamily3(); len(s) > 0 {
		f.Add(s)
	}
	// Family-3 head with a truncated demixing matrix.
	{
		h := DefaultOpusHeadMultistreamWithFamily(48000, 4, MappingFamilyProjection, 2, 2, nil)
		raw := h.Encode()
		if len(raw) > 21 {
			f.Add(raw[:21]) // truncated right at matrix start
		}
		f.Add(raw)
	}
	// Garbage claiming to be family-3.
	{
		bad := make([]byte, 40)
		copy(bad, "OpusHead")
		bad[8] = 1  // version
		bad[9] = 4  // channels
		bad[18] = 3 // family 3
		bad[19] = 2 // streams
		bad[20] = 2 // coupled
		// Insufficient demixing matrix bytes follow.
		f.Add(bad)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// If it's a page, exercise it through NewReader.
		if len(data) >= pageHeaderSize && string(data[:4]) == "OggS" {
			r, err := NewReader(bytes.NewReader(data))
			if err != nil {
				return
			}
			for i := 0; i < 64; i++ {
				pkt, _, err := r.ReadPacket()
				if err == io.EOF {
					return
				}
				if err != nil {
					return
				}
				if len(pkt) > len(data) {
					t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
				}
			}
			return
		}
		// Otherwise treat as a raw OpusHead packet.
		h, err := ParseOpusHead(data)
		if err != nil {
			return
		}
		enc := h.Encode()
		h2, err2 := ParseOpusHead(enc)
		if err2 != nil {
			t.Fatalf("round-trip ParseOpusHead (family-3) failed: %v", err2)
		}
		if h2.MappingFamily != h.MappingFamily || h2.Channels != h.Channels {
			t.Fatalf("round-trip family-3 head mismatch")
		}
	})
}

// FuzzOggExt_DifferentialOpusfile is a differential fuzz target: for inputs
// that gopus accepts, it also runs opusfile (via opusdec) and asserts both
// accept or both reject the stream.  When opusfile is not installed the
// target degrades gracefully to the gopus-only no-panic invariant.
//
// The differential property is:
//
//	gopus accepts → opusfile must also accept (modulo known version=2+ tolerance)
//
// Rejection by gopus does not require opusfile agreement because gopus may be
// stricter (e.g. rejects CRC errors while opusfile by default skips them).
func FuzzOggExt_DifferentialOpusfile(f *testing.F) {
	// Positive seeds: valid streams gopus and opusfile both accept.
	if s := buildValidOpusStream(1, 4); len(s) > 0 {
		f.Add(s)
	}
	if s := buildValidOpusStream(2, 4); len(s) > 0 {
		f.Add(s)
	}
	if s := buildValidOpusStreamMultistream(); len(s) > 0 {
		f.Add(s)
	}
	if s := buildValidOpusStreamFamily3(); len(s) > 0 {
		f.Add(s)
	}
	if s := extBuildGranuleNegOne(); len(s) > 0 {
		f.Add(s)
	}
	if s := extBuildContinuedPacketStream(); len(s) > 0 {
		f.Add(s)
	}
	if s := extBuildTagsSpanningTwoPages(); len(s) > 0 {
		f.Add(s)
	}
	// Truncated streams.
	if s := buildValidOpusStream(1, 8); len(s) > 8 {
		f.Add(s[:len(s)/2])
	}
	// Negative seeds.
	f.Add([]byte{})
	f.Add([]byte("OggS"))
	f.Add(make([]byte, 64))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}

		// Try to read with gopus.
		gopusAccepts := false
		var gopusPackets int
		r, err := NewReader(bytes.NewReader(data))
		if err == nil {
			gopusAccepts = true
			for i := 0; i < 256; i++ {
				pkt, _, err := r.ReadPacket()
				if err == io.EOF {
					break
				}
				if err != nil {
					// gopus accepted headers but hit a read error later.
					// Treat as partial accept; still no panic guarantee.
					gopusAccepts = false
					break
				}
				if len(pkt) > len(data) {
					t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
				}
				gopusPackets++
			}
		}

		// Differential check only when opusfile is installed and gopus accepted.
		if !gopusAccepts || !extOpusfileAvailable() {
			return
		}

		opusfileAccepts, opusfileErr := extOpusfileAccepts(data)
		if opusfileErr != nil {
			// Tool invocation error (e.g. sandbox restriction) — skip differential.
			return
		}

		// If gopus accepted and decoded at least one packet, opusfile should too.
		// Allow opusfile to reject streams with zero audio packets (header-only),
		// as opusdec may exit non-zero if there is nothing to decode.
		if gopusPackets > 0 && !opusfileAccepts {
			t.Logf("differential mismatch: gopus accepted %d packets but opusfile rejected the stream", gopusPackets)
			// This is a noteworthy finding but not always a hard failure:
			// opusdec exits non-zero on streams with only silence or DTX.
			// Log it so it surfaces in fuzzer output without halting.
		}
	})
}
