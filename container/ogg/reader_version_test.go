package ogg

import (
	"bytes"
	"errors"
	"testing"
)

const readerVersionTestSerial = 0x41728394

func readerVersionTestPage(version byte, sequence uint32, flags byte, granule uint64, packet []byte) []byte {
	return (&Page{
		Version:      version,
		HeaderType:   flags,
		GranulePos:   granule,
		SerialNumber: readerVersionTestSerial,
		PageSequence: sequence,
		Segments:     BuildSegmentTable(len(packet)),
		Payload:      packet,
	}).Encode()
}

func TestReaderRejectsNonzeroOggPageVersion(t *testing.T) {
	head := DefaultOpusHead(48000, 1).Encode()
	tags := DefaultOpusTags().Encode()
	audio := []byte{0xf8, 0x00}
	buildStream := func(headVersion, tagsVersion, audioVersion byte) []byte {
		stream := readerVersionTestPage(headVersion, 0, PageFlagBOS, 0, head)
		stream = append(stream, readerVersionTestPage(tagsVersion, 1, 0, 0, tags)...)
		stream = append(stream, readerVersionTestPage(audioVersion, 2, PageFlagEOS, 960, audio)...)
		return stream
	}

	for _, tc := range []struct {
		name                                   string
		headVersion, tagsVersion, audioVersion byte
		badAtHeader                            bool
	}{
		{name: "BOS page", headVersion: 1, tagsVersion: 0, audioVersion: 0, badAtHeader: true},
		{name: "OpusTags page", headVersion: 0, tagsVersion: 1, audioVersion: 0, badAtHeader: true},
		{name: "audio page", headVersion: 0, tagsVersion: 0, audioVersion: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewReader(bytes.NewReader(buildStream(tc.headVersion, tc.tagsVersion, tc.audioVersion)))
			if tc.badAtHeader {
				if !errors.Is(err, ErrInvalidPage) {
					t.Fatalf("NewReader error = %v, want ErrInvalidPage", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewReader() error = %v", err)
			}
			if _, _, err := r.ReadPacket(); !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("ReadPacket() error = %v, want ErrInvalidPage", err)
			}
		})
	}

	t.Run("version zero control", func(t *testing.T) {
		r, err := NewReader(bytes.NewReader(buildStream(0, 0, 0)))
		if err != nil {
			t.Fatalf("NewReader() error = %v", err)
		}
		packet, granule, err := r.ReadPacket()
		if err != nil || !bytes.Equal(packet, audio) || granule != 960 {
			t.Fatalf("ReadPacket() = (%x, %d, %v), want (%x, 960, nil)", packet, granule, err, audio)
		}
	})

	t.Run("low-level parser preserves version", func(t *testing.T) {
		data := readerVersionTestPage(1, 0, PageFlagBOS, 0, head)
		page, consumed, err := ParsePage(data)
		if err != nil {
			t.Fatalf("ParsePage() error = %v", err)
		}
		if page.Version != 1 || consumed != len(data) {
			t.Fatalf("ParsePage() version/consumed = %d/%d, want 1/%d", page.Version, consumed, len(data))
		}
	})
}
