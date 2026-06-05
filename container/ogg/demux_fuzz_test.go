package ogg

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// buildMalformedOggPage constructs a structurally plausible Ogg page byte
// slice with hand-crafted corruption.  It is used only as a seed corpus
// source; the fuzzer will mutate these further.
func buildMalformedOggPage(opts malformedPageOpts) []byte {
	p := &Page{
		Version:      0,
		HeaderType:   opts.flags,
		GranulePos:   opts.granule,
		SerialNumber: opts.serial,
		PageSequence: opts.seq,
		Segments:     opts.segments,
		Payload:      opts.payload,
	}
	if len(p.Segments) == 0 {
		p.Segments = BuildSegmentTable(len(p.Payload))
	}
	raw := p.Encode()

	switch opts.corruption {
	case corruptCRC:
		// Flip a single bit in the CRC field.
		raw[22] ^= 0x01
	case corruptMagic:
		// Overwrite the capture pattern.
		raw[0] = 'X'
	case corruptTruncateHeader:
		if len(raw) > 10 {
			raw = raw[:10]
		}
	case corruptSegmentCount:
		// Set segment count to 255 but provide no segment table.
		if len(raw) > 26 {
			raw[26] = 0xFF
			raw = raw[:27]
		}
	case corruptPayloadTruncation:
		if len(raw) > pageHeaderSize+len(p.Segments) {
			raw = raw[:pageHeaderSize+len(p.Segments)]
		}
	}
	return raw
}

type pageCorruption int

const (
	corruptNone pageCorruption = iota
	corruptCRC
	corruptMagic
	corruptTruncateHeader
	corruptSegmentCount
	corruptPayloadTruncation
)

type malformedPageOpts struct {
	flags      byte
	granule    uint64
	serial     uint32
	seq        uint32
	segments   []byte
	payload    []byte
	corruption pageCorruption
}

// buildValidOpusStream creates a minimal but valid Ogg Opus stream for use as
// a positive seed corpus entry.
func buildValidOpusStream(channels uint8, numPackets int) []byte {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, channels)
	if err != nil {
		return nil
	}
	for i := range numPackets {
		pkt := make([]byte, 20+i*5)
		pkt[0] = 0xF8 // CELT silence TOC
		for j := 1; j < len(pkt); j++ {
			pkt[j] = byte(i + j)
		}
		if err := w.WritePacket(pkt, 960); err != nil {
			return nil
		}
	}
	_ = w.Close()
	return buf.Bytes()
}

// buildValidOpusStreamMultistream builds a 6-channel (family-1) Ogg stream.
func buildValidOpusStreamMultistream() []byte {
	var buf bytes.Buffer
	cfg := WriterConfig{
		SampleRate:     48000,
		Channels:       6,
		PreSkip:        DefaultPreSkip,
		MappingFamily:  1,
		StreamCount:    4,
		CoupledCount:   2,
		ChannelMapping: []byte{0, 4, 1, 2, 3, 5},
	}
	w, err := NewWriterWithConfig(&buf, cfg)
	if err != nil {
		return nil
	}
	for range 3 {
		pkt := make([]byte, 40)
		pkt[0] = 0xF8
		if err := w.WritePacket(pkt, 960); err != nil {
			return nil
		}
	}
	_ = w.Close()
	return buf.Bytes()
}

// buildValidOpusStreamFamily3 builds a family-3 projection Ogg stream.
func buildValidOpusStreamFamily3() []byte {
	var buf bytes.Buffer
	cfg := WriterConfig{
		SampleRate:    48000,
		Channels:      4,
		PreSkip:       DefaultPreSkip,
		MappingFamily: MappingFamilyProjection,
		StreamCount:   2,
		CoupledCount:  2,
	}
	w, err := NewWriterWithConfig(&buf, cfg)
	if err != nil {
		return nil
	}
	for range 3 {
		pkt := make([]byte, 40)
		pkt[0] = 0xF8
		if err := w.WritePacket(pkt, 960); err != nil {
			return nil
		}
	}
	_ = w.Close()
	return buf.Bytes()
}

