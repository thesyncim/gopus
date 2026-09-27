package gopus

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamShortRefHelper libopustest.HelperCache
var multistreamExplicitShortRefHelper libopustest.HelperCache

type multistreamShortRef struct {
	streams, coupled int
	mapping          []byte
	packets          [][]byte
	ranges           []uint32
}

func encodeLibopusMultistreamShort(sampleRate, channels, family, bitrate, frameSize, frameCount, maxBytes int, vbr bool, pcm []int16) (*multistreamShortRef, error) {
	bin, err := multistreamShortRefHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "multistream short reference encode",
		OutputBase:  "gopus_libopus_refencode_multistream_short",
		SourceFile:  "libopus_refencode_multistream.c",
		CFlags:      []string{"-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "src"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		return nil, err
	}
	var useVBR uint32
	if vbr {
		useVBR = 1
	}
	bandwidthAuto := int32(-1000)
	payload := libopustest.NewOraclePayloadVersion("GMEI", 2,
		uint32(sampleRate), uint32(channels), uint32(family), 2049,
		uint32(bitrate), useVBR, 1, 10, uint32(bandwidthAuto),
		uint32(frameSize), uint32(frameCount), uint32(maxBytes), 1)
	for _, sample := range pcm {
		payload.I16(sample)
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "multistream short reference encode", "GMEO", 2)
	if err != nil {
		return nil, err
	}
	ref := &multistreamShortRef{streams: int(reader.U32()), coupled: int(reader.U32())}
	if got := int(reader.U32()); got != channels {
		return nil, fmt.Errorf("reference channels=%d want %d", got, channels)
	}
	ref.mapping = append([]byte(nil), reader.Bytes(channels)...)
	count := reader.Count(frameCount)
	ref.packets = make([][]byte, count)
	ref.ranges = make([]uint32, count)
	for frame := range count {
		ref.ranges[frame] = reader.U32()
		n := int(reader.U32())
		ref.packets[frame] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return ref, nil
}

func encodeLibopusExplicitMultistreamShort(sampleRate, channels, streams, coupled, bitrate, frameSize, frameCount, maxBytes int, vbr bool, mapping []byte, pcm []int16) (*multistreamShortRef, error) {
	bin, err := multistreamExplicitShortRefHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "explicit multistream short reference encode",
		OutputBase: "gopus_libopus_multistream_short_explicit",
		SourceFile: "libopus_multistream_short_explicit_oracle.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		return nil, err
	}
	var useVBR uint32
	if vbr {
		useVBR = 1
	}
	bandwidthAuto := int32(-1000)
	payload := libopustest.NewOraclePayloadVersion("GMSI", 1,
		uint32(sampleRate), uint32(channels), uint32(streams), uint32(coupled), 2049,
		uint32(bitrate), useVBR, 1, 10, uint32(bandwidthAuto),
		uint32(frameSize), uint32(frameCount), uint32(maxBytes))
	payload.Raw(mapping)
	for _, sample := range pcm {
		payload.I16(sample)
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "explicit multistream short reference encode", "GMSO", 1)
	if err != nil {
		return nil, err
	}
	count := reader.Count(frameCount)
	ref := &multistreamShortRef{streams: streams, coupled: coupled, mapping: append([]byte(nil), mapping...)}
	ref.packets = make([][]byte, count)
	ref.ranges = make([]uint32, count)
	for frame := range count {
		ref.ranges[frame] = reader.U32()
		n := int(reader.U32())
		ref.packets[frame] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return ref, nil
}

func publicMultistreamShortSweep(channels, frameSize, frameCount int) []int16 {
	pcm := make([]int16, channels*frameSize*frameCount)
	for sample := range frameSize * frameCount {
		tm := float64(sample) / 48000
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tm)
		for ch := range channels {
			v := amp * math.Sin(2*math.Pi*110*float64(ch+1)*tm)
			pcm[sample*channels+ch] = int16(math.Round(v * 32768))
		}
	}
	return pcm
}

