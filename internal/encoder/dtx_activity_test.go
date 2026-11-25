package encoder

import (
	"github.com/thesyncim/gopus/types"
	"math"
	"testing"
)

func TestGetVADActivityUsesAvailableOpusDecision(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if got := enc.GetVADActivity(); got != 0 {
		t.Fatalf("initial GetVADActivity()=%d, want 0", got)
	}

	tests := []struct {
		name     string
		observed bool
		valid    bool
		prob     float32
		want     int
	}{
		{name: "not observed", valid: true, prob: 0.5, want: 0},
		{name: "unavailable decision", observed: true, prob: 0.5, want: 0},
		{name: "zero activity", observed: true, valid: true, prob: 0, want: 0},
		{name: "fractional Q8", observed: true, valid: true, prob: 0.1, want: 25},
		{name: "half scale", observed: true, valid: true, prob: 0.5, want: 128},
		{name: "one saturates", observed: true, valid: true, prob: 1, want: 255},
		{name: "above one saturates", observed: true, valid: true, prob: 1.5, want: 255},
		{name: "negative clamps", observed: true, valid: true, prob: -0.5, want: 0},
		{name: "positive infinity saturates", observed: true, valid: true, prob: float32(math.Inf(1)), want: 255},
		{name: "NaN clamps", observed: true, valid: true, prob: float32(math.NaN()), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc.lastOpusVADActivityObserved = tt.observed
			enc.lastOpusVADValid = tt.valid
			enc.lastOpusVADProb = tt.prob
			enc.lastOpusVADActive = tt.prob >= 0.1
			beforeValid := enc.lastOpusVADValid
			beforeActive := enc.lastOpusVADActive
			beforeProb := math.Float32bits(enc.lastOpusVADProb)
			if got := enc.GetVADActivity(); got != tt.want {
				t.Fatalf("GetVADActivity()=%d, want %d", got, tt.want)
			}
			if got := enc.GetVADActivity(); got < 0 || got > 255 {
				t.Fatalf("GetVADActivity()=%d outside Q8 range", got)
			}
			if enc.lastOpusVADValid != beforeValid || enc.lastOpusVADActive != beforeActive || math.Float32bits(enc.lastOpusVADProb) != beforeProb {
				t.Fatal("GetVADActivity mutated the encoder decision state")
			}
		})
	}

	enc.lastOpusVADActivityObserved = true
	enc.lastOpusVADValid = true
	enc.lastOpusVADActive = true
	enc.lastOpusVADProb = 0.75
	enc.Reset()
	if got := enc.GetVADActivity(); got != 0 {
		t.Fatalf("GetVADActivity() after Reset=%d, want 0", got)
	}
	if !enc.lastOpusVADValid || !enc.lastOpusVADActive || enc.lastOpusVADProb != 0.75 {
		t.Fatal("getter availability reset changed the encoder decision state")
	}

	enc.lastOpusVADActivityObserved = true
	enc.lastOpusVADValid = true
	enc.lastOpusVADProb = 0.5
	if n := testing.AllocsPerRun(100, func() { _ = enc.GetVADActivity() }); n != 0 {
		t.Fatalf("GetVADActivity allocations=%v, want 0", n)
	}
}

func TestBuildDTXPacketSILK(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	packet, err := enc.buildDTXPacket(960)
	if err != nil {
		t.Fatalf("buildDTXPacket failed: %v", err)
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet should be 1 byte (TOC only), got %d", len(packet))
	}

	toc := packet[0]
	config := toc >> 3
	frameCode := toc & 0x03
	if frameCode != 0 {
		t.Fatalf("frameCode = %d, want 0", frameCode)
	}
	if int(config) >= len(configTable) {
		t.Fatalf("invalid config=%d", config)
	}
	if configTable[config].Mode != types.ModeSILK {
		t.Fatalf("TOC mode = %v, want %v", configTable[config].Mode, types.ModeSILK)
	}
}

func TestBuildDTXPacketCELT(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	packet, err := enc.buildDTXPacket(960)
	if err != nil {
		t.Fatalf("buildDTXPacket failed: %v", err)
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet should be 1 byte (TOC only), got %d", len(packet))
	}

	toc := packet[0]
	config := toc >> 3
	if int(config) >= len(configTable) {
		t.Fatalf("invalid config=%d", config)
	}
	if configTable[config].Mode != types.ModeCELT {
		t.Fatalf("TOC mode = %v, want %v", configTable[config].Mode, types.ModeCELT)
	}
}