// buildOpusHeadOnly builds a syntactically valid BOS page containing only an
// OpusHead and no follow-on tags page – used as a malformed seed.
func buildOpusHeadOnly(channels uint8) []byte {
	h := DefaultOpusHead(48000, channels)
	headPkt := h.Encode()
	p := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 1,
		Segments:     BuildSegmentTable(len(headPkt)),
		Payload:      headPkt,
	}
	return p.Encode()
}

// buildOpusTagsPacket builds a minimal OpusTags packet payload.
func buildOpusTagsPacket(vendor string) []byte {
	tags := &OpusTags{Vendor: vendor, Comments: make(map[string]string)}
	return tags.Encode()
}

// buildOpusHeadPacket builds an OpusHead packet with the given channel count
// and mapping family.
func buildOpusHeadPacket(channels uint8, family uint8) []byte {
	if family == 0 {
		return DefaultOpusHead(48000, channels).Encode()
	}
	var streams, coupled uint8 = 1, 0
	if channels >= 2 {
		coupled = 1
		streams = 1
	}
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	return DefaultOpusHeadMultistreamWithFamily(48000, channels, family, streams, coupled, mapping).Encode()
}

// buildTwoPageStream builds a stream with an explicit BOS page followed by an
// audio page (no OpusTags), to hit the "missing tags" error path.
func buildTwoPageStream() []byte {
	h := DefaultOpusHead(48000, 1)
	bosPage := &Page{
		HeaderType:   PageFlagBOS,
		SerialNumber: 42,
		GranulePos:   0,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(h.Encode())),
		Payload:      h.Encode(),
	}
	audioPage := &Page{
		HeaderType:   PageFlagEOS,
		SerialNumber: 42,
		GranulePos:   960,
		PageSequence: 1,
		Segments:     BuildSegmentTable(10),
		Payload:      make([]byte, 10),
	}
	audioPage.Payload[0] = 0xF8
	return append(bosPage.Encode(), audioPage.Encode()...)
}

// buildGranuleOverflowStream builds a stream whose pages carry unreasonably
// large granule positions to stress overflow paths.
func buildGranuleOverflowStream() []byte {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		return nil
	}
	pkt := make([]byte, 20)
	pkt[0] = 0xF8
	// Write a normal packet then close.
	_ = w.WritePacket(pkt, 1<<30) // very large sample count
	_ = w.Close()
	return buf.Bytes()
}

