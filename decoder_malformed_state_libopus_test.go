package gopus

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeMalformedVBRPreservesSelectedLibopusState(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := append([]byte(nil), encodeAPIRateSILKPacket(t, channels)...)
		body := packet[1:]
		makeBad := func(padding, oversizedLast bool) []byte {
			header := []byte{packet[0]&^3 | 3, 0x83}
			if padding {
				header[1] |= 0x40
				header = append(header, 3)
			}
			var length [2]byte
			n := encodeFrameLength(length[:], len(body))
			header = append(header, length[:n]...)
			if oversizedLast {
				header = append(header, 0)
			} else {
				header = append(header, 2)
			}
			header = append(header, body...)
			if oversizedLast {
				header = append(header, make([]byte, maxOpusFrameBytes+1)...)
			}
			if padding {
				header = append(header, 0, 0, 0)
			}
			return header
		}
		for _, format := range []uint32{libopustest.DecodeDiffFormatFloat32, libopustest.DecodeDiffFormatInt16, libopustest.DecodeDiffFormatInt24} {
			for _, padded := range []bool{false, true} {
				for _, oversized := range []bool{false, true} {
					t.Run(fmt.Sprintf("ch%d/format%d/padded%t/oversized%t", channels, format, padded, oversized), func(t *testing.T) {
						bad := makeBad(padded, oversized)
						steps := []libopustest.DecodeDiffCase{
							{Packet: packet, Format: format, FrameSize: 960},
							{Packet: bad, Format: format, FrameSize: 2880},
							{Packet: packet, Format: format, FrameSize: 960},
							{Format: format, FrameSize: 960},
							{Packet: packet, Format: format, FrameSize: 960},
						}
						want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
						if err != nil {
							t.Fatal(err)
						}
						if want[1].Code != -4 || want[1].FinalRange != want[0].FinalRange {
							t.Fatalf("C malformed packet status=%d range=%08x, preceding=%08x", want[1].Code, want[1].FinalRange, want[0].FinalRange)
						}
						cfg := DefaultDecoderConfig(48000, channels)
						cfg.MaxPacketBytes = 2 * maxOpusFrameBytes
						dec, err := NewDecoder(cfg)
						if err != nil {
							t.Fatal(err)
						}
						f32 := make([]float32, 2880*channels)
						i16 := make([]int16, len(f32))
						i24 := make([]int32, len(f32))
						decode := func(step libopustest.DecodeDiffCase) (int, error) {
							n := int(step.FrameSize) * channels
							switch format {
							case libopustest.DecodeDiffFormatInt16:
								return dec.DecodeInt16(step.Packet, i16[:n])
							case libopustest.DecodeDiffFormatInt24:
								return dec.DecodeInt24(step.Packet, i24[:n])
							default:
								return dec.Decode(step.Packet, f32[:n])
							}
						}
						for i, step := range steps {
							n, err := decode(step)
							if want[i].Code < 0 {
								if n != 0 || err != ErrInvalidPacket {
									t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
								}
							} else if err != nil || int32(n) != want[i].Code {
								t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
							}
							if dec.FinalRange() != want[i].FinalRange {
								t.Fatalf("step%d range=%08x C=%08x", i, dec.FinalRange(), want[i].FinalRange)
							}
							for j := range n * channels {
								var got, expected uint32
								switch format {
								case libopustest.DecodeDiffFormatInt16:
									got, expected = uint32(uint16(i16[j])), uint32(binary.LittleEndian.Uint16(want[i].PCM[j*2:]))
								case libopustest.DecodeDiffFormatInt24:
									got, expected = uint32(i24[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:])
								default:
									got, expected = math.Float32bits(f32[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:])
								}
								if got != expected {
									t.Fatalf("step%d sample%d raw=%08x C=%08x", i, j, got, expected)
								}
							}
						}
						if allocs := testing.AllocsPerRun(20, func() {
							if n, err := decode(steps[1]); n != 0 || err != ErrInvalidPacket {
								t.Fatalf("malformed decode=(%d,%v)", n, err)
							}
						}); allocs != 0 {
							t.Fatalf("warm malformed decode allocations=%g, want0", allocs)
						}
					})
				}
			}
		}
	}
}

func TestDecodeMalformedRawCSequenceWitnessPreservesState(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels = 1
	packet := encodeAPIRateSILKPacket(t, channels)
	bad := []byte{0x4b, 0x83, 0x02, 0x01, 0x00, 0x00}
	steps := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: bad, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 2880},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if want[1].Code != -4 || want[1].FinalRange != want[0].FinalRange {
		t.Fatalf("C witness status=%d range=%08x, preceding=%08x", want[1].Code, want[1].FinalRange, want[0].FinalRange)
	}

	cfg := DefaultDecoderConfig(48000, channels)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 2880*channels)
	for i, step := range steps {
		n, err := dec.Decode(step.Packet, pcm[:int(step.FrameSize)*channels])
		if want[i].Code < 0 {
			if n != 0 || err != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
			}
		} else if err != nil || int32(n) != want[i].Code {
			t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
		}
		if dec.FinalRange() != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, dec.FinalRange(), want[i].FinalRange)
		}
		for j := range n * channels {
			if got, expected := math.Float32bits(pcm[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:]); got != expected {
				t.Fatalf("step%d sample%d raw=%08x C=%08x", i, j, got, expected)
			}
		}
	}
}