func TestBuildDTXPacketStereo(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	packet, err := enc.buildDTXPacket(960)
	if err != nil {
		t.Fatalf("buildDTXPacket failed: %v", err)
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet should be 1 byte (TOC only), got %d", len(packet))
	}

	toc := packet[0]
	stereo := (toc & 0x04) != 0
	if !stereo {
		t.Fatal("stereo bit should be set for 2-channel encoder")
	}
}

func TestShouldUseDTXFrameDurationUsesConfiguredSampleRate(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)

	if _, _ = enc.shouldUseDTXRes(make([]opusRes, 960)); enc.dtx.frameDurationMs != 20 {
		t.Fatalf("frameDurationMs(960@48k) = %d, want 20", enc.dtx.frameDurationMs)
	}

	if _, _ = enc.shouldUseDTXRes(make([]opusRes, 1920)); enc.dtx.frameDurationMs != 40 {
		t.Fatalf("frameDurationMs(1920@48k) = %d, want 40", enc.dtx.frameDurationMs)
	}

	if _, _ = enc.shouldUseDTXRes(make([]opusRes, 2880)); enc.dtx.frameDurationMs != 60 {
		t.Fatalf("frameDurationMs(2880@48k) = %d, want 60", enc.dtx.frameDurationMs)
	}
}

func TestShouldUseDTXDoesNotSuppressBeforeThresholdAt48k20ms(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960) // 20ms @ 48kHz

	// DTX must not suppress before the configured 200ms threshold.
	for i := range DTXFrameThreshold {
		suppress, _ := enc.shouldUseDTXRes(silence)
		if suppress {
			t.Fatalf("suppressed too early at frame %d (threshold=%d)", i, DTXFrameThreshold)
		}
	}
}

// TestDTXMultiFrameCELTHybridTOC pins the multi-frame (>20 ms) CELT/Hybrid DTX
// TOC-only packet. CELT and Hybrid have no single-frame TOC config beyond 20 ms,
// so a 40/60 ms packet in those modes is assembled from N=frameSize/960 internal
// 20 ms frames. When DTX fires for such a frame, libopus' per-sub-frame encode
// returns a 1-byte TOC for each sub-frame and the repacketizer collapses them to
// a TOC-only packet: code 1 for 2 sub-frames (1 byte total) or code 3 with a
// frame-count byte for 3 sub-frames (2 bytes total).
//
// This is the byte-exact target captured from the same-arch libopus
// opus_encode_float oracle (encode_stateful_transition_fuzz_test.go DTX-run
// sweep). Before the fix gopus' DTX path tried to build a SINGLE-frame TOC for
// the full 40/60 ms duration, which has no CELT/Hybrid config, and returned
// ErrInvalidConfig instead of the packet. This regression pins the framing so it
// stays byte-exact without needing the C oracle present.
func TestDTXMultiFrameCELTHybridTOC(t *testing.T) {
	const fs = 48000
	// Drive enough true-silence frames to cross the DTX-fire threshold
	// (NB_SPEECH_FRAMES_BEFORE_DTX*20*2 = 400 Q1) with margin at every duration.
	const silentFrames = 20

	type kase struct {
		name      string
		mode      Mode
		bw        types.Bandwidth
		channels  int
		frameSize int
		// wantTOC is the leading TOC byte; wantLen the full DTX packet length.
		wantTOC byte
		wantLen int
		// wantCount is the code-3 frame-count byte (only checked when wantLen==2).
		wantCount byte
	}
	cases := []kase{
		// CELT FB: config 31 (0xf8>>3). 40 ms -> code 1 (2 frames), 1 byte.
		{"celt_fb_40ms_mono", ModeCELT, types.BandwidthFullband, 1, 1920, 0xf9, 1, 0},
		// 60 ms -> code 3 (3 frames) + count byte 0x03, 2 bytes.
		{"celt_fb_60ms_mono", ModeCELT, types.BandwidthFullband, 1, 2880, 0xfb, 2, 0x03},
		// Stereo sets the TOC stereo bit (0x04).
		{"celt_fb_40ms_stereo", ModeCELT, types.BandwidthFullband, 2, 1920, 0xfd, 1, 0},
		{"celt_fb_60ms_stereo", ModeCELT, types.BandwidthFullband, 2, 2880, 0xff, 2, 0x03},
		// CELT WB: config 23. 40 ms code 1.
		{"celt_wb_40ms_mono", ModeCELT, types.BandwidthWideband, 1, 1920, 0xb9, 1, 0},
		// Hybrid FB: config 15 (0x78>>3). 40 ms code 1.
		{"hybrid_fb_40ms_mono", ModeHybrid, types.BandwidthFullband, 1, 1920, 0x79, 1, 0},
		{"hybrid_fb_60ms_mono", ModeHybrid, types.BandwidthFullband, 1, 2880, 0x7b, 2, 0x03},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc := NewEncoder(fs, c.channels)
			enc.SetMode(c.mode)
			enc.SetBandwidth(c.bw)
			enc.SetMaxBandwidth(c.bw)
			enc.SetFrameSize(c.frameSize)
			enc.SetBitrate(32000)
			enc.SetBitrateMode(ModeVBR)
			enc.SetComplexity(10)
			enc.SetDTX(true)
			if c.channels == 2 {
				enc.SetForceChannels(2)
			}

			silence := make([]float32, c.frameSize*c.channels)
			var pkt []byte
			fired := false
			for f := range silentFrames {
				p, err := enc.EncodeFloat32(silence, c.frameSize)
				if err != nil {
					t.Fatalf("frame %d: EncodeFloat32 error: %v", f, err)
				}
				// DTX TOC-only packet is at most 2 bytes; the first such frame marks
				// DTX has fired. Capture it.
				if len(p) <= 2 && len(p) >= 1 {
					pkt = append([]byte(nil), p...)
					fired = true
					break
				}
			}
			if !fired {
				t.Fatalf("%s: DTX never fired within %d silent frames", c.name, silentFrames)
			}
			if len(pkt) != c.wantLen {
				t.Fatalf("%s: DTX packet len=%d want %d (toc=%#02x)", c.name, len(pkt), c.wantLen, pkt[0])
			}
			if pkt[0] != c.wantTOC {
				t.Errorf("%s: DTX TOC=%#02x want %#02x", c.name, pkt[0], c.wantTOC)
			}
			if c.wantLen == 2 && pkt[1] != c.wantCount {
				t.Errorf("%s: DTX code-3 count byte=%#02x want %#02x", c.name, pkt[1], c.wantCount)
			}
		})
	}
}

