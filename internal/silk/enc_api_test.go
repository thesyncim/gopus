package silk

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// testPacketEncoder drives a PacketEncoder the way the Opus layer drives
// silk_Encode for a SILK-only frame. Its API rate is the internal rate of the
// bandwidth, so the input reaches the encoder without rate conversion.
type testPacketEncoder struct {
	enc *PacketEncoder
	ctl EncControl
	re  rangecoding.Encoder
	buf [maxSilkPacketBytes]byte
}

func newTestPacketEncoder(bandwidth Bandwidth, channels int) *testPacketEncoder {
	fs := int32(GetBandwidthConfig(bandwidth).SampleRate)
	return newTestPacketEncoderAt(int(fs), bandwidth, channels)
}

// newTestPacketEncoderAt codes at the internal rate of bandwidth: it is the
// desired rate and both rate limits, so the encoder starts there and stays.
func newTestPacketEncoderAt(apiSampleRate int, bandwidth Bandwidth, channels int) *testPacketEncoder {
	fs := int32(GetBandwidthConfig(bandwidth).SampleRate)
	return &testPacketEncoder{
		enc: NewPacketEncoder(channels),
		ctl: EncControl{
			NChannelsAPI:              int32(channels),
			NChannelsInternal:         int32(channels),
			APISampleRate:             int32(apiSampleRate),
			MaxInternalSampleRate:     fs,
			MinInternalSampleRate:     fs,
			DesiredInternalSampleRate: fs,
			PayloadSizeMs:             20,
			BitRate:                   int32(24000 * channels),
			Complexity:                10,
			MaxBits:                   maxSilkPacketBytes * 8,
		},
	}
}

// encodeInto codes the interleaved pcm as one packet and returns the payload,
// the (ec_tell+7)>>3 bytes opus_encode_frame_native keeps after ec_enc_done.
// The returned slice aliases the encoder's buffer.
func (p *testPacketEncoder) encodeInto(tb testing.TB, pcm []float32, activity int) []byte {
	tb.Helper()
	n := len(pcm) / int(p.ctl.NChannelsAPI)
	p.ctl.PayloadSizeMs = int32(1000 * n / int(p.ctl.APISampleRate))
	p.re.Init(p.buf[:])
	if _, err := p.enc.Encode(&p.ctl, pcm, n, &p.re, 0, activity); err != nil {
		tb.Fatalf("Encode: %v", err)
	}
	nBytes := (p.re.Tell() + 7) >> 3
	p.re.Done()
	return p.buf[:nBytes]
}

// encode is encodeInto with the SILK VAD deciding alone, returning a copy.
func (p *testPacketEncoder) encode(tb testing.TB, pcm []float32) []byte {
	tb.Helper()
	return append([]byte(nil), p.encodeInto(tb, pcm, VADNoDecision)...)
}

// encodeTestPacket codes pcm (mono, at the internal rate of bandwidth) with a
// fresh encoder.
func encodeTestPacket(tb testing.TB, bandwidth Bandwidth, pcm []float32) []byte {
	tb.Helper()
	return newTestPacketEncoder(bandwidth, 1).encode(tb, pcm)
}

// encodeTestStereoPacket codes left/right (at the internal rate of bandwidth)
// as one stereo packet with a fresh encoder.
func encodeTestStereoPacket(tb testing.TB, bandwidth Bandwidth, left, right []float32) []byte {
	tb.Helper()
	return newTestPacketEncoder(bandwidth, 2).encode(tb, interleaveStereo(left, right))
}

func interleaveStereo(left, right []float32) []float32 {
	pcm := make([]float32, 2*len(left))
	for i := range left {
		pcm[2*i] = left[i]
		pcm[2*i+1] = right[i]
	}
	return pcm
}

// speechLikeSignal returns channels-interleaved samples of a pitched, amplitude
// modulated signal with a little noise, loud enough to keep the VAD active and
// to exercise the voiced analysis.
func speechLikeSignal(sampleRate, samples, channels int) []float32 {
	pcm := make([]float32, samples*channels)
	seed := uint32(22222)
	for i := range samples {
		tm := float64(i) / float64(sampleRate)
		env := 0.55 + 0.45*math.Sin(2*math.Pi*3*tm)
		v := env * (0.30*math.Sin(2*math.Pi*140*tm) + 0.15*math.Sin(2*math.Pi*280*tm) + 0.08*math.Sin(2*math.Pi*910*tm))
		for c := range channels {
			seed = seed*1664525 + 1013904223
			noise := (float64(int32(seed>>8)&0xffff) - 32768) / 32768 * 0.01
			pcm[i*channels+c] = float32(v*(1-0.2*float64(c)) + noise)
		}
	}
	return pcm
}

