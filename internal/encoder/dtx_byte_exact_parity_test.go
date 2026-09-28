// dtx_byte_exact_parity_test.go asserts byte-identical packet streams between
// gopus Encoder (DTX on, CBR) and the libopus 1.6.1 C oracle over
// speech->silence->speech sequences.
//
// Where the DTX cadence test (dtx_sequence_parity_test.go) only checks DTX vs
// normal cadence and the TOC byte of DTX packets, this file hardens the whole
// stream: every packet (speech frames AND 1-byte DTX continuation packets) must
// match libopus byte-for-byte on every selected instruction lane.
//
// Reference:
//   decide_dtx_mode():   tmp_check/opus-1.6.1/src/opus_encoder.c:1115-1140
//   DTX packet gen_toc:  tmp_check/opus-1.6.1/src/opus_encoder.c:2564-2572
//   is_digital_silence:  tmp_check/opus-1.6.1/src/opus_encoder.c:1060-1077
//
//go:build gopus_libopus_oracle

package encoder

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// runDTXOracleCBR runs the C oracle in CBR mode (OPUS_SET_VBR(0)) and returns
// the emitted packets. It mirrors runDTXOracle but flips GOPUS_DTX_CBR=1.
func runDTXOracleCBR(t *testing.T, helperPath string, pcm []float32, frameSize, channels, bitrate int, bandwidth, mode string) []dtxOraclePacket {
	return runDTXOracleCBRApp(t, helperPath, pcm, frameSize, channels, bitrate, bandwidth, mode, "audio")
}

// runDTXOracleCBRApp runs the CBR oracle with a specific application string
// ("audio", "voip", "rsilk").
func runDTXOracleCBRApp(t *testing.T, helperPath string, pcm []float32, frameSize, channels, bitrate int, bandwidth, mode, app string) []dtxOraclePacket {
	t.Helper()
	env := []string{
		fmt.Sprintf("GOPUS_DTX_FRAME_SIZE=%d", frameSize),
		fmt.Sprintf("GOPUS_DTX_CHANNELS=%d", channels),
		fmt.Sprintf("GOPUS_DTX_BITRATE=%d", bitrate),
		fmt.Sprintf("GOPUS_DTX_BANDWIDTH=%s", bandwidth),
		fmt.Sprintf("GOPUS_DTX_MODE=%s", mode),
		fmt.Sprintf("GOPUS_DTX_MAX_FRAMES=%d", len(pcm)/(frameSize*channels)),
		"GOPUS_DTX_PCM_STDIN=1",
		fmt.Sprintf("GOPUS_DTX_APPLICATION=%s", app),
		"GOPUS_DTX_CBR=1",
	}
	out, err := libopustest.RunHelperEnv(helperPath, dtxPCMToLE(pcm), env)
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx cbr oracle", err)
	}
	reader, version, parseErr := libopustest.NewOracleReaderVersion("dtx cbr sequence", "GDTX", out)
	if parseErr != nil {
		t.Fatalf("parse oracle output: %v", parseErr)
	}
	if version != 1 {
		t.Fatalf("oracle version=%d want 1", version)
	}
	gotFrameSize := int(reader.U32())
	gotChannels := int(reader.U32())
	count := int(reader.U32())
	if gotFrameSize != frameSize || gotChannels != channels {
		t.Fatalf("oracle header frameSize=%d channels=%d want %d/%d",
			gotFrameSize, gotChannels, frameSize, channels)
	}
	packets := make([]dtxOraclePacket, count)
	for i := 0; i < count; i++ {
		pktLen := int(reader.U32())
		data := reader.Bytes(pktLen)
		packets[i] = dtxOraclePacket{Data: append([]byte(nil), data...)}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("oracle output not fully consumed: %v", err)
	}
	return packets
}

// runGopusDTXSequenceCBR encodes the PCM with gopus, DTX on, CBR, forced mode.
func runGopusDTXSequenceCBR(t *testing.T, pcm []float32, frameSize, channels, bitrate int, bw, modeStr string) [][]byte {
	return runGopusDTXSequenceCBRApp(t, pcm, frameSize, channels, bitrate, bw, modeStr, "audio")
}

