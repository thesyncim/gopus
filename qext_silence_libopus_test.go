//go:build gopus_qext

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func qextSilencePacket(t *testing.T, codedChannels, extensionBytes int) []byte {
	t.Helper()
	toc := byte(31 << 3) // 20 ms full-band CELT
	if codedChannels == 2 {
		toc |= 4
	}
	packet := make([]byte, 4000)
	n, err := buildPacketWithOptions(toc, [][]byte{{0xff, 0xfe}}, packet, 0, false,
		[]packetExtensionData{{ID: qextPacketExtensionID, Frame: 0, Data: make([]byte, extensionBytes)}}, false)
	if err != nil {
		t.Fatal(err)
	}
	return packet[:n]
}

func compareQEXTDecodeSequenceWithLibopus(t *testing.T, outputChannels int, packets [][]byte) {
	t.Helper()
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTDecodeSingleHelper.Path(buildLibopusQEXTDecodeSingleHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "qext stateful float decoder", err)
		return
	}
	const frameSize = 960
	samplesPerPacket := frameSize * outputChannels
	payload := libopustest.NewOraclePayloadVersion("GOSI", 8, 0, 48000, 0, uint32(outputChannels), frameSize, uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0) // decode_fec
		payload.U32(frameSize)
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "qext stateful float decoder", "GOSO", 3)
	if err != nil {
		t.Fatal(err)
	}
	wantPCM := make([]float32, len(packets)*samplesPerPacket)
	if count := reader.Count(len(wantPCM)); count != len(wantPCM) {
		t.Fatalf("C PCM count %d, want %d", count, len(wantPCM))
	}
	for i := range wantPCM {
		wantPCM[i] = reader.Float32()
	}
	if count := reader.Count(len(packets)); count != len(packets) {
		t.Fatalf("C record count %d, want %d", count, len(packets))
	}
	wantRanges := make([]uint32, len(packets))
	for i := range packets {
		status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
		if status != 0 || samples != frameSize || offset != uint32(i*samplesPerPacket) {
			t.Fatalf("C frame %d status=%d samples=%d offset=%d", i, status, samples, offset)
		}
		wantRanges[i] = finalRange
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, outputChannels))
	if err != nil {
		t.Fatal(err)
	}
	gotPCM := make([]float32, samplesPerPacket)
	for i, packet := range packets {
		n, err := dec.Decode(packet, gotPCM)
		if err != nil || n != frameSize {
			t.Fatalf("Go frame %d samples=%d err=%v", i, n, err)
		}
		if got := dec.FinalRange(); got != wantRanges[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRanges[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}

func TestQEXTReceivedSilencePacketMatchesLibopus(t *testing.T) {
	for _, codedChannels := range []int{1, 2} {
		for _, outputChannels := range []int{1, 2} {
			for _, extensionBytes := range []int{20, 64, 128} {
				name := fmt.Sprintf("coded%d_output%d_extension%d", codedChannels, outputChannels, extensionBytes)
				t.Run(name, func(t *testing.T) {
					packet := qextSilencePacket(t, codedChannels, extensionBytes)
					compareQEXTDecodeSequenceWithLibopus(t, outputChannels, [][]byte{packet})
				})
			}
		}
	}
}

func TestQEXTReceivedSilenceRecoveryMatchesLibopus(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			bitrate, mode, modeArg := 128000, BitrateModeVBR, ""
			expectExtension := true
			if channels == 2 {
				bitrate, mode, modeArg = 96000, BitrateModeCVBR, "-cvbr"
				expectExtension = false
			}
			packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 2, channels, bitrate, mode, modeArg, expectExtension)
			if len(packets) != 2 {
				t.Fatalf("C packet count %d, want 2", len(packets))
			}
			sequence := [][]byte{packets[0], qextSilencePacket(t, channels, 64), packets[1]}
			compareQEXTDecodeSequenceWithLibopus(t, channels, sequence)
		})
	}
}