func TestDecideDTXMode_ExactLibopusParity(t *testing.T) {
	// Reference constants from silk/define.h
	const (
		nbSpeechFramesBeforeDTX = 10 // NB_SPEECH_FRAMES_BEFORE_DTX
		maxConsecutiveDTX       = 20 // MAX_CONSECUTIVE_DTX
		frameSizeMsQ1           = 40 // 20ms * 2 (Q1 format)
	)

	// Simulate libopus decide_dtx_mode for comparison
	libopusDecide := func(activity bool, counterQ1 *int) bool {
		if !activity {
			*counterQ1 += frameSizeMsQ1
			threshQ1 := nbSpeechFramesBeforeDTX * 20 * 2
			maxQ1 := (nbSpeechFramesBeforeDTX + maxConsecutiveDTX) * 20 * 2
			if *counterQ1 > threshQ1 {
				if *counterQ1 <= maxQ1 {
					return true // DTX
				}
				*counterQ1 = threshQ1 // reset
			}
		} else {
			*counterQ1 = 0
		}
		return false
	}

	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960) // 20ms @ 48kHz

	var libopusCounterQ1 int

	// Test 50 consecutive silent frames
	for i := range 50 {
		suppress, _ := enc.shouldUseDTXRes(silence)
		expected := libopusDecide(false, &libopusCounterQ1)

		if suppress != expected {
			t.Fatalf("frame %d: gopus=%v, libopus=%v (counterQ1=%d)",
				i, suppress, expected, libopusCounterQ1)
		}
	}

	// Now test speech interruption
	speech := make([]opusRes, 960)
	for i := range speech {
		speech[i] = opusRes(0.5 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}

	// Reset both
	libopusDecide(true, &libopusCounterQ1)
	enc.shouldUseDTXRes(speech)

	// Verify counter reset
	if libopusCounterQ1 != 0 {
		t.Fatalf("libopus counter should be 0 after speech, got %d", libopusCounterQ1)
	}
	if enc.dtx.noActivityMsQ1 != 0 {
		t.Fatalf("gopus counter should be 0 after speech, got %d", enc.dtx.noActivityMsQ1)
	}
}

