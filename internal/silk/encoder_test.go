package silk

import "testing"

// newTestEncoder returns a channel state after silk_init_encoder and the rate
// half of silk_setup_fs for the internal rate of bandwidth, at complexity 0,
// for tests that drive the analysis stages directly.
func newTestEncoder(bandwidth Bandwidth) *Encoder {
	e := newEncoder()
	e.setupFs(int32(GetBandwidthConfig(bandwidth).SampleRate/1000), e.packetSizeMs)
	e.setupComplexity(0)
	return e
}

func TestNewEncoderHasNoInternalRate(t *testing.T) {
	enc := newEncoder()
	if enc.fsKHz != 0 || enc.packetSizeMs != 0 || enc.frameLength != 0 {
		t.Fatalf("fs_kHz=%d PacketSize_ms=%d frame_length=%d, want all 0 after silk_init_encoder",
			enc.fsKHz, enc.packetSizeMs, enc.frameLength)
	}
	if !enc.firstFrameAfterReset {
		t.Fatal("first_frame_after_reset should be set")
	}
}

func TestSetupFsSetsRateParameters(t *testing.T) {
	tests := []struct {
		name          string
		fsKHz         int32
		wantBandwidth Bandwidth
		wantLPCOrder  int32
	}{
		{"narrowband", 8, BandwidthNarrowband, 10},
		{"mediumband", 12, BandwidthMediumband, 10},
		{"wideband", 16, BandwidthWideband, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := newEncoder()
			enc.setupFs(tt.fsKHz, 20)
			if enc.fsKHz != tt.fsKHz || enc.bandwidth != tt.wantBandwidth || enc.lpcOrder != tt.wantLPCOrder {
				t.Fatalf("fs_kHz=%d bandwidth=%v predictLPCOrder=%d, want %d %v %d",
					enc.fsKHz, enc.bandwidth, enc.lpcOrder, tt.fsKHz, tt.wantBandwidth, tt.wantLPCOrder)
			}
			if enc.nbSubfr != maxNbSubfr || enc.frameLength != 20*tt.fsKHz || enc.nFramesPerPacket != 1 {
				t.Fatalf("nb_subfr=%d frame_length=%d nFramesPerPacket=%d, want %d %d 1",
					enc.nbSubfr, enc.frameLength, enc.nFramesPerPacket, maxNbSubfr, 20*tt.fsKHz)
			}
			// silk_setup_fs (silk/control_codec.c) sets these non-zero values
			// on a rate change.
			if enc.pitchState.prevLag != 100 || enc.nsqState.lagPrev != 100 {
				t.Errorf("prevLag=%d sNSQ.lagPrev=%d, want 100", enc.pitchState.prevLag, enc.nsqState.lagPrev)
			}
			if enc.nsqState.prevGainQ16 != 1<<16 {
				t.Errorf("sNSQ.prev_gain_Q16 = %d, want %d", enc.nsqState.prevGainQ16, 1<<16)
			}
			if enc.previousGainIndex != 10 {
				t.Errorf("LastGainIndex = %d, want 10", enc.previousGainIndex)
			}
			if !enc.firstFrameAfterReset {
				t.Error("first_frame_after_reset should be set")
			}
		})
	}
}

func TestSetupFsKeepsHistoryAtTheSameRate(t *testing.T) {
	enc := newEncoder()
	enc.setupFs(16, 20)
	enc.firstFrameAfterReset = false
	enc.previousGainIndex = 33
	enc.inputBufIx = 5
	enc.targetRateBps = 20000

	// A new packet size re-targets the SNR but keeps the analysis history.
	enc.setupFs(16, 10)
	if enc.nbSubfr != 2 || enc.frameLength != 160 || enc.targetRateBps != 0 {
		t.Fatalf("nb_subfr=%d frame_length=%d TargetRate_bps=%d, want 2 160 0", enc.nbSubfr, enc.frameLength, enc.targetRateBps)
	}
	if enc.firstFrameAfterReset || enc.previousGainIndex != 33 || enc.inputBufIx != 5 {
		t.Fatalf("packet size change reset the history: first=%v LastGainIndex=%d inputBufIx=%d",
			enc.firstFrameAfterReset, enc.previousGainIndex, enc.inputBufIx)
	}

	// A rate change resets it and the buffered input.
	enc.setupFs(12, 10)
	if enc.frameLength != 120 || !enc.firstFrameAfterReset || enc.previousGainIndex != 10 || enc.inputBufIx != 0 {
		t.Fatalf("rate change: frame_length=%d first=%v LastGainIndex=%d inputBufIx=%d, want 120 true 10 0",
			enc.frameLength, enc.firstFrameAfterReset, enc.previousGainIndex, enc.inputBufIx)
	}
}

