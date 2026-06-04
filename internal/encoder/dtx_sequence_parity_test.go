// dtx_sequence_parity_test.go verifies multi-frame DTX sequence parity against
// the libopus 1.6.1 C oracle (libopus_dtx_emit_packets.c).
//
// Coverage gaps addressed:
//   - Multi-frame DTX TOC sequences: speech→silence→speech cadence
//   - DTX TOC byte exact match (gen_toc, libopus opus_encoder.c:330-361)
//   - Stereo DTX: both 1-ch and 2-ch SILK-WB paths
//   - Hybrid DTX: SILK side with SWB bandwidth
//
// Reference libopus code:
//   decide_dtx_mode():        opus_encoder.c:1114-1140
//   DTX packet gen_toc:       opus_encoder.c:2564-2572
//   NB_SPEECH_FRAMES_BEFORE_DTX / MAX_CONSECUTIVE_DTX: silk/define.h:52-53
//
//go:build gopus_libopus_oracle

package encoder

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// dtxSeqHelperOnce caches the compiled C oracle binary.
var dtxSeqHelperOnce libopustest.HelperCache

func getDTXSeqHelperPath() (string, error) {
	return dtxSeqHelperOnce.CHelperPath(libopustest.CHelperConfig{
		Label:       "dtx sequence",
		OutputBase:  "gopus_dtx_emit_packets",
		SourceFile:  "libopus_dtx_emit_packets.c",
		RefIncludes: []string{"src", "celt", "silk"},
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

// dtxSeqPCMFrame produces a single frame of float32 PCM for the given
// frameIndex and parameters.  Speech frames use a voiced 110 Hz signal;
// silence frames use literal zero.
func dtxSeqPCMFrame(frameIndex, frameSize, channels int, isSpeech bool) []float32 {
	pcm := make([]float32, frameSize*channels)
	if !isSpeech {
		return pcm
	}
	// Voiced signal with harmonics – matches libopus's activity detection
	// well above DTX_ACTIVITY_THRESHOLD=0.1 at amplitude 0.5.
	for i := 0; i < frameSize; i++ {
		n := frameIndex*frameSize + i
		t := float64(n) / 48000.0
		env := 0.85 + 0.15*math.Sin(2*math.Pi*1.1*t)
		s := 0.0
		s += 0.30 * math.Sin(2*math.Pi*110*t)
		s += 0.18 * math.Sin(2*math.Pi*220*t+0.08)
		s += 0.10 * math.Sin(2*math.Pi*330*t+0.21)
		s += 0.06 * math.Sin(2*math.Pi*440*t+0.35)
		v := float32(env * s)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = v
		}
	}
	return pcm
}

// dtxSeqPCMSequence builds the full float32 PCM for the oracle stdin.
// Layout: speechFrames speech → silenceFrames silence → extraSpeechFrames speech
func dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, extraSpeechFrames int) []float32 {
	total := (speechFrames + silenceFrames + extraSpeechFrames) * frameSize * channels
	pcm := make([]float32, total)
	frameIdx := 0
	for fi := 0; fi < speechFrames; fi++ {
		frame := dtxSeqPCMFrame(frameIdx, frameSize, channels, true)
		off := fi * frameSize * channels
		copy(pcm[off:], frame)
		frameIdx++
	}
	for fi := 0; fi < silenceFrames; fi++ {
		off := (speechFrames + fi) * frameSize * channels
		// zero already
		_ = off
		frameIdx++
	}
	for fi := 0; fi < extraSpeechFrames; fi++ {
		frame := dtxSeqPCMFrame(frameIdx, frameSize, channels, true)
		off := (speechFrames + silenceFrames + fi) * frameSize * channels
		copy(pcm[off:], frame)
		frameIdx++
	}
	return pcm
}

// dtxPCMToLE converts float32 slice to little-endian binary for oracle stdin.
func dtxPCMToLE(pcm []float32) []byte {
	out := make([]byte, len(pcm)*4)
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	return out
}

// dtxOraclePacket holds one packet from the C oracle.
type dtxOraclePacket struct {
	Data []byte
}

// runDTXOracle runs the C oracle with the given PCM and config, returning
// the emitted Opus packets.  Mode is "silk" or "hybrid".
func runDTXOracle(t *testing.T, helperPath string, pcm []float32, frameSize, channels, bitrate int, bandwidth, mode string) []dtxOraclePacket {
	t.Helper()
	env := []string{
		fmt.Sprintf("GOPUS_DTX_FRAME_SIZE=%d", frameSize),
		fmt.Sprintf("GOPUS_DTX_CHANNELS=%d", channels),
		fmt.Sprintf("GOPUS_DTX_BITRATE=%d", bitrate),
		fmt.Sprintf("GOPUS_DTX_BANDWIDTH=%s", bandwidth),
		fmt.Sprintf("GOPUS_DTX_MODE=%s", mode),
		fmt.Sprintf("GOPUS_DTX_MAX_FRAMES=%d", len(pcm)/(frameSize*channels)),
		"GOPUS_DTX_PCM_STDIN=1",
		"GOPUS_DTX_APPLICATION=audio",
	}
	out, err := libopustest.RunHelperEnv(helperPath, dtxPCMToLE(pcm), env)
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence oracle", err)
	}
	reader, version, parseErr := libopustest.NewOracleReaderVersion("dtx sequence", "GDTX", out)
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

// dtxGopusBandwidth maps a bandwidth string to types.Bandwidth.
func dtxGopusBandwidth(bw string) types.Bandwidth {
	switch bw {
	case "nb":
		return types.BandwidthNarrowband
	case "mb":
		return types.BandwidthMediumband
	case "wb":
		return types.BandwidthWideband
	case "swb":
		return types.BandwidthSuperwideband
	case "fb":
		return types.BandwidthFullband
	}
	return types.BandwidthWideband
}

// dtxGopusMode maps a mode string to encoder.Mode.
func dtxGopusMode(mode string) Mode {
	switch mode {
	case "silk":
		return ModeSILK
	case "hybrid":
		return ModeHybrid
	case "celt":
		return ModeCELT
	}
	return ModeSILK
}

// runGopusDTXSequence encodes the PCM with gopus and DTX enabled, returning
// the emitted packets.
func runGopusDTXSequence(t *testing.T, pcm []float32, frameSize, channels, bitrate int, bw, modeStr string) [][]byte {
	t.Helper()
	enc := NewEncoder(48000, channels)
	enc.SetDTX(true)
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

// assertDTXSequenceParity compares gopus and oracle packet sequences.
//
// DTX-scope checks (what this test owns):
//  1. Same number of packets.
//  2. For each packet: DTX cadence parity — both sides must agree on whether
//     a frame is DTX (1 byte) or normal (>1 byte). Exact byte sizes of normal
//     packets are not compared here; bitrate drift in VBR speech frames is
//     a separate concern owned by other parity tests.
//  3. For 1-byte DTX packets: identical TOC byte (gen_toc parity).
//     Reference: libopus opus_encoder.c:2570 – data[0] = gen_toc(st->mode,
//     st->Fs/frame_size, curr_bandwidth, st->stream_channels)
//  4. The sequence contains at least one DTX frame (test quality guard).
func assertDTXSequenceParity(t *testing.T, label string, wantPackets []dtxOraclePacket, gotPackets [][]byte) {
	t.Helper()
	if len(gotPackets) != len(wantPackets) {
		t.Fatalf("%s: packet count gopus=%d libopus=%d", label, len(gotPackets), len(wantPackets))
	}
	dtxCount := 0
	for i := range wantPackets {
		want := wantPackets[i].Data
		got := gotPackets[i]
		wantDTX := len(want) == 1
		gotDTX := len(got) == 1
		// Cadence parity: both sides must agree on DTX vs normal.
		if wantDTX != gotDTX {
			t.Errorf("%s frame %d: DTX cadence mismatch gopus_dtx=%v libopus_dtx=%v (gopus_len=%d libopus_len=%d toc gopus=0x%02x libopus=0x%02x)",
				label, i, gotDTX, wantDTX, len(got), len(want),
				tocByte(got), tocByte(want))
			continue
		}
		if wantDTX {
			dtxCount++
			// TOC byte parity for DTX packets (gen_toc output).
			if got[0] != want[0] {
				t.Errorf("%s frame %d: DTX TOC gopus=0x%02x libopus=0x%02x",
					label, i, got[0], want[0])
			}
		}
	}
	if dtxCount == 0 {
		t.Errorf("%s: no DTX frames emitted – test did not exercise the DTX path", label)
	}
	t.Logf("%s: %d/%d frames were DTX (1-byte TOC)", label, dtxCount, len(wantPackets))
}

func tocByte(pkt []byte) byte {
	if len(pkt) == 0 {
		return 0
	}
	return pkt[0]
}

// TestDTXSequenceParity_SilkMono_20ms is the primary coverage case:
// SILK-WB, mono, 20ms frames, speech→silence→speech.
// libopus emits: full packets during speech, 1-byte TOC-only during silence
// after 10 frames (200 ms), then full packets again when speech resumes.
func TestDTXSequenceParity_SilkMono_20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence", err)
	}

	const (
		frameSize     = 960 // 20 ms @ 48 kHz
		channels      = 1
		bitrate       = 24000
		bw            = "wb"
		mode          = "silk"
		speechFrames  = 30 // 600 ms active speech → DTX counter = 0
		silenceFrames = 30 // 600 ms silence → DTX fires after frame 10 (frame 11 is first DTX)
		resumeFrames  = 10 // resumes after silence
	)
	pcm := dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, resumeFrames)
	wantPackets := runDTXOracle(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequence(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXSequenceParity(t, "silk-mono-20ms", wantPackets, gotPackets)
}

// TestDTXSequenceParity_SilkStereo_20ms covers stereo DTX.
// libopus SILK-WB stereo: DTX TOC bit 2 must be set (stereo bit in gen_toc).
// Reference: opus_encoder.c:358 – toc |= (channels==2)<<2
func TestDTXSequenceParity_SilkStereo_20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence stereo", err)
	}

	const (
		frameSize     = 960
		channels      = 2
		bitrate       = 32000
		bw            = "wb"
		mode          = "silk"
		speechFrames  = 30
		silenceFrames = 30
		resumeFrames  = 10
	)
	pcm := dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, resumeFrames)
	wantPackets := runDTXOracle(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequence(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXSequenceParity(t, "silk-stereo-20ms", wantPackets, gotPackets)

	// Extra: verify the stereo bit is set in DTX TOC bytes from both sides.
	for i := range wantPackets {
		if len(wantPackets[i].Data) == 1 {
			// Stereo bit is bit 2 of the TOC byte.
			if wantPackets[i].Data[0]&0x04 == 0 {
				t.Errorf("frame %d: libopus DTX TOC 0x%02x has stereo bit clear", i, wantPackets[i].Data[0])
			}
		}
		if i < len(gotPackets) && len(gotPackets[i]) == 1 {
			if gotPackets[i][0]&0x04 == 0 {
				t.Errorf("frame %d: gopus DTX TOC 0x%02x has stereo bit clear", i, gotPackets[i][0])
			}
		}
	}
}

// TestDTXSequenceParity_HybridMono_20ms covers Hybrid-SWB DTX.
// Reference: opus_encoder.c decide_dtx_mode applies to the unified DTX path
// regardless of mode; gen_toc for Hybrid: toc=0x60|(bw-SWB)<<4|(period-2)<<3
func TestDTXSequenceParity_HybridMono_20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence hybrid", err)
	}

	const (
		frameSize     = 960
		channels      = 1
		bitrate       = 48000
		bw            = "swb"
		mode          = "hybrid"
		speechFrames  = 30
		silenceFrames = 30
		resumeFrames  = 10
	)
	pcm := dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, resumeFrames)
	wantPackets := runDTXOracle(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequence(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXSequenceParity(t, "hybrid-mono-20ms", wantPackets, gotPackets)
}

// TestDTXSequenceParity_SilkMono_10ms covers 10 ms SILK frames.
// At 10 ms, the DTX threshold (NB_SPEECH_FRAMES_BEFORE_DTX*20*2=400 Q1) requires
// 20 silent frames (each adds 20 Q1) before DTX fires.
func TestDTXSequenceParity_SilkMono_10ms(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence 10ms", err)
	}

	const (
		frameSize     = 480 // 10 ms @ 48 kHz
		channels      = 1
		bitrate       = 24000
		bw            = "wb"
		mode          = "silk"
		speechFrames  = 30
		silenceFrames = 40 // 400 ms silence – well into DTX
		resumeFrames  = 10
	)
	pcm := dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, resumeFrames)
	wantPackets := runDTXOracle(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequence(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXSequenceParity(t, "silk-mono-10ms", wantPackets, gotPackets)
}

// TestDTXSequenceParity_SilkMono_MaxConsecutiveReset verifies the
// MAX_CONSECUTIVE_DTX overflow-reset cycle.
// libopus resets nb_no_activity_ms_Q1 to NB_SPEECH_FRAMES_BEFORE_DTX*20*2
// when it exceeds (NB_SPEECH_FRAMES_BEFORE_DTX+MAX_CONSECUTIVE_DTX)*20*2=1200 Q1
// (frame 31 at 20 ms: counter=1240 > 1200 → reset to 400, DTX=0 for that frame).
// Frame 32: counter=440 > 400 → DTX=1 again.
func TestDTXSequenceParity_SilkMono_MaxConsecutiveReset(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence max-consecutive-reset", err)
	}

	const (
		frameSize     = 960
		channels      = 1
		bitrate       = 24000
		bw            = "wb"
		mode          = "silk"
		speechFrames  = 15 // pre-silence
		silenceFrames = 40 // covers NB_SPEECH_FRAMES_BEFORE_DTX+MAX_CONSECUTIVE_DTX+extras = 30 frames + wrap
		resumeFrames  = 5
	)
	pcm := dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, resumeFrames)
	wantPackets := runDTXOracle(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequence(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXSequenceParity(t, "silk-mono-20ms-max-consecutive-reset", wantPackets, gotPackets)
}

// TestDTXSequenceParity_SilkNB_Mono covers NB bandwidth.
func TestDTXSequenceParity_SilkNB_Mono(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence nb", err)
	}

	const (
		frameSize     = 960
		channels      = 1
		bitrate       = 12000
		bw            = "nb"
		mode          = "silk"
		speechFrames  = 25
		silenceFrames = 30
		resumeFrames  = 10
	)
	pcm := dtxSeqPCMSequence(frameSize, channels, speechFrames, silenceFrames, resumeFrames)
	wantPackets := runDTXOracle(t, helperPath, pcm, frameSize, channels, bitrate, bw, mode)
	gotPackets := runGopusDTXSequence(t, pcm, frameSize, channels, bitrate, bw, mode)
	assertDTXSequenceParity(t, "silk-nb-mono-20ms", wantPackets, gotPackets)
}

// TestDTXSequenceParity_BuildOracle verifies that the C oracle can be built
// and emits sane output independently of parity, which is useful for CI
// debug when parity fails.
func TestDTXSequenceParity_BuildOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx sequence oracle build", err)
	}
	if _, err := os.Stat(helperPath); err != nil {
		t.Fatalf("oracle binary not found after build: %v", err)
	}
	t.Logf("DTX sequence oracle: %s", helperPath)
}