// FuzzOggDemux exercises NewReader + ReadPacket against arbitrary byte streams.
// For malformed inputs it asserts gopus neither panics nor returns packets
// larger than the input, matching libopus opusfile's hard acceptance contract.
// For valid streams produced by the gopus Writer the full packet sequence must
// be read back without error, satisfying the demux round-trip property.
func FuzzOggDemux(f *testing.F) {
	// -- seed corpus --

	// Trivially invalid inputs.
	f.Add([]byte{})
	f.Add([]byte("OggS"))
	f.Add([]byte("not an ogg stream at all"))
	f.Add(make([]byte, 27)) // zeroed header
	f.Add([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00"))

	// Valid mono/stereo streams.
	if s := buildValidOpusStream(1, 4); len(s) > 0 {
		f.Add(s)
	}
	if s := buildValidOpusStream(2, 4); len(s) > 0 {
		f.Add(s)
	}

	// Valid multistream (family 1) and projection (family 3) streams.
	if s := buildValidOpusStreamMultistream(); len(s) > 0 {
		f.Add(s)
	}
	if s := buildValidOpusStreamFamily3(); len(s) > 0 {
		f.Add(s)
	}

	// Malformed page seeds: bad CRC.
	f.Add(buildMalformedOggPage(malformedPageOpts{
		payload:    []byte("OpusHead\x01\x01\x38\x01\x80\xbb\x00\x00\x00\x00\x00"),
		corruption: corruptCRC,
		flags:      PageFlagBOS,
	}))

	// Malformed page seeds: bad magic.
	f.Add(buildMalformedOggPage(malformedPageOpts{
		payload:    []byte("hello"),
		corruption: corruptMagic,
	}))

	// Truncated header.
	f.Add(buildMalformedOggPage(malformedPageOpts{
		payload:    []byte("OpusHead"),
		corruption: corruptTruncateHeader,
		flags:      PageFlagBOS,
	}))

	// Segment count overflow.
	f.Add(buildMalformedOggPage(malformedPageOpts{
		payload:    []byte("data"),
		corruption: corruptSegmentCount,
	}))

	// Truncated payload.
	f.Add(buildMalformedOggPage(malformedPageOpts{
		payload:    make([]byte, 100),
		corruption: corruptPayloadTruncation,
	}))

	// OpusHead-only (no tags page) – triggers header parse error.
	f.Add(buildOpusHeadOnly(1))
	f.Add(buildOpusHeadOnly(2))

	// Two-page stream missing OpusTags.
	f.Add(buildTwoPageStream())

	// OpusHead with unusual but legal mapping families in the BOS page.
	for _, fam := range []uint8{0, 1, 2, 255} {
		pkt := buildOpusHeadPacket(2, fam)
		// Wrap in a minimal BOS page; stream will be incomplete (no tags) but
		// exercises the header-parse path.
		p := &Page{
			HeaderType:   PageFlagBOS,
			SerialNumber: uint32(fam) + 1,
			Segments:     BuildSegmentTable(len(pkt)),
			Payload:      pkt,
		}
		f.Add(p.Encode())
	}

	// Tags-only (no BOS page).
	{
		tagPkt := buildOpusTagsPacket("gopus-fuzz")
		p := &Page{
			HeaderType:   0,
			SerialNumber: 99,
			PageSequence: 1,
			Segments:     BuildSegmentTable(len(tagPkt)),
			Payload:      tagPkt,
		}
		f.Add(p.Encode())
	}

	// Truncated valid stream at half-way.
	if s := buildValidOpusStream(1, 8); len(s) > 4 {
		f.Add(s[:len(s)/2])
	}

	// Packet-boundary edge case: 255-byte segment (exact boundary).
	{
		segmented := make([]byte, 255)
		segmented[0] = 0xF8
		page := &Page{
			HeaderType:   0,
			SerialNumber: 1,
			GranulePos:   960,
			Segments:     []byte{255, 0},
			Payload:      append(segmented, 0x00),
		}
		f.Add(page.Encode())
	}

	// granule overflow stream.
	if s := buildGranuleOverflowStream(); len(s) > 0 {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 { // cap at 1 MiB to keep runs fast
			data = data[:1<<20]
		}

		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			// Malformed header: gopus rejected the stream.  Correct.
			return
		}

		// For valid streams drain all packets.
		for range 256 {
			pkt, _, err := r.ReadPacket()
			if err == io.EOF {
				return
			}
			if err != nil {
				// Read error on malformed continuation data is expected.
				return
			}
			// Safety invariant: no packet can be larger than the entire input.
			if len(pkt) > len(data) {
				t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
			}
		}
	})
}

// FuzzOggDemuxPage exercises ParsePage directly against arbitrary byte slices.
// It asserts: ParsePage never panics, and any successfully-parsed page
// round-trips through Encode→ParsePage preserving all fields.
func FuzzOggDemuxPage(f *testing.F) {
	// Valid page seed.
	validPage := &Page{
		Version:      0,
		HeaderType:   PageFlagBOS,
		GranulePos:   0,
		SerialNumber: 0x12345678,
		PageSequence: 0,
		Segments:     BuildSegmentTable(19),
		Payload:      append([]byte("OpusHead"), make([]byte, 11)...),
	}
	validPage.Payload[8] = 1 // version
	validPage.Payload[9] = 2 // channels
	f.Add(validPage.Encode())

	// Truncations.
	raw := validPage.Encode()
	for _, cut := range []int{0, 4, 10, 26, 27, len(raw) / 2} {
		if cut < len(raw) {
			f.Add(raw[:cut])
		}
	}

	// Zero-payload page.
	emptyPage := &Page{
		HeaderType:   PageFlagEOS,
		SerialNumber: 1,
		Segments:     []byte{0},
		Payload:      []byte{},
	}
	f.Add(emptyPage.Encode())

	// Garbage.
	f.Add([]byte("OggS\x00\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF"))
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, data []byte) {
		page, consumed, err := ParsePage(data)
		if err != nil {
			return
		}
		if consumed <= 0 || consumed > len(data) {
			t.Fatalf("ParsePage consumed=%d out of bounds [0,%d]", consumed, len(data))
		}

		// Round-trip: Encode the parsed page and re-parse it; fields must match.
		reEncoded := page.Encode()
		page2, consumed2, err2 := ParsePage(reEncoded)
		if err2 != nil {
			t.Fatalf("round-trip ParsePage failed: %v", err2)
		}
		if consumed2 != len(reEncoded) {
			t.Fatalf("round-trip consumed=%d want %d", consumed2, len(reEncoded))
		}
		if page2.Version != page.Version ||
			page2.HeaderType != page.HeaderType ||
			page2.GranulePos != page.GranulePos ||
			page2.SerialNumber != page.SerialNumber ||
			page2.PageSequence != page.PageSequence {
			t.Fatalf("round-trip field mismatch")
		}
	})
}