func TestPacketEncoderEncodeZeroAlloc(t *testing.T) {
	cases := []struct {
		name      string
		apiRate   int
		bandwidth Bandwidth
		channels  int
		frameMs   int
		useCBR    bool
	}{
		{"mono_48k_wb_20ms", 48000, BandwidthWideband, 1, 20, false},
		{"stereo_48k_wb_20ms", 48000, BandwidthWideband, 2, 20, false},
		{"stereo_48k_mb_60ms_cbr", 48000, BandwidthMediumband, 2, 60, true},
		{"mono_16k_nb_10ms", 16000, BandwidthNarrowband, 1, 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPacketEncoderAt(tc.apiRate, tc.bandwidth, tc.channels)
			p.ctl.LBRRCoded = true
			p.ctl.PacketLossPercentage = 20
			p.ctl.UseCBR = tc.useCBR
			frame := tc.apiRate * tc.frameMs / 1000
			const packets = 12
			pcm := speechLikeSignal(tc.apiRate, frame*packets, tc.channels)
			stride := frame * tc.channels
			for i := range packets - 2 {
				p.encodeInto(t, pcm[i*stride:(i+1)*stride], 1)
			}
			k := packets - 2
			allocs := testing.AllocsPerRun(20, func() {
				p.encodeInto(t, pcm[k*stride:(k+1)*stride], 1)
				k = packets - 2 + (k+1)%2
			})
			if allocs != 0 {
				t.Fatalf("Encode allocs/op = %v, want 0", allocs)
			}
		})
	}
}

// TestPacketEncoderInternalRateSwitchZeroAlloc steps the internal rate
// 16 -> 12 -> 8 -> 12 -> 16 kHz, one step per packet, and checks that the
// switches (silk_setup_resamplers carrying x_buf over, silk_setup_fs) reuse
// the buffers of earlier switches.
func TestPacketEncoderInternalRateSwitchZeroAlloc(t *testing.T) {
	const apiRate = 48000
	p := newTestPacketEncoderAt(apiRate, BandwidthWideband, 1)
	p.ctl.MinInternalSampleRate = 8000
	p.ctl.OpusCanSwitch = true
	desired := [...]int32{12000, 8000, 12000, 16000}
	frame := apiRate / 50
	pcm := speechLikeSignal(apiRate, frame*len(desired), 1)
	cycle := func() {
		for i, fs := range desired {
			p.ctl.DesiredInternalSampleRate = fs
			p.encodeInto(t, pcm[i*frame:(i+1)*frame], 1)
			if p.ctl.InternalSampleRate != fs {
				t.Fatalf("internal rate %d, want %d", p.ctl.InternalSampleRate, fs)
			}
		}
	}
	p.encodeInto(t, pcm[:frame], 1)
	for range 3 {
		cycle()
	}
	if allocs := testing.AllocsPerRun(10, cycle); allocs != 0 {
		t.Fatalf("rate switch allocs/op = %v, want 0", allocs)
	}
}

func TestPacketEncoderInitRestoresFreshState(t *testing.T) {
	const fs = 16000
	pcm := speechLikeSignal(fs, 5*fs/50, 2)
	stride := 2 * fs / 50

	fresh := newTestPacketEncoder(BandwidthWideband, 2)
	var want [][]byte
	for i := range 3 {
		want = append(want, fresh.encode(t, pcm[i*stride:(i+1)*stride]))
	}

	used := newTestPacketEncoder(BandwidthWideband, 2)
	for i := range 5 {
		used.encode(t, pcm[i*stride:(i+1)*stride])
	}
	used.enc.Init()
	for i := range 3 {
		got := used.encode(t, pcm[i*stride:(i+1)*stride])
		if !bytes.Equal(got, want[i]) {
			t.Fatalf("packet %d after Init differs from a fresh encoder:\n got %x\nwant %x", i, got, want[i])
		}
	}
}