// TestDTX_ThresholdExact verifies DTX activates at exactly the right frame.
// libopus: DTX fires when nb_no_activity_ms_Q1 > NB_SPEECH_FRAMES_BEFORE_DTX*20*2 = 400 (Q1)
// At 20ms frames, that's frame index 10 (0-indexed), since frame 10 increments counter to 440 > 400.
func TestDTX_ThresholdExact(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	// Frames 0-9: counter goes 40, 80, ..., 400. NOT > 400, so no DTX.
	for i := range 10 {
		suppress, _ := enc.shouldUseDTXRes(silence)
		if suppress {
			t.Fatalf("frame %d: should NOT suppress (counter=%d, threshold=400 Q1)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 10: counter = 440 > 400. DTX should activate.
	suppress, _ := enc.shouldUseDTXRes(silence)
	if !suppress {
		t.Fatalf("frame 10: should suppress (counter=%d, threshold=400 Q1)",
			enc.dtx.noActivityMsQ1)
	}
}

// TestDTX_MaxConsecutiveReset verifies the counter overflow reset.
// After NB_SPEECH_FRAMES_BEFORE_DTX + MAX_CONSECUTIVE_DTX = 30 silent frames,
// the counter exceeds (10+20)*20*2 = 1200 Q1 and resets to 400.
// Frame 30 (counter=1240) causes reset: DTX OFF for that frame.
// Frame 31 (counter=440) re-enters DTX.
func TestDTX_MaxConsecutiveReset(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	// Track suppress decisions
	decisions := make([]bool, 35)
	for i := range 35 {
		decisions[i], _ = enc.shouldUseDTXRes(silence)
	}

	// Frames 0-9: no DTX (building up counter)
	for i := 0; i <= 9; i++ {
		if decisions[i] {
			t.Errorf("frame %d: should not suppress (pre-threshold)", i)
		}
	}

	// Frames 10-29: DTX active
	for i := 10; i <= 29; i++ {
		if !decisions[i] {
			t.Errorf("frame %d: should suppress (DTX active)", i)
		}
	}

	// Frame 30: counter overflows, reset, DTX OFF
	if decisions[30] {
		t.Error("frame 30: should NOT suppress (counter overflow reset)")
	}

	// Frame 31: re-enters DTX (counter 440 > 400)
	if !decisions[31] {
		t.Error("frame 31: should suppress (re-entered DTX after reset)")
	}
}

// TestDTX_SpeechExitsImmediately verifies DTX exits immediately on speech.
func TestDTX_SpeechExitsImmediately(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	// Enter DTX mode
	for range 15 {
		enc.shouldUseDTXRes(silence)
	}

	// Verify in DTX
	suppress, _ := enc.shouldUseDTXRes(silence)
	if !suppress {
		t.Fatal("should be in DTX mode after 15 silent frames")
	}

	// Generate speech (high-amplitude sine)
	speech := make([]opusRes, 960)
	for i := range speech {
		speech[i] = opusRes(0.5 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}

	// Speech should exit DTX immediately
	suppress, _ = enc.shouldUseDTXRes(speech)
	if suppress {
		t.Fatal("speech should exit DTX immediately")
	}

	// Counter should be reset
	if enc.dtx.noActivityMsQ1 != 0 {
		t.Errorf("counter should be 0 after speech, got %d", enc.dtx.noActivityMsQ1)
	}
	if enc.dtx.inDTXMode {
		t.Error("inDTXMode should be false after speech")
	}
}

// TestDTX_InDTXGetterMatchesLibopus verifies InDTX() matches OPUS_GET_IN_DTX.
// In libopus: *value = st->nb_no_activity_ms_Q1 >= NB_SPEECH_FRAMES_BEFORE_DTX*20*2
func TestDTX_InDTXGetterMatchesLibopus(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	// Initially not in DTX
	if enc.InDTX() {
		t.Error("should not be in DTX initially")
	}

	// Feed silence until DTX activates
	for range 11 {
		enc.shouldUseDTXRes(silence)
	}

	// Now should be in DTX (frame 10 activated it)
	if !enc.InDTX() {
		t.Error("should be in DTX after 11 silent frames")
	}

	// Speech exits DTX
	speech := make([]opusRes, 960)
	for i := range speech {
		speech[i] = opusRes(0.5 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}
	enc.shouldUseDTXRes(speech)

	if enc.InDTX() {
		t.Error("should not be in DTX after speech")
	}
}

func TestDTX_DisableDoesNotResetCounterUntilEncode(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	for range 11 {
		enc.shouldUseDTXRes(silence)
	}
	if !enc.InDTX() {
		t.Fatal("should be in DTX before disabling")
	}

	enc.SetDTX(false)
	if enc.InDTX() {
		t.Fatal("InDTX()=true after SetDTX(false), want false")
	}
	if enc.dtx.noActivityMsQ1 == 0 {
		t.Fatal("SetDTX(false) reset noActivityMsQ1; libopus only toggles use_dtx")
	}

	enc.SetDTX(true)
	if !enc.InDTX() {
		t.Fatal("SetDTX(true) should expose preserved DTX counter")
	}
}

func TestDTX_DisabledEncodeClearsCounter(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	for range 11 {
		enc.shouldUseDTXRes(silence)
	}
	if !enc.InDTX() {
		t.Fatal("should be in DTX before disabling")
	}

	enc.SetDTX(false)
	suppress, _ := enc.shouldUseDTXRes(silence)
	if suppress {
		t.Fatal("disabled DTX suppressed a frame")
	}
	if enc.dtx.noActivityMsQ1 != 0 {
		t.Fatalf("disabled encode left noActivityMsQ1=%d want 0", enc.dtx.noActivityMsQ1)
	}
	if enc.dtx.inDTXMode {
		t.Fatal("disabled encode left inDTXMode=true")
	}
}

// TestDTX_40msFrames verifies DTX timing with 40ms frames (1920 samples at 48kHz).
// At 40ms per frame, DTX threshold of 200ms = 5 frames.
// frameSizeMsQ1 = 40 * 2 = 80
// Threshold: counter > 400 Q1 → frame 5 (counter=400 NOT > 400), frame 5 at 80*5=400 not triggered.
// Actually: frame 5 → counter 5*80=400, NOT > 400. Frame 6 → 480 > 400. DTX at frame 5.
// Wait: 0-indexed. Frame 0→80, frame 1→160, ..., frame 4→400 (NOT >400). Frame 5→480 (>400). DTX!
func TestDTX_40msFrames(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 1920) // 40ms @ 48kHz

	// Frames 0-4: counter 80,160,240,320,400. None > 400.
	for i := range 5 {
		suppress, _ := enc.shouldUseDTXRes(silence)
		if suppress {
			t.Fatalf("frame %d (40ms): should not suppress (counter=%d)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 5: counter = 480 > 400. DTX activates.
	suppress, _ := enc.shouldUseDTXRes(silence)
	if !suppress {
		t.Fatalf("frame 5 (40ms): should suppress (counter=%d)", enc.dtx.noActivityMsQ1)
	}
}

// TestDTX_10msFrames verifies DTX timing with 10ms frames (480 samples at 48kHz).
// At 10ms per frame, DTX threshold of 200ms = 20 frames.
// frameSizeMsQ1 = 10 * 2 = 20
// Frame 19: counter 20*20=400. NOT > 400. Frame 20: 420 > 400. DTX!
func TestDTX_10msFrames(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 480) // 10ms @ 48kHz

	// Frames 0-19: counter 20,40,...,400. None > 400.
	for i := range 20 {
		suppress, _ := enc.shouldUseDTXRes(silence)
		if suppress {
			t.Fatalf("frame %d (10ms): should not suppress (counter=%d)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 20: counter = 420 > 400. DTX activates.
	suppress, _ := enc.shouldUseDTXRes(silence)
	if !suppress {
		t.Fatalf("frame 20 (10ms): should suppress (counter=%d)", enc.dtx.noActivityMsQ1)
	}
}

// TestDTX_60msFrames verifies DTX timing with 60ms frames (2880 samples at 48kHz).
// frameSizeMsQ1 = 60 * 2 = 120
// Frame 0→120, frame 1→240, frame 2→360, frame 3→480 (>400). DTX at frame 3.
func TestDTX_60msFrames(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 2880) // 60ms @ 48kHz

	// Frames 0-2: counter 120,240,360. None > 400.
	for i := range 3 {
		suppress, _ := enc.shouldUseDTXRes(silence)
		if suppress {
			t.Fatalf("frame %d (60ms): should not suppress (counter=%d)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 3: counter = 480 > 400. DTX activates.
	suppress, _ := enc.shouldUseDTXRes(silence)
	if !suppress {
		t.Fatalf("frame 3 (60ms): should suppress (counter=%d)", enc.dtx.noActivityMsQ1)
	}
}

// TestDTX_TOCPacketFormat verifies the DTX packet matches libopus format.
// libopus (opus_encoder.c:2570):
//
//	data[0] = gen_toc(st->mode, st->Fs/frame_size, curr_bandwidth, st->stream_channels);
//	return 1;
//
// The 1-byte TOC must encode mode, bandwidth, frame size, and stereo correctly.
func TestDTX_TOCPacketFormat(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		bw        types.Bandwidth
		frameSize int
		channels  int
		stereo    bool
	}{
		{"SILK NB 20ms mono", ModeSILK, types.BandwidthNarrowband, 960, 1, false},
		{"SILK WB 20ms mono", ModeSILK, types.BandwidthWideband, 960, 1, false},
		{"SILK WB 20ms stereo", ModeSILK, types.BandwidthWideband, 960, 2, true},
		{"CELT FB 20ms mono", ModeCELT, types.BandwidthFullband, 960, 1, false},
		{"CELT FB 20ms stereo", ModeCELT, types.BandwidthFullband, 960, 2, true},
		{"Hybrid SWB 20ms mono", ModeHybrid, types.BandwidthSuperwideband, 960, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, tc.channels)
			enc.SetMode(tc.mode)
			enc.SetBandwidth(tc.bw)
			enc.SetDTX(true)

			packet, err := enc.buildDTXPacket(tc.frameSize)
			if err != nil {
				t.Fatalf("buildDTXPacket: %v", err)
			}

			// Must be exactly 1 byte
			if len(packet) != 1 {
				t.Fatalf("DTX packet length = %d, want 1", len(packet))
			}

			// Parse TOC
			toc := packet[0]
			config := int(toc >> 3)
			stereo := (toc & 0x04) != 0
			frameCode := toc & 0x03

			// Verify stereo bit
			if stereo != tc.stereo {
				t.Errorf("stereo = %v, want %v", stereo, tc.stereo)
			}

			// Frame code 0 = single frame (with no data = DTX)
			if frameCode != 0 {
				t.Errorf("frameCode = %d, want 0", frameCode)
			}

			// Verify config matches expected mode
			if config < 0 || config >= len(configTable) {
				t.Fatalf("invalid config=%d", config)
			}
			entry := configTable[config]
			wantMode := modeToTypes(tc.mode)
			if entry.Mode != wantMode {
				t.Errorf("TOC mode = %v, want %v", entry.Mode, wantMode)
			}
		})
	}
}

// TestDTX_DisabledNeverSuppresses verifies DTX does nothing when disabled.
func TestDTX_DisabledNeverSuppresses(t *testing.T) {
	enc := NewEncoder(48000, 1)
	// DTX disabled (default)
	silence := make([]opusRes, 960)

	for i := range 100 {
		suppress, _ := enc.shouldUseDTXRes(silence)
		if suppress {
			t.Fatalf("frame %d: DTX should never suppress when disabled", i)
		}
	}
}

// TestDTX_FullEncodeCycle tests the full Encode() path with DTX,
// verifying packet sizes match the expected 1-byte TOC for DTX.
func TestDTX_FullEncodeCycle(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	silence := make([]float64, 960)

	// Phase 1: Pre-DTX (full packets)
	var preDTXSizes []int
	for i := range 11 {
		packet, err := encodeTest(enc, silence, 960)
		if err != nil {
			t.Fatalf("frame %d: encode error: %v", i, err)
		}
		preDTXSizes = append(preDTXSizes, len(packet))
	}

	// First 10 frames should be full packets (>1 byte)
	for i := range 10 {
		if preDTXSizes[i] <= 1 {
			t.Errorf("frame %d: expected full packet, got %d bytes", i, preDTXSizes[i])
		}
	}

	// Frame 10 (index 10, the 11th frame) should be DTX (1 byte)
	if preDTXSizes[10] != 1 {
		t.Errorf("frame 10: expected 1-byte DTX, got %d bytes", preDTXSizes[10])
	}

	// Phase 2: Sustained DTX (1-byte packets)
	for i := 11; i < 30; i++ {
		packet, err := encodeTest(enc, silence, 960)
		if err != nil {
			t.Fatalf("frame %d: encode error: %v", i, err)
		}
		if len(packet) != 1 {
			t.Errorf("frame %d: expected 1-byte DTX, got %d bytes", i, len(packet))
		}
	}

	// Phase 3: Overflow reset at frame 30 (full packet for 1 frame)
	packet, err := encodeTest(enc, silence, 960)
	if err != nil {
		t.Fatalf("frame 30: encode error: %v", err)
	}
	if len(packet) <= 1 {
		t.Errorf("frame 30: expected full packet (overflow reset), got %d bytes", len(packet))
	}

	// Phase 4: Re-enters DTX on frame 31
	packet, err = encodeTest(enc, silence, 960)
	if err != nil {
		t.Fatalf("frame 31: encode error: %v", err)
	}
	if len(packet) != 1 {
		t.Errorf("frame 31: expected 1-byte DTX (re-entry), got %d bytes", len(packet))
	}
}

func TestDTXPacketFinalRangeIsZero(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	silence := make([]float64, 960)
	var packet []byte
	for i := range 11 {
		var err error
		packet, err = encodeTest(enc, silence, 960)
		if err != nil {
			t.Fatalf("frame %d: encode error: %v", i, err)
		}
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet length=%d want 1", len(packet))
	}
	if got := enc.FinalRange(); got != 0 {
		t.Fatalf("FinalRange after DTX packet=0x%08x want 0", got)
	}
}

// TestDTX_ResetClearsState verifies encoder Reset() clears all DTX state.
func TestDTX_ResetClearsState(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]opusRes, 960)

	// Enter DTX
	for range 15 {
		enc.shouldUseDTXRes(silence)
	}
	if !enc.InDTX() {
		t.Fatal("should be in DTX before reset")
	}

	enc.Reset()

	// All DTX state should be cleared
	if enc.InDTX() {
		t.Error("InDTX should be false after reset")
	}
	if enc.dtx.noActivityMsQ1 != 0 {
		t.Errorf("noActivityMsQ1 should be 0 after reset, got %d", enc.dtx.noActivityMsQ1)
	}
	if enc.dtx.inDTXMode {
		t.Error("inDTXMode should be false after reset")
	}

	// Should not suppress immediately after reset
	suppress, _ := enc.shouldUseDTXRes(silence)
	if suppress {
		t.Error("should not suppress first frame after reset")
	}
}

// TestDTX_StereoMixToMono verifies stereo input is mixed to mono for VAD.
func TestDTX_StereoMixToMono(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetDTX(true)

	// Stereo silence (interleaved L,R,L,R...)
	silence := make([]opusRes, 960*2)

	for range 15 {
		enc.shouldUseDTXRes(silence)
	}

	if !enc.InDTX() {
		t.Error("stereo silence should enter DTX")
	}
}

// TestDTX_Q1TimingAccuracy verifies Q1 counter arithmetic is exact.
func TestDTX_Q1TimingAccuracy(t *testing.T) {
	tests := []struct {
		name      string
		frameSize int // samples at 48kHz
		wantQ1    int // expected Q1 increment per frame
	}{
		{"10ms", 480, 20},
		{"20ms", 960, 40},
		{"40ms", 1920, 80},
		{"60ms", 2880, 120},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetDTX(true)

			silence := make([]opusRes, tc.frameSize)
			enc.shouldUseDTXRes(silence)

			if enc.dtx.noActivityMsQ1 != int32(tc.wantQ1) {
				t.Errorf("after 1 frame: noActivityMsQ1 = %d, want %d",
					enc.dtx.noActivityMsQ1, tc.wantQ1)
			}
		})
	}
}

// TestDTX_MultipleReentries tests repeated DTX→speech→DTX transitions.
func TestDTX_MultipleReentries(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)

	silence := make([]opusRes, 960)
	speech := make([]opusRes, 960)
	for i := range speech {
		speech[i] = opusRes(0.5 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}

	for cycle := range 5 {
		// Enter DTX
		for range 15 {
			enc.shouldUseDTXRes(silence)
		}
		if !enc.InDTX() {
			t.Fatalf("cycle %d: should be in DTX", cycle)
		}

		// Exit with speech
		enc.shouldUseDTXRes(speech)
		if enc.InDTX() {
			t.Fatalf("cycle %d: should exit DTX on speech", cycle)
		}
		if enc.dtx.noActivityMsQ1 != 0 {
			t.Fatalf("cycle %d: counter should be 0, got %d", cycle, enc.dtx.noActivityMsQ1)
		}
	}
}
