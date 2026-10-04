package multistream

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestPacketDurationRejectsInvalidStreamCount(t *testing.T) {
	packet := []byte{0xf8, 0xff, 0xfe}
	for _, streams := range []int{-1, 0, 256, int(^uint(0) >> 1)} {
		t.Run(fmt.Sprintf("streams_%d", streams), func(t *testing.T) {
			for _, rate := range []int{0, 48000} {
				var duration int
				var err error
				if rate == 0 {
					duration, err = PacketDuration(packet, streams)
				} else {
					duration, err = PacketDurationAtRate(packet, streams, rate)
				}
				if duration != 0 || !errors.Is(err, ErrInvalidStreamCount) {
					t.Errorf("rate=%d duration=(%d, %v), want (0, ErrInvalidStreamCount)", rate, duration, err)
				}
			}
		})
	}
}

func TestPacketDurationAcceptsMaximumStreamCount(t *testing.T) {
	// RFC 6716 Appendix B puts an explicit zero-byte frame length after
	// the TOC of each self-delimited packet. The last stream is undelimited.
	packet := append(bytes.Repeat([]byte{0xf8, 0}, 254), 0xf8)
	if duration, err := PacketDuration(packet, 255); err != nil || duration != 960 {
		t.Fatalf("PacketDuration = (%d, %v), want (960, nil)", duration, err)
	}
	if duration, err := PacketDurationAtRate(packet, 255, 8000); err != nil || duration != 160 {
		t.Fatalf("PacketDurationAtRate = (%d, %v), want (160, nil)", duration, err)
	}
}

func TestPacketDurationInvalidStreamCountZeroAlloc(t *testing.T) {
	check := func() {
		if _, err := PacketDuration(nil, 256); !errors.Is(err, ErrInvalidStreamCount) {
			t.Fatal(err)
		}
		if _, err := PacketDurationAtRate(nil, 256, 48000); !errors.Is(err, ErrInvalidStreamCount) {
			t.Fatal(err)
		}
	}
	check()
	if allocs := testing.AllocsPerRun(100, check); allocs != 0 {
		t.Fatalf("invalid stream count allocates %g times, want 0", allocs)
	}
}