func newPublicShortMultistream(t *testing.T, channels, frameSize, bitrate int, vbr bool) *MultistreamEncoder {
	t.Helper()
	var enc *MultistreamEncoder
	var err error
	if channels <= 8 {
		enc, err = NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
	} else {
		mapping := make([]byte, channels)
		for i := range mapping {
			mapping[i] = byte(i)
		}
		enc, err = NewMultistreamEncoder(48000, channels, channels/2, channels/2, mapping, ApplicationAudio)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(vbr)
	enc.SetVBRConstraint(true)
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestPublicMultistreamInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		channels, family, frameSize, frames, bitrate, maxBytes int
		vbr                                                    bool
	}{
		{6, 1, 960, 24, 192000, 4000, true},
	} {
		t.Run(fmt.Sprintf("ch%d_family%d", tc.channels, tc.family), func(t *testing.T) {
			pcm := publicMultistreamShortSweep(tc.channels, tc.frameSize, tc.frames)
			ref, err := encodeLibopusMultistreamShort(48000, tc.channels, tc.family, tc.bitrate, tc.frameSize, tc.frames, tc.maxBytes, tc.vbr, pcm)
			if err != nil {
				t.Fatalf("live C encode: %v", err)
			}
			enc := newPublicShortMultistream(t, tc.channels, tc.frameSize, tc.bitrate, tc.vbr)
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupled {
				t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupled)
			}
			out := make([]byte, tc.maxBytes)
			for frame := range tc.frames {
				input := pcm[frame*tc.frameSize*tc.channels : (frame+1)*tc.frameSize*tc.channels]
				n, err := enc.EncodeInt16(input, out)
				if err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
						firstPacketDifference(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
				}
			}
			last := pcm[(tc.frames-1)*tc.frameSize*tc.channels:]
			if _, err := enc.EncodeInt16(last, out); err != nil {
				t.Fatalf("warm short encode: %v", err)
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.EncodeInt16(last, out); err != nil {
					t.Fatalf("short encode: %v", err)
				}
			}); allocs != 0 {
				t.Fatalf("warm public short caller-buffer allocations=%g want 0", allocs)
			}
		})
	}
}

func TestPublicExplicitMultistreamInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels, streams, coupled, frameSize, frames, bitrate, maxBytes = 16, 8, 8, 960, 24, 384000, 4000
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	pcm := publicMultistreamShortSweep(channels, frameSize, frames)
	ref, err := encodeLibopusExplicitMultistreamShort(48000, channels, streams, coupled, bitrate, frameSize, frames, maxBytes, true, mapping, pcm)
	if err != nil {
		t.Fatalf("live C explicit multistream encode: %v", err)
	}
	enc := newPublicShortMultistream(t, channels, frameSize, bitrate, true)
	out := make([]byte, maxBytes)
	for frame := range frames {
		input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
		n, err := enc.EncodeInt16(input, out)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
				firstPacketDifference(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
		}
	}
	last := pcm[(frames-1)*frameSize*channels:]
	if _, err := enc.EncodeInt16(last, out); err != nil {
		t.Fatalf("warm short encode: %v", err)
	}
	if allocs := testing.AllocsPerRun(20, func() {
		if _, err := enc.EncodeInt16(last, out); err != nil {
			t.Fatalf("short encode: %v", err)
		}
	}); allocs != 0 {
		t.Fatalf("warm public explicit short caller-buffer allocations=%g want 0", allocs)
	}
}

