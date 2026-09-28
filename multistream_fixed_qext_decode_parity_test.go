//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func encodeLibopusQEXTMultiFramePacket(t *testing.T, opusDemo string, channels, frameSizeMs, bitrate int, pcm []float32) []byte {
	t.Helper()
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext-multiframe.f32")
	packetPath := filepath.Join(tmpDir, "qext-multiframe.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatalf("write QEXT multiframe input: %v", err)
	}
	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), fmt.Sprint(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", fmt.Sprint(frameSizeMs),
		"-qext", "-cbr", inputPath, packetPath,
	}
	if output, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("opus_demo encode %d ms QEXT packet: %v (%s)", frameSizeMs, err, output)
	}
	packet, err := firstOpusDemoPacket(packetPath)
	if err != nil {
		t.Fatalf("read %d ms QEXT packet: %v", frameSizeMs, err)
	}
	return packet
}

func TestFixedQEXTMultistreamDecodeUsesSidePayload(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Fatalf("QEXT-enabled opus_demo: %v", err)
	}

	const sampleRate, channels, frameSize, packetCount = 48000, 2, 960, 4
	packet := encodeLibopusPacketAtBitrate(t, opusDemo, channels,
		qextSinePCM(channels, frameSize), true, true, 256000)
	_, _, padding, frameCount, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parse QEXT packet: %v", err)
	}
	if _, found, _ := findPacketExtension(padding, frameCount, qextPacketExtensionID); !found {
		t.Fatalf("reference packet has no CELT QEXT payload (TOC %#02x)", packet[0])
	}

	const streams, coupled = 1, 1
	mapping := []byte{0, 1}
	packets := make([][]byte, packetCount)
	for i := range packets {
		packets[i] = packet
	}
	want16, err := decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		t.Fatalf("selected fixed+QEXT C int16 decode: %v", err)
	}
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		t.Fatalf("selected fixed+QEXT C int24 decode: %v", err)
	}

	dec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder int16: %v", err)
	}
	got16 := make([]int32, 0, len(want16))
	frame16 := make([]int16, frameSize*channels)
	for i, packet := range packets {
		n, err := dec16.DecodeInt16(packet, frame16)
		if err != nil || n != frameSize {
			t.Fatalf("DecodeInt16 frame %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got16 = append(got16, int16ToInt32(frame16)...)
	}
	assertFixedExact(t, "fixed+QEXT multistream int16", got16, int16ToInt32(want16))
	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec16.DecodeInt16(packet, frame16); err != nil || n != frameSize {
			t.Fatalf("warm DecodeInt16 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm fixed+QEXT multistream DecodeInt16 allocations=%g want 0", allocs)
	}

	dec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder int24: %v", err)
	}
	got24 := make([]int32, 0, len(want24))
	frame24 := make([]int32, frameSize*channels)
	for i, packet := range packets {
		n, err := dec24.DecodeInt24(packet, frame24)
		if err != nil || n != frameSize {
			t.Fatalf("DecodeInt24 frame %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got24 = append(got24, frame24...)
	}
	assertFixedExact(t, "fixed+QEXT multistream int24", got24, want24)
	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec24.DecodeInt24(packet, frame24); err != nil || n != frameSize {
			t.Fatalf("warm DecodeInt24 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm fixed+QEXT multistream DecodeInt24 allocations=%g want 0", allocs)
	}

	for _, gainQ8 := range []int{512, -512, 2048, -2048, 8192, -8192, 32767, -32768} {
		t.Run(fmt.Sprintf("decode_gain_%d", gainQ8), func(t *testing.T) {
			wantGain16, err := decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				t.Fatalf("selected fixed+QEXT C int16 decode with gain: %v", err)
			}
			wantGain24, err := decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				t.Fatalf("selected fixed+QEXT C int24 decode with gain: %v", err)
			}

			gainDec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder gain int16: %v", err)
			}
			if err := gainDec16.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d) int16: %v", gainQ8, err)
			}
			gotGain16 := make([]int32, 0, len(wantGain16))
			gainOut16 := make([]int16, frameSize*channels)
			for i, packet := range packets {
				if n, err := gainDec16.DecodeInt16(packet, gainOut16); err != nil || n != frameSize {
					t.Fatalf("gain DecodeInt16 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
				}
				gotGain16 = append(gotGain16, int16ToInt32(gainOut16)...)
			}
			assertFixedExact(t, "fixed+QEXT multistream gained int16", gotGain16, int16ToInt32(wantGain16))
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := gainDec16.DecodeInt16(packet, gainOut16); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt16 returned samples=%d, err=%v; want %d", n, err, frameSize)
				}
			}); allocs != 0 {
				t.Fatalf("warm fixed+QEXT gained DecodeInt16 allocations=%g want 0", allocs)
			}

			gainDec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder gain int24: %v", err)
			}
			if err := gainDec24.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d) int24: %v", gainQ8, err)
			}
			gotGain24 := make([]int32, 0, len(wantGain24))
			gainOut24 := make([]int32, frameSize*channels)
			for i, packet := range packets {
				if n, err := gainDec24.DecodeInt24(packet, gainOut24); err != nil || n != frameSize {
					t.Fatalf("gain DecodeInt24 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
				}
				gotGain24 = append(gotGain24, gainOut24...)
			}
			assertFixedExact(t, "fixed+QEXT multistream gained int24", gotGain24, wantGain24)
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := gainDec24.DecodeInt24(packet, gainOut24); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt24 returned samples=%d, err=%v; want %d", n, err, frameSize)
				}
			}); allocs != 0 {
				t.Fatalf("warm fixed+QEXT gained DecodeInt24 allocations=%g want 0", allocs)
			}
		})
	}

	t.Run("multi_frame", func(t *testing.T) {
		const multiFrameSize, frameSizeMs = 1920, 40
		multiPCM := qextSinePCM(channels, multiFrameSize)
		multiPacket := encodeLibopusQEXTMultiFramePacket(t, opusDemo, channels, frameSizeMs, 256000, multiPCM)
		_, _, multiPadding, multiFrames, err := parsePacketFramesAndPadding(multiPacket)
		if err != nil {
			t.Fatalf("parse QEXT multiframe packet: %v", err)
		}
		if multiFrames < 2 {
			t.Fatalf("opus_demo %d ms packet has %d frame(s), want at least 2", frameSizeMs, multiFrames)
		}
		if _, found, _ := findPacketExtension(multiPadding, multiFrames, qextPacketExtensionID); !found {
			t.Fatalf("reference multiframe packet has no QEXT payload (TOC %#02x)", multiPacket[0])
		}
		multiPackets := [][]byte{multiPacket, multiPacket}
		wantMulti16, err := decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, multiFrameSize, mapping, multiPackets)
		if err != nil {
			t.Fatalf("selected fixed+QEXT multiframe C int16 decode: %v", err)
		}
		wantMulti24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, multiFrameSize, mapping, multiPackets)
		if err != nil {
			t.Fatalf("selected fixed+QEXT multiframe C int24 decode: %v", err)
		}

		multiDec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
		if err != nil {
			t.Fatalf("NewMultistreamDecoder multiframe int16: %v", err)
		}
		multiGot16 := make([]int32, 0, len(wantMulti16))
		multiOut16 := make([]int16, multiFrameSize*channels)
		for i := range multiPackets {
			n, err := multiDec16.DecodeInt16(multiPackets[i], multiOut16)
			if err != nil || n != multiFrameSize {
				t.Fatalf("multiframe DecodeInt16 packet %d returned samples=%d, err=%v; want %d", i, n, err, multiFrameSize)
			}
			multiGot16 = append(multiGot16, int16ToInt32(multiOut16)...)
		}
		assertFixedExact(t, "fixed+QEXT multiframe multistream int16", multiGot16, int16ToInt32(wantMulti16))
		if allocs := testing.AllocsPerRun(100, func() {
			if n, err := multiDec16.DecodeInt16(multiPacket, multiOut16); err != nil || n != multiFrameSize {
				t.Fatalf("warm multiframe DecodeInt16 returned samples=%d, err=%v; want %d", n, err, multiFrameSize)
			}
		}); allocs != 0 {
			t.Fatalf("warm fixed+QEXT multiframe multistream DecodeInt16 allocations=%g want 0", allocs)
		}

		multiDec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
		if err != nil {
			t.Fatalf("NewMultistreamDecoder multiframe int24: %v", err)
		}
		multiGot24 := make([]int32, 0, len(wantMulti24))
		multiOut24 := make([]int32, multiFrameSize*channels)
		for i := range multiPackets {
			n, err := multiDec24.DecodeInt24(multiPackets[i], multiOut24)
			if err != nil || n != multiFrameSize {
				t.Fatalf("multiframe DecodeInt24 packet %d returned samples=%d, err=%v; want %d", i, n, err, multiFrameSize)
			}
			multiGot24 = append(multiGot24, multiOut24...)
		}
		assertFixedExact(t, "fixed+QEXT multiframe multistream int24", multiGot24, wantMulti24)
		if allocs := testing.AllocsPerRun(100, func() {
			if n, err := multiDec24.DecodeInt24(multiPacket, multiOut24); err != nil || n != multiFrameSize {
				t.Fatalf("warm multiframe DecodeInt24 returned samples=%d, err=%v; want %d", n, err, multiFrameSize)
			}
		}); allocs != 0 {
			t.Fatalf("warm fixed+QEXT multiframe multistream DecodeInt24 allocations=%g want 0", allocs)
		}
	})
}
