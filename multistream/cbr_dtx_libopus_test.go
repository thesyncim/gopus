package multistream

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamCBRDTXMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames, channels = 960, 120, 2
	for _, history := range []string{"silence", "mixed", "active_silence"} {
		t.Run(history, func(t *testing.T) {
			pcm := make([]float32, frameSize*frames*channels)
			for f := range frames {
				for i := range frameSize {
					x := float32(0.25 * math.Sin(2*math.Pi*440*float64(f*frameSize+i)/48000))
					if history == "mixed" || history == "active_silence" && f < 25 {
						pcm[(f*frameSize+i)*channels] = x
					}
					if history == "active_silence" && f < 25 {
						pcm[(f*frameSize+i)*channels+1] = x
					}
				}
			}
			ref, err := encodeLibopusSurround(48000, channels, 255, 2049, 32000, false, false,
				10, -1000, frameSize, frames, 4000, pcm, true)
			if err != nil {
				t.Fatal(err)
			}
			if ref.streams != 2 || ref.coupledStreams != 0 || len(ref.packets) != frames {
				t.Fatal("unexpected oracle stream layout or packet count")
			}
			e, err := NewEncoder(48000, channels, 2, 0, []byte{0, 1})
			if err != nil {
				t.Fatal(err)
			}
			e.SetBitrate(32000)
			e.SetVBR(false)
			e.SetDTX(true)
			e.SetComplexity(10)
			out := make([]byte, 4000)
			for f := range frames {
				n, err := e.Encode(pcm[f*frameSize*channels:(f+1)*frameSize*channels], frameSize, out)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out[:n], ref.packets[f]) || e.GetFinalRange() != ref.ranges[f] {
					t.Fatalf("frame %d: bytes %d/%d, range %08x/%08x, packets equal=%t", f, n, len(ref.packets[f]), e.GetFinalRange(), ref.ranges[f], bytes.Equal(out[:n], ref.packets[f]))
				}
				if n != 80 {
					t.Fatalf("frame %d: CBR bytes %d, want 80", f, n)
				}
			}
			last := pcm[(frames-1)*frameSize*channels:]
			if allocs := testing.AllocsPerRun(20, func() {
				if n, err := e.Encode(last, frameSize, out); err != nil || n != 80 {
					t.Fatalf("warm encode bytes=%d err=%v", n, err)
				}
			}); allocs != 0 {
				t.Fatalf("warm allocations %g, want 0", allocs)
			}
		})
	}
}

func TestPaddedStreamMatchesLibopusRepacketizer(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := libopustest.BuildCHelper(pairMultistreamReference(libopustest.CHelperConfig{
		Label: "multistream CBR padding", OutputBase: "gopus_ms_cbr_pad",
		SourceFile: "libopus_repacketizer_info.c", CFlags: []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"src", "celt", "silk"},
		Libs:        []string{"-lm"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	packets := [][]byte{{0x78}, {0x78, 1, 2}, {0x79, 1, 2}, {0x7a, 1, 1, 2, 3}, {0x7b, 3, 1, 2, 3}}
	for _, ext := range [][]packetExtensionData{
		{{ID: 3, Frame: 0, Data: []byte{0x52}}},
		{{ID: 124, Frame: 0, Data: []byte{1, 2, 3}}, {ID: 32, Frame: 1, Data: []byte{4, 5}}},
	} {
		buf := make([]byte, 100)
		n, err := buildOpusPacketFromFramesAndExtensions(0x78, [][]byte{{1, 2}, {3}}, ext, false, buf)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, buf[:n])
	}
	ordinary := make([]byte, 100)
	n, err := buildOpusPacketFromFramesAndPadding(0x78, [][]byte{{1, 2}}, make([]byte, 17), false, ordinary)
	if err != nil {
		t.Fatal(err)
	}
	packets = append(packets, ordinary[:n])
	noncanonical := make([]byte, 100)
	n, err = buildOpusPacketFromFramesAndPadding(0x78, [][]byte{{1, 2}}, []byte{1, 1, 7, 0x52, 1}, false, noncanonical)
	if err != nil {
		t.Fatal(err)
	}
	packets = append(packets, noncanonical[:n])
	for i, packet := range packets {
		for _, extra := range []int{1, 2, 254, 255, 256, 257, 510, 511} {
			t.Run(fmt.Sprintf("packet%d_extra%d", i, extra), func(t *testing.T) {
				target := len(packet) + extra
				payload := libopustest.NewOraclePayload("GRPI", 1, 1, uint32(len(packet)))
				payload.Raw(packet)
				for _, v := range []uint32{0, 0, uint32(target), uint32(target)} {
					payload.U32(v)
				}
				reader, err := libopustest.RunOracle(bin, payload.Bytes(), "multistream CBR padding", "GRPO")
				if err != nil {
					t.Fatal(err)
				}
				reader.Count(1)
				if status := reader.I32(); status != 0 {
					t.Fatalf("C packet rejection: %d", status)
				}
				reader.I32() // frame count
				reader.I32() // unpadded output status
				size := int(reader.I32())
				if size < 0 || size > target {
					t.Fatalf("invalid oracle output size %d", size)
				}
				reader.Bytes(size)
				if status := reader.I32(); status != 0 {
					t.Fatalf("C padding rejection: %d", status)
				}
				want := reader.Bytes(reader.Count(target))
				reader.I32() // unpad status
				size = int(reader.I32())
				if size < 0 || size > target {
					t.Fatalf("invalid oracle unpad size %d", size)
				}
				reader.Bytes(size)
				if err := reader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}
				var scratch packetScratch
				out := make([]byte, target)
				check := func() {
					n, err := padStreamPacketInto(&scratch, out, packet)
					if err != nil || n != target || !bytes.Equal(out[:n], want) {
						t.Fatalf("pad got %x, want %x, err=%v", out[:n], want, err)
					}
				}
				check()
				if allocs := testing.AllocsPerRun(20, check); allocs != 0 {
					t.Fatalf("warm padding allocations %g, want 0", allocs)
				}
			})
		}
	}
}
