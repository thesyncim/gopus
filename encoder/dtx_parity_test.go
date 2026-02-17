// dtx_parity_test.go validates gopus DTX behavior against libopus decide_dtx_mode()
// and the SILK DTX state machine (silk/fixed/encode_frame_FIX.c:61-80).
//
// Reference implementation:
//   - decide_dtx_mode():  tmp_check/opus-1.6.1/src/opus_encoder.c:1115-1140
//   - SILK DTX counters:  tmp_check/opus-1.6.1/silk/fixed/encode_frame_FIX.c:61-80
//   - DTX constants:      tmp_check/opus-1.6.1/silk/define.h
//   - DTX packet format:  tmp_check/opus-1.6.1/src/opus_encoder.c:2565-2572
package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestDecideDTXMode_ExactLibopusParity exercises the decide_dtx_mode logic from
// opus_encoder.c:1115-1140 for 20ms frames at 48kHz, verifying frame-by-frame
// decisions match libopus exactly.
//
// libopus decide_dtx_mode:
//
//	if (!activity) {
//	    (*nb_no_activity_ms_Q1) += frame_size_ms_Q1;
//	    if (*nb_no_activity_ms_Q1 > NB_SPEECH_FRAMES_BEFORE_DTX*20*2) {
//	        if (*nb_no_activity_ms_Q1 <= (NB_SPEECH_FRAMES_BEFORE_DTX+MAX_CONSECUTIVE_DTX)*20*2)
//	            return 1;
//	        else
//	            (*nb_no_activity_ms_Q1) = NB_SPEECH_FRAMES_BEFORE_DTX*20*2;
//	    }
//	} else
//	    (*nb_no_activity_ms_Q1) = 0;
//	return 0;
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
	silence := make([]float64, 960) // 20ms @ 48kHz

	var libopusCounterQ1 int

	// Test 50 consecutive silent frames
	for i := 0; i < 50; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		expected := libopusDecide(false, &libopusCounterQ1)

		if suppress != expected {
			t.Fatalf("frame %d: gopus=%v, libopus=%v (counterQ1=%d)",
				i, suppress, expected, libopusCounterQ1)
		}
	}

	// Now test speech interruption
	speech := make([]float64, 960)
	for i := range speech {
		speech[i] = 0.5 * math.Sin(float64(i)*2*math.Pi*440/48000)
	}

	// Reset both
	libopusDecide(true, &libopusCounterQ1)
	enc.shouldUseDTX(speech)

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
	silence := make([]float64, 960)

	// Frames 0-9: counter goes 40, 80, ..., 400. NOT > 400, so no DTX.
	for i := 0; i < 10; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		if suppress {
			t.Fatalf("frame %d: should NOT suppress (counter=%d, threshold=400 Q1)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 10: counter = 440 > 400. DTX should activate.
	suppress, _ := enc.shouldUseDTX(silence)
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
	silence := make([]float64, 960)

	// Track suppress decisions
	decisions := make([]bool, 35)
	for i := 0; i < 35; i++ {
		decisions[i], _ = enc.shouldUseDTX(silence)
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
	silence := make([]float64, 960)

	// Enter DTX mode
	for i := 0; i < 15; i++ {
		enc.shouldUseDTX(silence)
	}

	// Verify in DTX
	suppress, _ := enc.shouldUseDTX(silence)
	if !suppress {
		t.Fatal("should be in DTX mode after 15 silent frames")
	}

	// Generate speech (high-amplitude sine)
	speech := make([]float64, 960)
	for i := range speech {
		speech[i] = 0.5 * math.Sin(float64(i)*2*math.Pi*440/48000)
	}

	// Speech should exit DTX immediately
	suppress, _ = enc.shouldUseDTX(speech)
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
	silence := make([]float64, 960)

	// Initially not in DTX
	if enc.InDTX() {
		t.Error("should not be in DTX initially")
	}

	// Feed silence until DTX activates
	for i := 0; i < 11; i++ {
		enc.shouldUseDTX(silence)
	}

	// Now should be in DTX (frame 10 activated it)
	if !enc.InDTX() {
		t.Error("should be in DTX after 11 silent frames")
	}

	// Speech exits DTX
	speech := make([]float64, 960)
	for i := range speech {
		speech[i] = 0.5 * math.Sin(float64(i)*2*math.Pi*440/48000)
	}
	enc.shouldUseDTX(speech)

	if enc.InDTX() {
		t.Error("should not be in DTX after speech")
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
	silence := make([]float64, 1920) // 40ms @ 48kHz

	// Frames 0-4: counter 80,160,240,320,400. None > 400.
	for i := 0; i < 5; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		if suppress {
			t.Fatalf("frame %d (40ms): should not suppress (counter=%d)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 5: counter = 480 > 400. DTX activates.
	suppress, _ := enc.shouldUseDTX(silence)
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
	silence := make([]float64, 480) // 10ms @ 48kHz

	// Frames 0-19: counter 20,40,...,400. None > 400.
	for i := 0; i < 20; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		if suppress {
			t.Fatalf("frame %d (10ms): should not suppress (counter=%d)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 20: counter = 420 > 400. DTX activates.
	suppress, _ := enc.shouldUseDTX(silence)
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
	silence := make([]float64, 2880) // 60ms @ 48kHz

	// Frames 0-2: counter 120,240,360. None > 400.
	for i := 0; i < 3; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		if suppress {
			t.Fatalf("frame %d (60ms): should not suppress (counter=%d)",
				i, enc.dtx.noActivityMsQ1)
		}
	}

	// Frame 3: counter = 480 > 400. DTX activates.
	suppress, _ := enc.shouldUseDTX(silence)
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
	silence := make([]float64, 960)

	for i := 0; i < 100; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
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
	for i := 0; i < 11; i++ {
		packet, err := enc.Encode(silence, 960)
		if err != nil {
			t.Fatalf("frame %d: encode error: %v", i, err)
		}
		preDTXSizes = append(preDTXSizes, len(packet))
	}

	// First 10 frames should be full packets (>1 byte)
	for i := 0; i < 10; i++ {
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
		packet, err := enc.Encode(silence, 960)
		if err != nil {
			t.Fatalf("frame %d: encode error: %v", i, err)
		}
		if len(packet) != 1 {
			t.Errorf("frame %d: expected 1-byte DTX, got %d bytes", i, len(packet))
		}
	}

	// Phase 3: Overflow reset at frame 30 (full packet for 1 frame)
	packet, err := enc.Encode(silence, 960)
	if err != nil {
		t.Fatalf("frame 30: encode error: %v", err)
	}
	if len(packet) <= 1 {
		t.Errorf("frame 30: expected full packet (overflow reset), got %d bytes", len(packet))
	}

	// Phase 4: Re-enters DTX on frame 31
	packet, err = enc.Encode(silence, 960)
	if err != nil {
		t.Fatalf("frame 31: encode error: %v", err)
	}
	if len(packet) != 1 {
		t.Errorf("frame 31: expected 1-byte DTX (re-entry), got %d bytes", len(packet))
	}
}

// TestDTX_ResetClearsState verifies encoder Reset() clears all DTX state.
func TestDTX_ResetClearsState(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]float64, 960)

	// Enter DTX
	for i := 0; i < 15; i++ {
		enc.shouldUseDTX(silence)
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
	suppress, _ := enc.shouldUseDTX(silence)
	if suppress {
		t.Error("should not suppress first frame after reset")
	}
}

// TestDTX_StereoMixToMono verifies stereo input is mixed to mono for VAD.
func TestDTX_StereoMixToMono(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetDTX(true)

	// Stereo silence (interleaved L,R,L,R...)
	silence := make([]float64, 960*2)

	for i := 0; i < 15; i++ {
		enc.shouldUseDTX(silence)
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

			silence := make([]float64, tc.frameSize)
			enc.shouldUseDTX(silence)

			if enc.dtx.noActivityMsQ1 != tc.wantQ1 {
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

	silence := make([]float64, 960)
	speech := make([]float64, 960)
	for i := range speech {
		speech[i] = 0.5 * math.Sin(float64(i)*2*math.Pi*440/48000)
	}

	for cycle := 0; cycle < 5; cycle++ {
		// Enter DTX
		for i := 0; i < 15; i++ {
			enc.shouldUseDTX(silence)
		}
		if !enc.InDTX() {
			t.Fatalf("cycle %d: should be in DTX", cycle)
		}

		// Exit with speech
		enc.shouldUseDTX(speech)
		if enc.InDTX() {
			t.Fatalf("cycle %d: should exit DTX on speech", cycle)
		}
		if enc.dtx.noActivityMsQ1 != 0 {
			t.Fatalf("cycle %d: counter should be 0, got %d", cycle, enc.dtx.noActivityMsQ1)
		}
	}
}