// bandwidthControlEncoder returns a channel coding at fsKHz for a 48 kHz API
// rate with the given desired rate, as silk_control_encoder leaves it.
func bandwidthControlEncoder(fsKHz, desiredHz int32) (*Encoder, *EncControl) {
	enc := newEncoder()
	ctl := &EncControl{
		APISampleRate:             48000,
		MaxInternalSampleRate:     16000,
		MinInternalSampleRate:     8000,
		DesiredInternalSampleRate: desiredHz,
		PayloadSizeMs:             20,
		MaxBits:                   1000,
	}
	enc.apiFsHz = ctl.APISampleRate
	enc.maxInternalFsHz = ctl.MaxInternalSampleRate
	enc.minInternalFsHz = ctl.MinInternalSampleRate
	enc.desiredInternalFsHz = ctl.DesiredInternalSampleRate
	enc.setupFs(fsKHz, 20)
	return enc, ctl
}

func TestControlAudioBandwidthStartsAtDesiredRate(t *testing.T) {
	enc := newEncoder()
	enc.apiFsHz = 12000
	enc.maxInternalFsHz = 16000
	enc.minInternalFsHz = 8000
	enc.desiredInternalFsHz = 16000
	if got := enc.controlAudioBandwidth(&EncControl{}); got != 12 {
		t.Fatalf("fresh encoder rate = %d kHz, want the API rate 12", got)
	}
	enc.lpState.SavedFsKHz = 8
	if got := enc.controlAudioBandwidth(&EncControl{}); got != 8 {
		t.Fatalf("rate after a prefill 2 reset = %d kHz, want the saved 8", got)
	}
}

func TestControlAudioBandwidthClampsIntoLimits(t *testing.T) {
	enc, ctl := bandwidthControlEncoder(16, 16000)
	enc.maxInternalFsHz = 12000
	if got := enc.controlAudioBandwidth(ctl); got != 12 {
		t.Fatalf("rate above the maximum = %d kHz, want 12", got)
	}
	enc, ctl = bandwidthControlEncoder(8, 16000)
	enc.minInternalFsHz = 16000
	if got := enc.controlAudioBandwidth(ctl); got != 16 {
		t.Fatalf("rate below the minimum = %d kHz, want 16", got)
	}
	if ctl.SwitchReady {
		t.Fatal("clamping into the limits is not a signalled switch")
	}
}

func TestControlAudioBandwidthSwitchDown(t *testing.T) {
	enc, ctl := bandwidthControlEncoder(16, 12000)

	// Without permission the rate holds and nothing starts.
	if got := enc.controlAudioBandwidth(ctl); got != 16 || enc.lpState.Mode != 0 {
		t.Fatalf("switch without permission: rate %d mode %d, want 16 0", got, enc.lpState.Mode)
	}

	// With permission the LP filter starts fading the band out at double speed.
	enc.allowBandwidthSwitch = true
	if got := enc.controlAudioBandwidth(ctl); got != 16 {
		t.Fatalf("first switch-down step changed the rate to %d", got)
	}
	if enc.lpState.Mode != -2 || enc.lpState.TransitionFrameNo != transitionFrames {
		t.Fatalf("switch down started with mode %d transition %d, want -2 %d", enc.lpState.Mode, enc.lpState.TransitionFrameNo, transitionFrames)
	}
	if ctl.SwitchReady {
		t.Fatal("switchReady before the transition finished")
	}

	// Once the transition has run out the encoder asks Opus to switch and
	// leaves room for the redundant frame.
	enc.lpState.TransitionFrameNo = 0
	if got := enc.controlAudioBandwidth(ctl); got != 16 {
		t.Fatalf("switch-ready step changed the rate to %d", got)
	}
	if !ctl.SwitchReady || ctl.MaxBits != 1000-1000*5/25 {
		t.Fatalf("switchReady=%v maxBits=%d, want true %d", ctl.SwitchReady, ctl.MaxBits, 1000-1000*5/25)
	}

	// The switch happens when Opus allows it.
	ctl.SwitchReady = false
	ctl.OpusCanSwitch = true
	if got := enc.controlAudioBandwidth(ctl); got != 12 || enc.lpState.Mode != 0 {
		t.Fatalf("opusCanSwitch: rate %d mode %d, want 12 0", got, enc.lpState.Mode)
	}
}