func runGopusDTXSequenceCBRApp(t *testing.T, pcm []float32, frameSize, channels, bitrate int, bw, modeStr, app string) [][]byte {
	t.Helper()
	enc := NewEncoder(48000, channels)
	if app == "rsilk" {
		enc.SetRestrictedSilkApplication(true)
	}
	enc.SetDTX(true)
	enc.SetVBR(false)
	enc.SetComplexity(10)
	enc.SetMode(dtxGopusMode(modeStr))
	enc.SetBandwidth(dtxGopusBandwidth(bw))
	enc.SetBitrate(bitrate)
	if channels == 2 {
		enc.SetForceChannels(2)
	}

	totalFrames := len(pcm) / (frameSize * channels)
	packets := make([][]byte, 0, totalFrames)
	for i := 0; i < totalFrames; i++ {
		off := i * frameSize * channels
		frame := pcm[off : off+frameSize*channels]
		pkt, err := encodeTest(enc, frame, frameSize)
		if err != nil {
			t.Fatalf("gopus encode frame %d: %v", i, err)
		}
		if pkt == nil {
			t.Fatalf("gopus frame %d: nil packet", i)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// assertDTXByteExact asserts every packet is byte-identical.
func assertDTXByteExact(t *testing.T, label string, wantPackets []dtxOraclePacket, gotPackets [][]byte) {
	t.Helper()
	if len(gotPackets) != len(wantPackets) {
		t.Fatalf("%s: packet count gopus=%d libopus=%d", label, len(gotPackets), len(wantPackets))
	}

	dtxCount := 0
	diffFrames := 0
	var firstDiff string
	for i := range wantPackets {
		want := wantPackets[i].Data
		got := gotPackets[i]
		if len(want) == 1 {
			dtxCount++
		}
		if !bytes.Equal(got, want) {
			diffFrames++
			if firstDiff == "" {
				firstDiff = fmt.Sprintf("frame %d: gopus_len=%d libopus_len=%d toc gopus=0x%02x libopus=0x%02x",
					i, len(got), len(want), tocByte(got), tocByte(want))
			}
		}
	}

	if dtxCount == 0 {
		t.Errorf("%s: no DTX frames emitted – test did not exercise the DTX path", label)
	}

	if diffFrames == 0 {
		t.Logf("%s: byte-exact over %d packets (%d DTX)", label, len(wantPackets), dtxCount)
		return
	}

	t.Fatalf("%s: %d/%d packets differ (first: %s)", label, diffFrames, len(wantPackets), firstDiff)
}

// dtxByteExactCase is a single byte-exact DTX parity matrix cell.
type dtxByteExactCase struct {
	name          string
	frameSize     int
	channels      int
	bitrate       int
	bw            string
	mode          string
	speechFrames  int
	silenceFrames int
	resumeFrames  int
}

// TestDTXByteExactParity_Matrix asserts byte-identical packet streams for a
// representative DTX matrix: SILK NB/WB mono+stereo and 10/20/40 ms frames,
// plus Hybrid SWB. Speech frames, the pre-DTX silence, the 1-byte DTX
// continuation packets, the MAX_CONSECUTIVE_DTX overflow reset packet, and the
// resume speech frames must all match libopus exactly.
func TestDTXByteExactParity_Matrix(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx byte-exact", err)
	}

	cases := []dtxByteExactCase{
		{"silk-wb-mono-20ms", 960, 1, 24000, "wb", "silk", 30, 30, 10},
		{"silk-nb-mono-20ms", 960, 1, 12000, "nb", "silk", 25, 30, 10},
		{"silk-wb-stereo-20ms", 960, 2, 32000, "wb", "silk", 30, 30, 10},
		{"silk-wb-mono-10ms", 480, 1, 24000, "wb", "silk", 30, 40, 10},
		{"silk-wb-mono-40ms", 1920, 1, 24000, "wb", "silk", 12, 16, 6},
		// Long silence run to exercise the MAX_CONSECUTIVE_DTX overflow reset.
		{"silk-wb-mono-maxreset", 960, 1, 24000, "wb", "silk", 15, 40, 5},
		// Hybrid SWB: exact selected-C packets on every architecture.
		{"hybrid-swb-mono-20ms", 960, 1, 48000, "swb", "hybrid", 30, 30, 10},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pcm := dtxSeqPCMSequence(tc.frameSize, tc.channels, tc.speechFrames, tc.silenceFrames, tc.resumeFrames)
			wantPackets := runDTXOracleCBR(t, helperPath, pcm, tc.frameSize, tc.channels, tc.bitrate, tc.bw, tc.mode)
			gotPackets := runGopusDTXSequenceCBR(t, pcm, tc.frameSize, tc.channels, tc.bitrate, tc.bw, tc.mode)
			assertDTXByteExact(t, tc.name, wantPackets, gotPackets)
		})
	}
}

// TestDTXByteExactParity_PureSilence asserts that a pure digital-silence stream
// (literal zero PCM from the very first frame) produces byte-identical packets:
// the leading frames (counter building to NB_SPEECH_FRAMES_BEFORE_DTX) are full
// SILK silence packets, then a steady cadence of 1-byte DTX continuations.
func TestDTXByteExactParity_PureSilence(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx pure silence", err)
	}

	const (
		frameSize = 960
		channels  = 1
		bitrate   = 24000
		bw        = "wb"
		mode      = "silk"
		frames    = 40
	)
	// All-zero PCM: speechFrames=0, silenceFrames=frames, resumeFrames=0.
	pcm := dtxSeqPCMSequence(frameSize, channels, 0, frames, 0)
	wantPackets := runDTXOracleCBR(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequenceCBR(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXByteExact(t, "silk-wb-mono-pure-silence", wantPackets, gotPackets)
}

// TestDTXByteExactParity_RestrictedSilk compares the SILK-internal DTX path
// selected when tonality analysis is unavailable in restricted-SILK mode.
// The Opus-level decide_dtx_mode path is covered by the audio application cases.
func TestDTXByteExactParity_RestrictedSilk(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "restricted SILK DTX", err)
	}
	cases := []dtxByteExactCase{
		{"wb-mono-20ms", 960, 1, 24000, "wb", "silk", 30, 30, 10},
		{"wb-stereo-20ms", 960, 2, 32000, "wb", "silk", 30, 30, 10},
		{"wb-mono-10ms", 480, 1, 24000, "wb", "silk", 30, 40, 10},
		{"wb-mono-40ms", 1920, 1, 24000, "wb", "silk", 12, 16, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := dtxSeqPCMSequence(tc.frameSize, tc.channels, tc.speechFrames, tc.silenceFrames, tc.resumeFrames)
			wantPackets := runDTXOracleCBRApp(t, helperPath, pcm, tc.frameSize, tc.channels, tc.bitrate, tc.bw, tc.mode, "rsilk")
			gotPackets := runGopusDTXSequenceCBRApp(t, pcm, tc.frameSize, tc.channels, tc.bitrate, tc.bw, tc.mode, "rsilk")
			assertDTXByteExact(t, tc.name, wantPackets, gotPackets)
		})
	}
}
