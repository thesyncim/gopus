package ogg

import (
	"bytes"
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestNewReaderRequiresOpusTagsContinuation(t *testing.T) {
	const serial = 0x9879
	head := DefaultOpusHead(48000, 1).Encode()
	tags := (&OpusTags{Vendor: string(bytes.Repeat([]byte{'v'}, 700))}).Encode()
	for _, tc := range []struct {
		name        string
		middleFlags byte
		finalFlags  byte
	}{
		{name: "missing middle flag", finalFlags: PageFlagContinuation},
		{name: "missing final flag", middleFlags: PageFlagContinuation},
		{name: "valid", middleFlags: PageFlagContinuation, finalFlags: PageFlagContinuation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head)
			stream = append(stream, readerBoundaryPage(serial, 1, 0, ^uint64(0), []byte{255}, tags[:255])...)
			stream = append(stream, readerBoundaryPage(serial, 2, tc.middleFlags, ^uint64(0), []byte{255}, tags[255:510])...)
			stream = append(stream, readerBoundaryPage(serial, 3, tc.finalFlags, 0, BuildSegmentTable(len(tags)-510), tags[510:])...)
			r, err := NewReader(bytes.NewReader(stream))
			if tc.name == "valid" {
				if err != nil || r.Tags.Vendor != string(bytes.Repeat([]byte{'v'}, 700)) {
					t.Fatalf("NewReader = (%v, %v), want intact tags", r, err)
				}
			} else if !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("NewReader error = %v, want ErrInvalidPage", err)
			}
		})
	}
}

func TestPacketDuration48kRejectsOverlongPackets(t *testing.T) {
	for _, packet := range [][]byte{
		{tocByte(0, 3), 13},  // 13 * 10 ms
		{tocByte(1, 3), 7},   // 7 * 20 ms
		{tocByte(2, 3), 4},   // 4 * 40 ms
		{tocByte(3, 3), 3},   // 3 * 60 ms
		{tocByte(16, 3), 49}, // 49 * 2.5 ms
		{tocByte(17, 3), 25}, // 25 * 5 ms
	} {
		if duration, ok := packetDuration48k(packet); ok {
			t.Errorf("packetDuration48k(%x) = (%d, true), want unknown duration", packet, duration)
		}
	}
}

func TestReaderOverlongPacketGranuleFallback(t *testing.T) {
	first := []byte{0xf8, 0x11}
	overlong := []byte{tocByte(1, 3), 7}
	for _, flags := range []byte{0, PageFlagEOS} {
		stream := eosGranuleStream(readerBoundaryPacketPage(0x4e71, 2, flags, 1920, first, overlong))
		r, err := NewReader(bytes.NewReader(stream))
		if err != nil {
			t.Fatal(err)
		}
		got, gp, err := r.ReadPacket()
		if err != nil || !bytes.Equal(got, first) || gp != 1920 {
			t.Errorf("flags %d: first packet = (%x, %d, %v), want (%x, 1920, nil)", flags, got, gp, err, first)
		}
		if err := r.SeekGranule(1000); err != nil {
			t.Fatal(err)
		}
		got, gp, err = r.ReadPacket()
		if err != nil || !bytes.Equal(got, first) || gp != 1920 {
			t.Errorf("flags %d: sought packet = (%x, %d, %v), want (%x, 1920, nil)", flags, got, gp, err, first)
		}
	}
}

func TestPacketDuration48kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:      "Ogg packet duration",
		OutputBase: "gopus_libopus_packet_duration",
		SourceFile: "libopus_packet_duration_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "Ogg packet duration", err)
	}
	packets := [][]byte{nil}
	for config := byte(0); config < 32; config++ {
		for code := byte(0); code < 3; code++ {
			packets = append(packets, []byte{config<<3 | code})
		}
		packets = append(packets, []byte{config<<3 | 3})
		for count := byte(0); count < 64; count++ {
			packets = append(packets, []byte{config<<3 | 3, count})
		}
	}
	payload := libopustest.NewOraclePayload("GPDI", uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(48000)
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "Ogg packet duration", "GPDO")
	if err != nil {
		t.Fatal(err)
	}
	count := reader.Count(len(packets))
	reader.ExpectRemaining(12 * count)
	for _, packet := range packets {
		reader.I32()         // samples per frame
		reader.I32()         // frame count
		want := reader.I32() // opus_packet_get_nb_samples at 48 kHz
		got, ok := packetDuration48k(packet)
		if ok != (want > 0) || (ok && got != uint64(want)) {
			t.Errorf("packetDuration48k(%x) = (%d, %v), libopus samples = %d", packet, got, ok, want)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