func TestControlAudioBandwidthSwitchUp(t *testing.T) {
	enc, ctl := bandwidthControlEncoder(8, 16000)
	enc.allowBandwidthSwitch = true

	// With the LP filter idle the encoder is ready right away.
	if got := enc.controlAudioBandwidth(ctl); got != 8 || !ctl.SwitchReady {
		t.Fatalf("switch up: rate %d switchReady %v, want 8 true", got, ctl.SwitchReady)
	}

	// Opus switches one step up and the LP filter fades the band in.
	ctl.OpusCanSwitch = true
	enc.lpState.InLPState = [2]int32{3, 4}
	if got := enc.controlAudioBandwidth(ctl); got != 12 {
		t.Fatalf("opusCanSwitch rate = %d, want 12", got)
	}
	if enc.lpState.Mode != 1 || enc.lpState.TransitionFrameNo != 0 || enc.lpState.InLPState != ([2]int32{}) {
		t.Fatalf("up transition state %+v, want mode 1 from frame 0 with a cleared filter", enc.lpState)
	}

	// A running down transition turns around when the desired rate is met.
	enc, ctl = bandwidthControlEncoder(16, 16000)
	enc.allowBandwidthSwitch = true
	enc.lpState.Mode = -2
	enc.lpState.TransitionFrameNo = 100
	if got := enc.controlAudioBandwidth(ctl); got != 16 || enc.lpState.Mode != 1 {
		t.Fatalf("turnaround: rate %d mode %d, want 16 1", got, enc.lpState.Mode)
	}
	// And stops when the transition is complete.
	enc.lpState.TransitionFrameNo = transitionFrames
	enc.controlAudioBandwidth(ctl)
	if enc.lpState.Mode != 0 {
		t.Fatalf("finished transition left mode %d", enc.lpState.Mode)
	}
}

// TestSetupResamplersCarriesXBufOver checks silk_setup_resamplers on a rate
// change: x_buf goes up to the API rate through a fresh decoder-side
// resampler and back down through a fresh encoder resampler, which the
// channel keeps.
func TestSetupResamplersCarriesXBufOver(t *testing.T) {
	const apiHz = 48000
	enc := newEncoder()
	enc.apiFsHz = apiHz
	enc.setupResamplers(16)
	enc.setupFs(16, 20)
	const bufLengthMs = 4*5*2 + laShapeMs
	oldX := make([]int16, bufLengthMs*16)
	for i, v := range speechLikeSignal(16000, len(oldX), 1) {
		oldX[i] = float32ToInt16(v)
	}
	enc.xBufFromInt16(oldX)

	up := NewLibopusResampler(16000, apiHz)
	apiX := make([]int16, bufLengthMs*apiHz/1000)
	up.Resample(apiX, oldX)
	down := NewLibopusResamplerEnc(apiHz, 12000)
	wantX := make([]int16, bufLengthMs*12)
	down.Resample(wantX, apiX)

	enc.setupResamplers(12)
	gotX := make([]int16, len(wantX))
	enc.xBufToInt16(gotX)
	for i, w := range wantX {
		if gotX[i] != w {
			t.Fatalf("x_buf[%d] = %d, want %d", i, gotX[i], w)
		}
	}

	// The input resampler continues from the primed state.
	in := make([]int16, apiHz/100)
	for i := range in {
		in[i] = int16(1000 * (i % 7))
	}
	got := make([]int16, 120)
	want := make([]int16, 120)
	enc.resampler.Resample(got, in)
	down.Resample(want, in)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resampler output[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