func TestPublicMultistreamInt16BufferAndControlState(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels, frameSize, frames, bitrate, maxBytes = 6, 960, 2, 192000, 4000
	pcm := publicMultistreamShortSweep(channels, frameSize, frames)
	ref, err := encodeLibopusMultistreamShort(48000, channels, 1, bitrate, frameSize, frames, maxBytes, true, pcm)
	if err != nil {
		t.Fatalf("live C encode: %v", err)
	}
	enc := newPublicShortMultistream(t, channels, frameSize, bitrate, true)
	first := pcm[:frameSize*channels]
	firstCopy := append([]int16(nil), first...)
	if n, err := enc.EncodeInt16(first, make([]byte, 1)); err == nil || n != 0 {
		t.Fatalf("short output: n=%d err=%v", n, err)
	}
	out1 := make([]byte, maxBytes)
	n1, err := enc.EncodeInt16(first, out1)
	if err != nil {
		t.Fatalf("encode after short output: %v", err)
	}
	if !bytes.Equal(out1[:n1], ref.packets[0]) || enc.GetFinalRange() != ref.ranges[0] {
		t.Fatalf("state after short output: firstByte=%d GoRange=%08x CRange=%08x",
			firstPacketDifference(out1[:n1], ref.packets[0]), enc.GetFinalRange(), ref.ranges[0])
	}
	if !slices.Equal(first, firstCopy) {
		t.Fatal("encoder modified caller input")
	}
	owned := append([]byte(nil), out1[:n1]...)
	out2 := make([]byte, maxBytes)
	n2, err := enc.EncodeInt16(pcm[frameSize*channels:], out2)
	if err != nil {
		t.Fatalf("second encode: %v", err)
	}
	if !bytes.Equal(out2[:n2], ref.packets[1]) || enc.GetFinalRange() != ref.ranges[1] {
		t.Fatalf("second packet: firstByte=%d GoRange=%08x CRange=%08x",
			firstPacketDifference(out2[:n2], ref.packets[1]), enc.GetFinalRange(), ref.ranges[1])
	}
	if !bytes.Equal(out1[:n1], owned) {
		t.Fatal("second encode changed the first caller output buffer")
	}

	for _, depth := range []int{8, 12, 24} {
		t.Run(fmt.Sprintf("lsb%d", depth), func(t *testing.T) {
			enc := newPublicShortMultistream(t, 2, frameSize, 128000, true)
			if err := enc.SetLSBDepth(depth); err != nil {
				t.Fatal(err)
			}
			short := publicMultistreamShortSweep(2, frameSize, 1)
			out := make([]byte, maxBytes)
			if _, err := enc.EncodeInt16(short, out); err != nil {
				t.Fatal(err)
			}
			if got := enc.LSBDepth(); got != depth {
				t.Fatalf("after short input LSB depth=%d want %d", got, depth)
			}
			floatPCM := make([]float32, len(short))
			for i, sample := range short {
				floatPCM[i] = float32(sample) / 32768
			}
			if _, err := enc.Encode(floatPCM, out); err != nil {
				t.Fatal(err)
			}
			if got := enc.LSBDepth(); got != depth {
				t.Fatalf("after float input LSB depth=%d want %d", got, depth)
			}
			if _, err := enc.EncodeInt16(short, out); err != nil {
				t.Fatal(err)
			}
			if got := enc.LSBDepth(); got != depth {
				t.Fatalf("after alternating input LSB depth=%d want %d", got, depth)
			}
		})
	}
}

func TestPublicMultistreamInt16LowSpaceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels, frameSize, frames, bitrate, maxBytes = 8, 480, 5, 64000, 15
	pcm := publicMultistreamShortSweep(channels, frameSize, frames)
	ref, err := encodeLibopusMultistreamShort(48000, channels, 1, bitrate, frameSize, frames, maxBytes, false, pcm)
	if err != nil {
		t.Fatalf("live C encode: %v", err)
	}
	enc := newPublicShortMultistream(t, channels, frameSize, bitrate, false)
	out := make([]byte, maxBytes)
	for frame := range frames {
		input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
		n, err := enc.EncodeInt16(input, out)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Fatalf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
				firstPacketDifference(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
		}
	}
	last := pcm[(frames-1)*frameSize*channels:]
	if _, err := enc.EncodeInt16(last, out); err != nil {
		t.Fatalf("warm low-space encode: %v", err)
	}
	if allocs := testing.AllocsPerRun(20, func() {
		if _, err := enc.EncodeInt16(last, out); err != nil {
			t.Fatalf("low-space encode: %v", err)
		}
	}); allocs != 0 {
		t.Fatalf("warm low-space caller-buffer allocations=%g want 0", allocs)
	}
}

func firstPacketDifference(got, want []byte) int {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return min(len(got), len(want))
	}
	return -1
}
