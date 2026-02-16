package multistream

import (
	"bytes"
	"testing"
)

func buildCode2Packet(tocBase byte, frame0, frame1 []byte) []byte {
	packet := make([]byte, 1+frameLengthBytes(len(frame0))+len(frame0)+len(frame1))
	offset := 0
	packet[offset] = tocBase | 0x02
	offset++
	offset += writeFrameLength(packet[offset:], len(frame0))
	copy(packet[offset:], frame0)
	offset += len(frame0)
	copy(packet[offset:], frame1)
	return packet
}

func buildCode3VBRPacket(tocBase byte, frames ...[]byte) []byte {
	if len(frames) < 3 {
		panic("buildCode3VBRPacket needs at least 3 frames")
	}

	headerBytes := 2
	for i := 0; i < len(frames)-1; i++ {
		headerBytes += frameLengthBytes(len(frames[i]))
	}
	frameBytes := 0
	for _, frame := range frames {
		frameBytes += len(frame)
	}

	packet := make([]byte, headerBytes+frameBytes)
	offset := 0
	packet[offset] = tocBase | 0x03
	offset++
	packet[offset] = byte(len(frames)) | 0x80
	offset++
	for i := 0; i < len(frames)-1; i++ {
		offset += writeFrameLength(packet[offset:], len(frames[i]))
	}
	for _, frame := range frames {
		copy(packet[offset:], frame)
		offset += len(frame)
	}

	return packet
}

func TestSelfDelimitedPacketRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
	}{
		{
			name:   "code0",
			packet: append([]byte{0xF8}, []byte{1, 2, 3, 4}...),
		},
		{
			name:   "code1",
			packet: append([]byte{0xF9}, []byte{1, 2, 3, 4, 5, 6}...),
		},
		{
			name:   "code2",
			packet: buildCode2Packet(0xF8, []byte{1, 2, 3, 4, 5}, []byte{6, 7, 8}),
		},
		{
			name: "code3-vbr",
			packet: buildCode3VBRPacket(0xF8,
				[]byte{1, 2, 3},
				[]byte{4, 5, 6, 7},
				[]byte{8, 9},
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseOpusPacket(tc.packet, false)
			if err != nil {
				t.Fatalf("parseOpusPacket(false) error: %v", err)
			}
			if parsed.consumed != len(tc.packet) {
				t.Fatalf("parseOpusPacket(false) consumed=%d want=%d", parsed.consumed, len(tc.packet))
			}

			selfDelimited, err := makeSelfDelimitedPacket(tc.packet)
			if err != nil {
				t.Fatalf("makeSelfDelimitedPacket error: %v", err)
			}

			recovered, consumed, err := decodeSelfDelimitedPacket(selfDelimited)
			if err != nil {
				t.Fatalf("decodeSelfDelimitedPacket error: %v", err)
			}
			if consumed != len(selfDelimited) {
				t.Fatalf("decodeSelfDelimitedPacket consumed=%d want=%d", consumed, len(selfDelimited))
			}
			if !bytes.Equal(recovered, tc.packet) {
				t.Fatalf("packet mismatch after self-delimited round-trip: got=%v want=%v", recovered, tc.packet)
			}
		})
	}
}

func TestParseMultistreamPacketWithSelfDelimitedCode3(t *testing.T) {
	stream0 := buildCode3VBRPacket(0xF8,
		[]byte{1, 2, 3},
		[]byte{4, 5},
		[]byte{6, 7, 8, 9},
	)
	stream1 := append([]byte{0xF8}, []byte{10, 11, 12, 13}...)

	selfDelimited0, err := makeSelfDelimitedPacket(stream0)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream0): %v", err)
	}

	data := append(selfDelimited0, stream1...)
	packets, err := parseMultistreamPacket(data, 2)
	if err != nil {
		t.Fatalf("parseMultistreamPacket: %v", err)
	}
	if len(packets) != 2 {
		t.Fatalf("packet count=%d want=2", len(packets))
	}
	if !bytes.Equal(packets[0], stream0) {
		t.Fatalf("stream0 mismatch: got=%v want=%v", packets[0], stream0)
	}
	if !bytes.Equal(packets[1], stream1) {
		t.Fatalf("stream1 mismatch: got=%v want=%v", packets[1], stream1)
	}
}