func TestAllowBandwidthSwitchMatchesLibopusThreshold(t *testing.T) {
	s := NewPacketEncoder(1)

	s.updateAllowBandwidthSwitch(speechActivityDTXThresholdQ8-1, 20)
	if !s.allowBandwidthSwitch {
		t.Fatal("low activity should allow bandwidth switching")
	}
	if s.timeSinceSwitchAllowedMs != 0 {
		t.Fatalf("timeSinceSwitchAllowedMs=%d want reset 0", s.timeSinceSwitchAllowedMs)
	}

	s.updateAllowBandwidthSwitch(speechActivityDTXThresholdQ8, 20)
	if s.allowBandwidthSwitch {
		t.Fatal("activity at threshold should not allow bandwidth switching")
	}
	if s.timeSinceSwitchAllowedMs != 20 {
		t.Fatalf("timeSinceSwitchAllowedMs=%d want 20", s.timeSinceSwitchAllowedMs)
	}

	// The threshold rises by 3188/2^16 per ms without a switch; after 5 s it
	// passes the maximum Q8 activity.
	s.timeSinceSwitchAllowedMs = 5000
	s.updateAllowBandwidthSwitch(255, 20)
	if !s.allowBandwidthSwitch {
		t.Fatal("full delay threshold should allow even max Q8 activity")
	}
	if s.timeSinceSwitchAllowedMs != 0 {
		t.Fatalf("timeSinceSwitchAllowedMs after delayed switch=%d want 0", s.timeSinceSwitchAllowedMs)
	}
}

func TestReducedDependencyCodesEveryPacketIndependently(t *testing.T) {
	const fs = 16000
	pcm := speechLikeSignal(fs, 4*fs/50, 1)
	stride := fs / 50

	p := newTestPacketEncoder(BandwidthWideband, 1)
	p.ctl.ReducedDependency = true
	for i := range 4 {
		p.encodeInto(t, pcm[i*stride:(i+1)*stride], 1)
		if st := p.enc.state[0]; st.firstFrameAfterReset {
			t.Fatalf("packet %d: first_frame_after_reset still set after coding the frame", i)
		}
	}

	// Reduced dependency marks the first frame of every packet as the first
	// after a reset (silk/enc_API.c:172-177).
	control := newTestPacketEncoder(BandwidthWideband, 1)
	control.ctl.ReducedDependency = true
	manual := newTestPacketEncoder(BandwidthWideband, 1)
	plain := newTestPacketEncoder(BandwidthWideband, 1)
	differs := false
	for i := range 4 {
		want := append([]byte(nil), control.encodeInto(t, pcm[i*stride:(i+1)*stride], 1)...)
		manual.enc.state[0].firstFrameAfterReset = true
		got := append([]byte(nil), manual.encodeInto(t, pcm[i*stride:(i+1)*stride], 1)...)
		if !bytes.Equal(got, want) {
			t.Fatalf("packet %d: reduced dependency differs from a first-after-reset packet:\n got %x\nwant %x", i, got, want)
		}
		if !bytes.Equal(plain.encodeInto(t, pcm[i*stride:(i+1)*stride], 1), want) {
			differs = true
		}
	}
	if !differs {
		t.Fatal("reduced dependency coded the same packets as conditional coding")
	}
}

func TestPrefillResetsChannelsAndKeepsLPStateOnlyForPrefill2(t *testing.T) {
	const fs = 16000
	for _, prefill := range []int{1, 2} {
		p := newTestPacketEncoder(BandwidthWideband, 1)
		// A first packet sets the internal rate the prefill 2 keeps.
		p.encode(t, speechLikeSignal(fs, fs/50, 1))
		st := p.enc.state[0]
		st.lpState = LPState{InLPState: [2]int32{11, 22}, TransitionFrameNo: 17, Mode: -2}
		st.frameCounter = 23
		st.previousGainIndex = 33

		pcm := speechLikeSignal(fs, fs/100, 1)
		if _, err := p.enc.Encode(&p.ctl, pcm, fs/100, nil, prefill, VADNoDecision); err != nil {
			t.Fatalf("prefill %d: %v", prefill, err)
		}
		if p.ctl.PayloadSizeMs != 20 || p.ctl.Complexity != 10 {
			t.Fatalf("prefill %d changed the controls: payload=%d complexity=%d", prefill, p.ctl.PayloadSizeMs, p.ctl.Complexity)
		}
		if st.prefillFlag || st.controlledSinceLastPayload {
			t.Fatalf("prefill %d left prefillFlag=%v controlledSinceLastPayload=%v", prefill, st.prefillFlag, st.controlledSinceLastPayload)
		}
		// silk_init_encoder clears the channel; the prefill frame only advances
		// the frame counter (silk_encode_frame_FLP draws the seed first). The
		// FIXED_POINT build keeps its frame counter in the integer state.
		if st.previousGainIndex != 10 || !st.firstFrameAfterReset {
			t.Fatalf("prefill %d: previousGainIndex=%d firstFrameAfterReset=%v, want 10, true",
				prefill, st.previousGainIndex, st.firstFrameAfterReset)
		}
		if !silkFixedEncodeBuild && st.frameCounter != 1 {
			t.Fatalf("prefill %d: frameCounter=%d, want 1", prefill, st.frameCounter)
		}
		want := LPState{}
		if prefill == 2 {
			want = LPState{InLPState: [2]int32{11, 22}, TransitionFrameNo: 17, Mode: -2, SavedFsKHz: 16}
		}
		// The LP filter ran over the prefill frame; only the transition
		// bookkeeping is comparable.
		if st.lpState.Mode != want.Mode || st.lpState.SavedFsKHz != want.SavedFsKHz {
			t.Fatalf("prefill %d: lpState=%+v, want mode %d savedFs %d", prefill, st.lpState, want.Mode, want.SavedFsKHz)
		}
	}
}

