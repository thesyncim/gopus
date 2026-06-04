package encoder

import (
	"fmt"
	"math"
	"testing"
)

// TestLargeFrameNoPanic guards the encoder against the index-out-of-range panic
// in the SILK VAD band decimation (encoder/vad.go) that fired when a sub-48 kHz
// frame produced a SILK/transition-prefill frame shorter than VADMinFrameLength.
//
// libopus opus_encode accepts every 20/40/60/80/100/120 ms frame at all of
// 8/12/16/24/48 kHz (mono and stereo) and never returns OPUS_BAD_ARG for them.
// gopus must never panic on any of these inputs: it must return a packet or a
// clean error. The previously-reproduced crashes were:
//   - 8 kHz mono/stereo 120 ms (frameSize 960), via the SILK stereo
//     transition-prefill VAD on the CELT->SILK switch.
//   - 12 kHz mono/stereo 80 ms (frameSize 960), same path.
//   - 8 kHz mono/stereo 20 ms (frameSize 160) with SILK forced, via the main
//     SILK encode VAD on the down-resampled frame.
func TestLargeFrameNoPanic(t *testing.T) {
	rates := []int{8000, 12000, 16000, 24000, 48000}
	durations := []struct {
		name string
		ms   int
	}{
		{"20ms", 20},
		{"40ms", 40},
		{"60ms", 60},
		{"80ms", 80},
		{"100ms", 100},
		{"120ms", 120},
	}
	channelsList := []int{1, 2}
	modes := []struct {
		name string
		m    Mode
	}{
		{"auto", ModeAuto},
		{"silk", ModeSILK},
	}

	for _, fs := range rates {
		for _, ch := range channelsList {
			for _, md := range modes {
				for _, d := range durations {
					name := fmt.Sprintf("%s_%dHz_%dch_%s", md.name, fs, ch, d.name)
					t.Run(name, func(t *testing.T) {
						frameSize := fs * d.ms / 1000
						n := frameSize * ch
						silkPCM := make([]float32, n)
						for i := range silkPCM {
							silkPCM[i] = float32(0.2 * math.Sin(float64(i)*0.02))
						}

						// Direct encode.
						runLargeFrameEncode(t, fs, ch, md.m, frameSize, silkPCM)

						// CELT->SILK/Hybrid transition: encode a forced-CELT frame
						// first so the next frame runs the transition prefill that
						// drove the original stereo-prefill VAD panic.
						encT := NewEncoder(fs, ch)
						encT.SetComplexity(10)
						encT.SetVoIPApplication(true)
						if ch == 2 {
							encT.SetForceChannels(2)
						}
						encT.SetMode(ModeCELT)
						celtPCM := make([]float32, n)
						for i := range celtPCM {
							celtPCM[i] = float32(0.5 * math.Sin(float64(i)*0.9))
						}
						mustNotPanic(t, "celt-prime", fs, ch, md.m, frameSize, func() {
							_, _ = encT.Encode(celtPCM, frameSize)
						})
						encT.SetMode(md.m)
						mustNotPanic(t, "post-transition", fs, ch, md.m, frameSize, func() {
							_, _ = encT.Encode(silkPCM, frameSize)
						})
					})
				}
			}
		}
	}
}

func runLargeFrameEncode(t *testing.T, fs, ch int, mode Mode, frameSize int, pcm []float32) {
	t.Helper()
	enc := NewEncoder(fs, ch)
	enc.SetComplexity(10)
	enc.SetVoIPApplication(true)
	enc.SetMode(mode)
	if ch == 2 {
		enc.SetForceChannels(2)
	}
	mustNotPanic(t, "encode", fs, ch, mode, frameSize, func() {
		_, _ = enc.Encode(pcm, frameSize)
	})
}

func mustNotPanic(t *testing.T, stage string, fs, ch int, mode Mode, frameSize int, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC [%s] mode=%v fs=%d ch=%d frameSize=%d: %v", stage, mode, fs, ch, frameSize, r)
		}
	}()
	fn()
}
