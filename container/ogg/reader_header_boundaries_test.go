package ogg

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNewReaderRequiresOpusTagsContinuation(t *testing.T) {
	const serial = 0x2845
	tags := (&OpusTags{Vendor: strings.Repeat("v", 700), Comments: []string{"TITLE=test"}}).Encode()
	for _, missingPage := range []int{1, 2} {
		name := "middle page"
		if missingPage == 2 {
			name = "completion page"
		}
		t.Run(name, func(t *testing.T) {
			stream := readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, DefaultOpusHead(48000, 1).Encode())
			for i, start := 0, 0; start < len(tags); i, start = i+1, start+255 {
				end := min(start+255, len(tags))
				flags := byte(PageFlagContinuation)
				if i == 0 || i == missingPage {
					flags = 0
				}
				segments := []byte{255}
				granule := ^uint64(0)
				if end == len(tags) {
					segments = BuildSegmentTable(end - start)
					granule = 0
				}
				stream = append(stream, readerBoundaryPage(serial, uint32(i+1), flags, granule, segments, tags[start:end])...)
			}
			if _, err := NewReader(bytes.NewReader(stream)); !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("NewReader error = %v, want ErrInvalidPage", err)
			}
		})
	}
}

func TestNewReaderSkipsOtherStreamsDuringHeaders(t *testing.T) {
	const serial = 0x3845
	const otherSerial = 0x3533
	audio := []byte{0xf8, 0x11}
	for _, vendor := range []string{"gopus", strings.Repeat("v", 700)} {
		name := "single page tags"
		if len(vendor) > 255 {
			name = "continued tags"
		}
		t.Run(name, func(t *testing.T) {
			tags := (&OpusTags{Vendor: vendor, Comments: []string{"TITLE=test"}}).Encode()
			stream := readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, DefaultOpusHead(48000, 1).Encode())
			// RFC 3533 puts every grouped stream's BOS before its data pages.
			stream = append(stream, readerBoundaryPacketPage(otherSerial, 0, PageFlagBOS, 0, []byte("other codec"))...)
			seq := uint32(1)
			otherSeq := uint32(1)
			for start := 0; start < len(tags); start += 255 {
				end := min(start+255, len(tags))
				flags := byte(0)
				if start > 0 {
					flags = PageFlagContinuation
				}
				segments := []byte{255}
				granule := ^uint64(0)
				if end == len(tags) {
					segments = BuildSegmentTable(end - start)
					granule = 0
				}
				stream = append(stream, readerBoundaryPage(serial, seq, flags, granule, segments, tags[start:end])...)
				seq++
				otherFlags := byte(0)
				if end == len(tags) {
					otherFlags = PageFlagEOS
				}
				stream = append(stream, readerBoundaryPacketPage(otherSerial, otherSeq, otherFlags, 0, []byte("other data"))...)
				otherSeq++
			}
			stream = append(stream, readerBoundaryPacketPage(serial, seq, PageFlagEOS, 960, audio)...)
			for _, fragmented := range []bool{false, true} {
				var source io.Reader = bytes.NewReader(stream)
				if fragmented {
					source = &chunkCountingReader{data: stream, maxChunk: 7}
				}
				r, err := NewReader(source)
				if err != nil {
					t.Fatalf("fragmented=%v NewReader: %v", fragmented, err)
				}
				if r.Serial() != serial || r.Tags.Vendor != vendor {
					t.Fatalf("selected stream/tags = (%x, %q), want (%x, %q)", r.Serial(), r.Tags.Vendor, serial, vendor)
				}
				if title, ok := r.Tags.Value("TITLE"); !ok || title != "test" {
					t.Fatalf("TITLE = %q, present=%v, want test", title, ok)
				}
				if !fragmented {
					if err := r.SeekGranule(960); err != nil {
						t.Fatalf("SeekGranule: %v", err)
					}
				}
				dst := make([]byte, len(audio))
				n, granule, err := r.ReadPacketInto(dst)
				if err != nil || granule != 960 || !bytes.Equal(dst[:n], audio) {
					t.Fatalf("fragmented=%v packet = (%x, %d, %v), want (%x, 960, nil)", fragmented, dst[:n], granule, err, audio)
				}
				if _, _, err := r.ReadPacket(); !errors.Is(err, io.EOF) {
					t.Fatalf("trailing read = %v, want io.EOF", err)
				}
			}
		})
	}
}

func TestOggMultiplexedHeadersMatchOpusdec(t *testing.T) {
	if !checkOpusenc() || !checkOpusdec() {
		t.Skip("opusenc/opusdec (opus-tools) not available")
	}
	opts := libopusEncodeOpts{channels: 2, bitrateK: 96, signalKind: "sine", durFrames: 10, bigCommentLen: 70000}
	data, ok := extEncodeWithOpusenc(t, genPCM16(opts), opts)
	if !ok {
		t.Skip("opusenc could not run in this environment")
	}

	var multiplexed []byte
	var otherSerial, otherSeq uint32
	tagsPending := true
	tagsPages := 0
	for len(data) > 0 {
		page, n, err := ParsePage(data)
		if err != nil {
			t.Fatal(err)
		}
		multiplexed = append(multiplexed, data[:n]...)
		data = data[n:]
		if page.IsBOS() {
			otherSerial = page.SerialNumber ^ 0xffffffff
			multiplexed = append(multiplexed, readerBoundaryPacketPage(otherSerial, otherSeq, PageFlagBOS, 0, []byte("other codec"))...)
			otherSeq++
		} else if tagsPending {
			tagsPages++
			flags := byte(0)
			if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] < 255 {
				tagsPending = false
				flags = PageFlagEOS
			}
			multiplexed = append(multiplexed, readerBoundaryPacketPage(otherSerial, otherSeq, flags, 0, []byte("other data"))...)
			otherSeq++
		}
	}
	if tagsPages < 2 {
		t.Fatalf("OpusTags uses %d pages, want a continued header", tagsPages)
	}
	requireLibopusContainerParity(t, multiplexed)
}
