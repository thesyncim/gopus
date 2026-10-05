package multistream

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// These packets decode to 960, 480, and 120 samples at 48 kHz. The last
// packet switches to stereo CELT after a mono Hybrid packet.
func statefulProjectionRecoveryPackets(t *testing.T) [][]byte {
	t.Helper()
	hexPackets := [...]string{
		"f89ecc3c1abc39107aac7c6f0f14812ad56396763f3811fbbcdeb4484cc2341f57f4397aebf1ae9cd2861f0b00c65fd6d1bc1ade33026844ea9e43cb87cd45b73eedcafdd10e92bae665c74952f8d9e7b0d52290a322b6e7f9f08c2015ec7760e37b614827315ae6237d4ac567edabc589879eb69ae19d328617ed4e3a42650257780886508000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000021762d14e1499436820b31dda2decbc315253e77eecbd3bacb74598abdab638c171dc7d35e8a43faa87b0fd117028815c48573e2ee0ec143f26fc4896a9696ed5817e22fc65ce0ff2b1d5295f810d341739eb5a139c9",
		"70a8f77ca6242c85d1dfe89250f7bcd05a152957cab69d04b1d02d2dfdcd23b79593e89ff61d1dcedaa4fb8ad2155e16052b21c563cc828769374b9334e79f124655123c13ca005d9c048c566b1cc473ccef7467dda10aa8d9612fd230288c14f3f1106a98d19a1b22c89b867b7027648f65e0ef6ba4cdcd2320bbd578e89ccbe410ebf9b12176d174ac92",
		"e470378d859b002ea26903dbf5ce78a48c66f7173b6e6bf546f1a3149c1d049e7c0768ab7279bc38cee724a0f0ca5b423816a563d6",
	}
	packets := make([][]byte, len(hexPackets))
	for i, encoded := range hexPackets {
		packet, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode packet %d: %v", i, err)
		}
		packets[i] = packet
	}
	if len(packets[0]) != 388 || len(packets[1]) != 139 || len(packets[2]) != 53 {
		t.Fatalf("unexpected packet lengths: %d/%d/%d", len(packets[0]), len(packets[1]), len(packets[2]))
	}
	return packets
}

func TestMultistreamStatefulShortFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, frameSize = 48000, 5760
	packets := statefulProjectionRecoveryPackets(t)
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, FrameSize: frameSize}
	}
	for _, layout := range []struct {
		name     string
		channels int
		coupled  int
		mapping  []byte
	}{
		{name: "mono", channels: 1, coupled: 0, mapping: []byte{0}},
		{name: "coupled_stereo", channels: 2, coupled: 1, mapping: []byte{0, 1}},
	} {
		t.Run(layout.name, func(t *testing.T) {
			want, err := decodeWithLibopusReferencePackets(1, sampleRate, layout.channels, 1, layout.coupled, frameSize, layout.mapping, nil, packets)
			if err != nil {
				t.Fatalf("libopus multistream decode: %v", err)
			}
			child, err := libopustest.ProbeDecodeSequence(sampleRate, layout.channels, cases)
			if err != nil {
				t.Fatalf("libopus child decode: %v", err)
			}
			decoder, err := NewDecoder(sampleRate, layout.channels, 1, layout.coupled, layout.mapping)
			if err != nil {
				t.Fatal(err)
			}
			position := 0
			for i, packet := range packets {
				got, err := decoder.DecodeToFloat32(packet, frameSize)
				if err != nil {
					t.Fatalf("step %d decode: %v", i, err)
				}
				wantSamples := int(child[i].Code)
				if len(got) != wantSamples*layout.channels {
					t.Fatalf("step %d returned %d samples per channel; libopus returns %d", i, len(got)/layout.channels, wantSamples)
				}
				if position+len(got) > len(want) {
					t.Fatalf("step %d exceeds libopus output", i)
				}
				for j, sample := range got {
					if gotBits, wantBits := math.Float32bits(sample), math.Float32bits(want[position+j]); gotBits != wantBits {
						t.Fatalf("step %d sample %d: Go=%08x libopus=%08x", i, j, gotBits, wantBits)
					}
				}
				if gotRange := decoder.FinalRange(); gotRange != child[i].FinalRange {
					t.Fatalf("step %d final range: Go=%08x libopus=%08x", i, gotRange, child[i].FinalRange)
				}
				position += len(got)
			}
			if position != len(want) {
				t.Fatalf("decoded %d samples, libopus returned %d", position, len(want))
			}
		})
	}
}

func TestProjectionStatefulShortFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, frameSize, channels = 48000, 5760, 4
	ref := projectionDecodeRef(t, channels, 960, 2, 160000)
	if ref.streams != 2 || ref.coupledStreams != 2 {
		t.Fatalf("unexpected FOA layout: streams=%d coupled=%d", ref.streams, ref.coupledStreams)
	}
	basePackets := statefulProjectionRecoveryPackets(t)
	packets := make([][]byte, len(basePackets))
	for i, packet := range basePackets {
		selfDelimited, err := makeSelfDelimitedPacket(packet)
		if err != nil {
			t.Fatalf("step %d self-delimited packet: %v", i, err)
		}
		packets[i] = append(selfDelimited, packet...)
	}
	want, err := decodeWithLibopusReferencePackets(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, trivialMapping(channels), ref.demixing, packets)
	if err != nil {
		t.Fatalf("libopus projection decode: %v", err)
	}
	child := make([][]libopustest.DecodeDiffResult, ref.streams)
	for stream := 0; stream < ref.streams; stream++ {
		cases := make([]libopustest.DecodeDiffCase, len(packets))
		for i, packet := range packets {
			parts, err := parseMultistreamPacket(packet, ref.streams)
			if err != nil {
				t.Fatalf("step %d parse multistream packet: %v", i, err)
			}
			cases[i] = libopustest.DecodeDiffCase{Packet: parts[stream], FrameSize: frameSize}
		}
		child[stream], err = libopustest.ProbeDecodeSequence(sampleRate, 2, cases)
		if err != nil {
			t.Fatalf("libopus child %d decode: %v", stream, err)
		}
	}
	decoder, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	position := 0
	for i, packet := range packets {
		got, err := decoder.DecodeToFloat32(packet, frameSize)
		if err != nil {
			t.Fatalf("step %d decode: %v", i, err)
		}
		wantSamples := int(child[0][i].Code)
		if len(got) != wantSamples*channels {
			t.Fatalf("step %d returned %d samples per channel; libopus returns %d", i, len(got)/channels, wantSamples)
		}
		if position+len(got) > len(want) {
			t.Fatalf("step %d exceeds libopus output", i)
		}
		for j, sample := range got {
			if gotBits, wantBits := math.Float32bits(sample), math.Float32bits(want[position+j]); gotBits != wantBits {
				t.Fatalf("step %d sample %d: Go=%08x libopus=%08x", i, j, gotBits, wantBits)
			}
		}
		var wantRange uint32
		for stream := range child {
			if child[stream][i].Code != child[0][i].Code {
				t.Fatalf("step %d child %d sample count %d differs from child 0 count %d", i, stream, child[stream][i].Code, child[0][i].Code)
			}
			wantRange ^= child[stream][i].FinalRange
		}
		if gotRange := decoder.FinalRange(); gotRange != wantRange {
			t.Fatalf("step %d final range: Go=%08x libopus children=%08x", i, gotRange, wantRange)
		}
		position += len(got)
	}
	if position != len(want) {
		t.Fatalf("decoded %d samples, libopus returned %d", position, len(want))
	}
}