func TestOpusInactivityLowersSILKActivity(t *testing.T) {
	const fs = 16000
	pcm := speechLikeSignal(fs, 3*fs/50, 1)
	stride := fs / 50

	active := newTestPacketEncoder(BandwidthWideband, 1)
	inactive := newTestPacketEncoder(BandwidthWideband, 1)
	for i := range 3 {
		a := active.encodeInto(t, pcm[i*stride:(i+1)*stride], 1)
		if !active.enc.state[0].vadFlags[0] {
			t.Fatalf("packet %d: speech-like input should be VAD active (activity %d)", i, active.enc.state[0].speechActivityQ8)
		}
		aCopy := append([]byte(nil), a...)
		b := inactive.encodeInto(t, pcm[i*stride:(i+1)*stride], VADNoActivity)
		st := inactive.enc.state[0]
		if st.vadFlags[0] || st.speechActivityQ8 >= speechActivityDTXThresholdQ8 {
			t.Fatalf("packet %d: VAD_NO_ACTIVITY left vad=%v activity=%d", i, st.vadFlags[0], st.speechActivityQ8)
		}
		if bytes.Equal(aCopy, b) {
			t.Fatalf("packet %d: Opus-level inactivity did not change the packet", i)
		}
	}
}

func TestResetAnalysisHistory(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	enc.isPreviousFrameVoiced = true
	enc.pitchState.prevLag = 222
	enc.previousGainIndex = 33
	enc.ecPrevLagIndex = 7
	enc.ecPrevSignalType = typeVoiced
	enc.firstFrameAfterReset = false
	enc.nsqState.prevGainQ16 = 54321
	enc.nsqState.lagPrev = 222
	enc.lpState.InLPState = [2]int32{11, 22}
	enc.lpState.Mode = 1
	for i := range enc.prevLSFQ15 {
		enc.prevLSFQ15[i] = int16(i + 1)
	}

	enc.resetAnalysisHistory()

	if enc.previousGainIndex != 10 {
		t.Fatalf("previousGainIndex = %d, want 10", enc.previousGainIndex)
	}
	if enc.isPreviousFrameVoiced {
		t.Fatal("prevSignalType should be TYPE_NO_VOICE_ACTIVITY")
	}
	// silk/enc_API.c:453-463 leaves the entropy coding history alone.
	if enc.ecPrevLagIndex != 7 || enc.ecPrevSignalType != typeVoiced {
		t.Fatalf("entropy coding history = (%d,%d), want (7,%d)", enc.ecPrevLagIndex, enc.ecPrevSignalType, typeVoiced)
	}
	if enc.pitchState.prevLag != 100 || enc.nsqState.lagPrev != 100 {
		t.Fatalf("prevLag=%d lagPrev=%d, want 100", enc.pitchState.prevLag, enc.nsqState.lagPrev)
	}
	if enc.nsqState.prevGainQ16 != 1<<16 {
		t.Fatalf("nsqState.prevGainQ16 = %d, want %d", enc.nsqState.prevGainQ16, 1<<16)
	}
	if enc.lpState.InLPState != ([2]int32{}) || enc.lpState.Mode != 1 {
		t.Fatalf("lpState = %+v, want cleared In_LP_State with the mode kept", enc.lpState)
	}
	for i, got := range enc.prevLSFQ15 {
		if got != 0 {
			t.Fatalf("prevLSFQ15[%d] = %d, want 0", i, got)
		}
	}
	if !enc.firstFrameAfterReset {
		t.Fatal("first_frame_after_reset should be set")
	}
}