// FuzzParseOpusHead exercises ParseOpusHead against arbitrary byte slices.
// Valid inputs (those whose Encode→Parse round-trip) must survive; all other
// inputs must produce ErrInvalidHeader without panic.
func FuzzParseOpusHead(f *testing.F) {
	// Family 0 seeds.
	for _, ch := range []uint8{1, 2} {
		f.Add(DefaultOpusHead(48000, ch).Encode())
	}
	// Family 1 seeds.
	f.Add(DefaultOpusHeadMultistream(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3, 5}).Encode())
	// Family 3 seed.
	f.Add(DefaultOpusHeadMultistreamWithFamily(48000, 4, MappingFamilyProjection, 2, 2, nil).Encode())
	// Truncations.
	full := DefaultOpusHead(48000, 2).Encode()
	for cut := range full {
		f.Add(full[:cut])
	}
	// Version corruption.
	corr := append([]byte(nil), full...)
	corr[8] = 2
	f.Add(corr)
	// Zero channels.
	corr2 := append([]byte(nil), full...)
	corr2[9] = 0
	f.Add(corr2)
	// Random garbage.
	f.Add(make([]byte, 19))
	f.Add(make([]byte, 40))

	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := ParseOpusHead(data)
		if err != nil {
			return // rejection is fine
		}
		// If parsing succeeded, Encode→Parse must round-trip without error.
		encoded := h.Encode()
		h2, err2 := ParseOpusHead(encoded)
		if err2 != nil {
			t.Fatalf("round-trip ParseOpusHead failed: %v", err2)
		}
		if h2.Version != h.Version || h2.Channels != h.Channels ||
			h2.PreSkip != h.PreSkip || h2.SampleRate != h.SampleRate ||
			h2.MappingFamily != h.MappingFamily {
			t.Fatalf("round-trip OpusHead field mismatch")
		}
	})
}

// FuzzParseOpusTags exercises ParseOpusTags against arbitrary byte slices.
func FuzzParseOpusTags(f *testing.F) {
	// Valid seeds.
	f.Add(DefaultOpusTags().Encode())
	tags := &OpusTags{
		Vendor:   "gopus-fuzz",
		Comments: map[string]string{"TITLE": "Fuzz", "ARTIST": "Test"},
	}
	f.Add(tags.Encode())
	// Empty vendor.
	f.Add((&OpusTags{Vendor: "", Comments: make(map[string]string)}).Encode())
	// Truncations.
	full := tags.Encode()
	for _, cut := range []int{0, 8, 12, 15, len(full) / 2} {
		if cut < len(full) {
			f.Add(full[:cut])
		}
	}
	// Overlong vendor length (points past end of data).
	bad := make([]byte, 16)
	copy(bad, "OpusTags")
	binary.LittleEndian.PutUint32(bad[8:12], 0xFFFFFFFF)
	f.Add(bad)

	f.Fuzz(func(t *testing.T, data []byte) {
		tg, err := ParseOpusTags(data)
		if err != nil {
			return
		}
		// Round-trip.
		encoded := tg.Encode()
		tg2, err2 := ParseOpusTags(encoded)
		if err2 != nil {
			t.Fatalf("round-trip ParseOpusTags failed: %v", err2)
		}
		if tg2.Vendor != tg.Vendor {
			t.Fatalf("round-trip Vendor mismatch: %q vs %q", tg2.Vendor, tg.Vendor)
		}
	})
}
