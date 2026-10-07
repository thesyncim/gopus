package ogg

import (
	"bytes"
	"errors"
	"testing"
)

func TestReaderPacketLimitPerStream(t *testing.T) {
	for _, tc := range []struct {
		name       string
		streams    uint8
		packetSize int
		tooLarge   bool
	}{
		{name: "single exact", streams: 1, packetSize: maxOpusPacketSizePerStream},
		{name: "single over", streams: 1, packetSize: maxOpusPacketSizePerStream + 1, tooLarge: true},
		{name: "multistream exact", streams: 2, packetSize: 2 * maxOpusPacketSizePerStream},
		{name: "multistream over", streams: 2, packetSize: 2*maxOpusPacketSizePerStream + 1, tooLarge: true},
	} {
		for _, useInto := range []bool{false, true} {
			api := "ReadPacket"
			if useInto {
				api = "ReadPacketInto"
			}
			t.Run(tc.name+"/"+api, func(t *testing.T) {
				packetLimit := maxOpusPacketSizePerStream * int(tc.streams)
				bigPacket := bytes.Repeat([]byte{0}, tc.packetSize)
				packets := [][]byte{bigPacket}
				if tc.tooLarge {
					packets = append(packets, []byte{0x00, 0x5a})
				}
				stream := packetLimitTestStream(t, tc.streams, packets...)
				r, err := NewReader(bytes.NewReader(stream))
				if err != nil {
					t.Fatalf("NewReader: %v", err)
				}

				dst := make([]byte, packetLimit+1)
				got, n, granule, err := readLimitTestPacket(r, dst, useInto)
				tooLargeForAPI := tc.tooLarge && !useInto
				if tooLargeForAPI {
					if !errors.Is(err, ErrPacketTooLarge) || len(got) != 0 || n != 0 || granule != 0 {
						t.Fatalf("oversized read = (%d bytes, %d, %d, %v), want empty and ErrPacketTooLarge", len(got), n, granule, err)
					}
					if r.GranulePos() != 480 {
						t.Fatalf("GranulePos after discarded oversized packet = %d, want 480", r.GranulePos())
					}
				} else if err != nil || granule != 480 || n != len(bigPacket) || !bytes.Equal(got, bigPacket) {
					t.Fatalf("packet read = (%d bytes, %d, %d, %v), want (%d bytes, %d, 480, nil)", len(got), n, granule, err, len(bigPacket), len(bigPacket))
				}
				if tc.tooLarge {
					got, n, granule, err = readLimitTestPacket(r, dst, useInto)
					if err != nil || granule != 960 || !bytes.Equal(got, packets[1]) || n != len(packets[1]) {
						t.Fatalf("read after boundary+1 packet = (%x, %d, %d, %v), want (%x, %d, 960, nil)", got, n, granule, err, packets[1], len(packets[1]))
					}
				}
			})
		}
	}
}

func TestSeekGranulePacketLimit(t *testing.T) {
	packetLimit := maxOpusPacketSizePerStream
	tooLargeStream := packetLimitTestStream(t, 1,
		bytes.Repeat([]byte{0}, packetLimit+1),
		[]byte{0x00, 0x5a},
	)
	r, err := NewReader(bytes.NewReader(tooLargeStream))
	if err != nil {
		t.Fatalf("NewReader(oversized stream): %v", err)
	}
	if err := r.SeekGranule(480); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("SeekGranule on oversized packet = %v, want ErrPacketTooLarge", err)
	}
	packet, granule, err := r.ReadPacket()
	if err != nil || granule != 960 || !bytes.Equal(packet, []byte{0x00, 0x5a}) {
		t.Fatalf("read after oversized seek packet = (%x, %d, %v), want (005a, 960, nil)", packet, granule, err)
	}

	const streams = 2
	limit := streams * maxOpusPacketSizePerStream
	exactStream := packetLimitTestStream(t, streams, bytes.Repeat([]byte{0}, limit))
	r, err = NewReader(bytes.NewReader(exactStream))
	if err != nil {
		t.Fatalf("NewReader(exact multistream packet): %v", err)
	}
	if err := r.SeekGranule(480); err != nil {
		t.Fatalf("SeekGranule at exact multistream limit: %v", err)
	}
	packet, granule, err = r.ReadPacket()
	if err != nil || granule != 480 || len(packet) != limit {
		t.Fatalf("read after exact multistream seek = (%d bytes, %d, %v), want (%d bytes, 480, nil)", len(packet), granule, err, limit)
	}

	tooLargeStream = packetLimitTestStream(t, streams,
		bytes.Repeat([]byte{0}, limit+1),
		[]byte{0x00, 0x5a},
	)
	r, err = NewReader(bytes.NewReader(tooLargeStream))
	if err != nil {
		t.Fatalf("NewReader(oversized multistream packet): %v", err)
	}
	if err := r.SeekGranule(480); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("SeekGranule on oversized multistream packet = %v, want ErrPacketTooLarge", err)
	}
	packet, granule, err = r.ReadPacket()
	if err != nil || granule != 960 || !bytes.Equal(packet, []byte{0x00, 0x5a}) {
		t.Fatalf("read after oversized multistream seek packet = (%x, %d, %v), want (005a, 960, nil)", packet, granule, err)
	}
}

func readLimitTestPacket(r *Reader, dst []byte, useInto bool) ([]byte, int, uint64, error) {
	if useInto {
		n, granule, err := r.ReadPacketInto(dst)
		if err != nil {
			return nil, n, granule, err
		}
		return dst[:n], n, granule, nil
	}
	packet, granule, err := r.ReadPacket()
	return packet, len(packet), granule, err
}

func packetLimitTestStream(t *testing.T, streams uint8, packets ...[]byte) []byte {
	t.Helper()
	const serial = 0x71aa
	head := DefaultOpusHead(48000, 1)
	if streams > 1 {
		head = &OpusHead{
			Version:        opusHeadVersion,
			Channels:       2,
			SampleRate:     48000,
			MappingFamily:  MappingFamilyVorbis,
			StreamCount:    streams,
			CoupledCount:   0,
			ChannelMapping: []byte{0, 1},
		}
	}
	stream := readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head.Encode())
	stream = append(stream, readerBoundaryPacketPage(serial, 1, 0, 0, DefaultOpusTags().Encode())...)
	seq := uint32(2)
	for i, packet := range packets {
		stream = appendPacketLimitPages(t, stream, serial, &seq, packet, uint64((i+1)*480), i == len(packets)-1)
	}
	return stream
}

func appendPacketLimitPages(t *testing.T, stream []byte, serial uint32, seq *uint32, packet []byte, granule uint64, eos bool) []byte {
	const maxPageChunk = 63_750 // 250 complete lacing segments; page stays below Ogg's 65,307-byte maximum.
	for off := 0; off < len(packet); {
		chunkLen := min(maxPageChunk, len(packet)-off)
		more := off+chunkLen < len(packet)
		flags := byte(0)
		pageGranule := granule
		segments := BuildSegmentTable(chunkLen)
		if off > 0 {
			flags |= PageFlagContinuation
		}
		if more {
			segments = make([]byte, chunkLen/255)
			for i := range segments {
				segments[i] = 255
			}
			pageGranule = ^uint64(0)
		} else if eos {
			flags |= PageFlagEOS
		}
		page := readerBoundaryPage(serial, *seq, flags, pageGranule, segments, packet[off:off+chunkLen])
		if len(page) > 65_307 {
			t.Fatalf("test page is %d bytes, exceeds Ogg maximum 65,307", len(page))
		}
		stream = append(stream, page...)
		*seq = *seq + 1
		off += chunkLen
	}
	return stream
}
