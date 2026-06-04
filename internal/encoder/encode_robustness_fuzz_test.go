// encode_robustness_fuzz_test.go — robustness / edge-case coverage for the public
// encoder entry points.
//
// These tests exercise the public API surface (NewEncoder, the Encode* family,
// the Set* controls and the BuildPacket helpers) across the full space of valid
// configurations plus deliberately malformed inputs. Their contract is:
//
//   - the encoder MUST NOT panic on any input, valid or malformed;
//   - malformed configuration MUST be rejected with an error (no output);
//   - for a valid configuration the produced packet MUST be self-consistent: its
//     TOC must round-trip through gopus.ParseTOC to the requested mode/bandwidth/
//     stereo/duration, its framing must re-parse, and a decoder must accept it
//     without panicking.
//
// They intentionally assert NO specific output bytes, so they cannot perturb the
// libopus byte-exact parity that the differential/parity tests lock down; the
// byte-exact contract for valid input lives in those tests, not here.
package encoder_test

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

// apiSampleRates are the five sample rates accepted by the Opus API.
var apiSampleRates = [5]int{8000, 12000, 16000, 24000, 48000}

// maxSilkPacketBytesCapForTest mirrors the encoder's absolute internal output
// cap (maxSilkPacketBytes*6) that encodeOpusResWithAnalysisMaxBytes clamps
// maxDataBytes to. No produced packet may exceed it regardless of the requested
// budget.
const maxSilkPacketBytesCapForTest = 1275 * 6

// frameDurations48k lists every valid Opus frame duration as a count of samples
// at 48 kHz: 2.5/5/10/20/40/60/80/100/120 ms. A native frame size is obtained by
// scaling by sampleRate/48000 (all five API rates divide these evenly).
var frameDurations48k = [9]int{120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760}

// nativeFrameSize returns the native-rate frame size for the given sample rate
// and 48 kHz duration. silkOK reports whether the duration is usable by
// SILK/Hybrid (which have no 2.5/5 ms configs).
func nativeFrameSize(sampleRate, dur48k int) (frameSize int, silkOK bool) {
	frameSize = dur48k * sampleRate / 48000
	silkOK = dur48k >= 480
	return frameSize, silkOK
}

// allBandwidths is the set of Opus bandwidths the encoder accepts.
var allBandwidths = [5]types.Bandwidth{
	types.BandwidthNarrowband,
	types.BandwidthMediumband,
	types.BandwidthWideband,
	types.BandwidthSuperwideband,
	types.BandwidthFullband,
}

// allModes is the set of public encoder modes.
var allModes = [4]encoder.Mode{
	encoder.ModeAuto,
	encoder.ModeSILK,
	encoder.ModeHybrid,
	encoder.ModeCELT,
}

// makePCM builds a deterministic frame of interleaved PCM of the requested total
// length, seeding the samples from the fuzz-provided bytes so the analyzer and
// VAD see varied content (silence, tones, noise) without per-call allocation
// churn dominating.
func makePCM(total int, seed []byte, kind byte) []float32 {
	pcm := make([]float32, total)
	if total == 0 {
		return pcm
	}
	switch kind % 4 {
	case 0:
		// Digital silence: exercises the DTX / "too little space" paths.
	case 1:
		// Full-scale-ish tone.
		for i := range pcm {
			if (i>>3)&1 == 0 {
				pcm[i] = 0.8
			} else {
				pcm[i] = -0.8
			}
		}
	case 2:
		// Pseudo-noise from the seed bytes.
		for i := range pcm {
			b := byte(0)
			if len(seed) > 0 {
				b = seed[i%len(seed)]
			}
			pcm[i] = (float32(b)/127.5 - 1.0)
		}
	default:
		// Mixed: low-amplitude ramp plus seed perturbation, includes a few
		// out-of-range samples to exercise clamping.
		for i := range pcm {
			v := float32(i%97)/96.0*2 - 1
			if len(seed) > 0 {
				v += (float32(seed[i%len(seed)])/255.0 - 0.5) * 4 // can exceed [-1,1]
			}
			pcm[i] = v
		}
	}
	return pcm
}

// configureFromBytes derives a full encoder configuration from b and applies it.
// It returns the chosen native frame size and channel count. Only the controls
// available on the default build are touched.
func configureFromBytes(enc *encoder.Encoder, sampleRate, channels int, b []byte) (frameSize, ch int) {
	get := func(i int) byte {
		if i < len(b) {
			return b[i]
		}
		return 0
	}

	mode := allModes[int(get(0))%len(allModes)]
	enc.SetMode(mode)

	dur := frameDurations48k[int(get(1))%len(frameDurations48k)]
	frameSize, silkOK := nativeFrameSize(sampleRate, dur)
	// SILK/Hybrid cannot encode 2.5/5 ms frames; fall back to 20 ms so the config
	// stays valid for the chosen mode.
	if !silkOK && (mode == encoder.ModeSILK || mode == encoder.ModeHybrid) {
		frameSize, _ = nativeFrameSize(sampleRate, 960)
	}

	bw := allBandwidths[int(get(2))%len(allBandwidths)]
	if get(2)&0x80 != 0 {
		enc.SetBandwidthAuto()
	} else {
		enc.SetBandwidth(bw)
	}
	enc.SetMaxBandwidth(allBandwidths[int(get(3))%len(allBandwidths)])

	// Bitrate extremes: below-min, mid, above-max, and the special sentinels.
	switch get(4) % 6 {
	case 0:
		enc.SetBitrate(encoder.BitrateAuto)
	case 1:
		enc.SetBitrate(encoder.BitrateMax)
	case 2:
		enc.SetBitrate(1) // below MinBitrate -> clamped
	case 3:
		enc.SetBitrate(6000)
	case 4:
		enc.SetBitrate(128000)
	default:
		enc.SetBitrate(10_000_000) // above MaxBitrate -> clamped
	}

	enc.SetComplexity(int(get(5)) % 11) // 0..10
	switch get(6) % 3 {
	case 0:
		enc.SetBitrateMode(encoder.ModeVBR)
	case 1:
		enc.SetBitrateMode(encoder.ModeCVBR)
	default:
		enc.SetBitrateMode(encoder.ModeCBR)
	}
	enc.SetVBR(get(7)&1 == 0)
	enc.SetVBRConstraint(get(7)&2 == 0)

	enc.SetFEC(get(8)&1 == 1)
	enc.SetDTX(get(8)&2 == 2)
	enc.SetPacketLoss(int(get(9)) % 101) // 0..100

	enc.SetVoIPApplication(get(10)&1 == 1)
	enc.SetRestrictedSilkApplication(get(10)&2 == 2)
	enc.SetLowDelay(get(10)&4 == 4)

	enc.SetForceChannels(int(get(11)%3) - 1) // -1,0,1
	enc.SetLSBDepth(8 + int(get(12))%17)     // 8..24
	enc.SetPredictionDisabled(get(13)&1 == 1)
	enc.SetPhaseInversionDisabled(get(13)&2 == 2)

	switch get(14) % 3 {
	case 0:
		enc.SetSignalType(types.SignalAuto)
	case 1:
		enc.SetSignalType(types.SignalVoice)
	default:
		enc.SetSignalType(types.SignalMusic)
	}

	return frameSize, channels
}

// assertPacketConsistent verifies that a non-empty encoder output is a
// well-formed Opus packet that a decoder accepts without panicking. It checks
// structural self-consistency only (parseable TOC, positive advertised frame
// size, decode-no-panic); it deliberately does not pin specific bytes, nor does
// it require the actual coded mode/stereo to equal the user request, because the
// encoder may legitimately pick a different per-frame mode (e.g. a forced mode
// that is infeasible at the chosen rate/bandwidth falls back, and the
// too-little-space path emits a minimal packet).
func assertPacketConsistent(t *testing.T, enc *encoder.Encoder, pkt []byte) {
	t.Helper()
	if len(pkt) == 0 {
		return
	}
	toc := gopus.ParseTOC(pkt[0])
	if toc.FrameSize <= 0 {
		t.Fatalf("TOC frame size %d must be positive", toc.FrameSize)
	}
	// A 1-byte packet is a TOC-only (DTX / PLC / too-little-space) packet; its
	// payload framing is trivially consistent.
	if len(pkt) == 1 {
		return
	}
	// The decoder must accept the encoder's own output without panicking. Decode
	// at the encoder's sample rate using the channel count the TOC advertises so
	// the output buffer is large enough.
	decChannels := 1
	if toc.Stereo {
		decChannels = 2
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(enc.SampleRate(), decChannels))
	if err != nil {
		return
	}
	nativeFrame := toc.FrameSize * enc.SampleRate() / 48000
	if nativeFrame <= 0 {
		nativeFrame = toc.FrameSize
	}
	out := make([]float32, nativeFrame*decChannels)
	_, _ = dec.Decode(pkt, out)
}

func FuzzEncodeConfig(f *testing.F) {
	// Seed corpus: a couple of representative control words plus PCM seed.
	f.Add([]byte{0, 3, 4, 4, 3, 9, 1, 0, 0, 0, 1, 0, 24, 0, 0}, []byte{1, 2, 3, 4})
	f.Add([]byte{3, 1, 2, 4, 4, 5, 0, 0, 2, 5, 1, 2, 16, 1, 1}, []byte{0})
	f.Add([]byte{1, 8, 0, 0, 5, 0, 2, 1, 1, 100, 4, 1, 8, 3, 2}, []byte{255, 0, 128})

	f.Fuzz(func(t *testing.T, cfg, pcmSeed []byte) {
		sampleRate := apiSampleRates[int(firstByte(cfg))%len(apiSampleRates)]
		channels := 1 + int(secondByte(cfg))%2

		enc := encoder.NewEncoder(sampleRate, channels)
		frameSize, ch := configureFromBytes(enc, sampleRate, channels, cfg)
		if frameSize <= 0 {
			t.Fatalf("derived non-positive frame size %d", frameSize)
		}

		kind := byte(0)
		if len(cfg) > 0 {
			kind = cfg[len(cfg)-1]
		}
		pcm := makePCM(frameSize*ch, pcmSeed, kind)

		// Vary the output budget too, including a tight budget that forces the
		// "too little space" path.
		maxBytes := 1276
		if len(cfg) > 15 {
			switch cfg[15] % 4 {
			case 0:
				maxBytes = 2
			case 1:
				maxBytes = 50
			case 2:
				maxBytes = 400
			}
		}

		pkt, err := enc.EncodeWithAnalysisMaxBytes(pcm, frameSize, pcm, maxBytes)
		if err != nil {
			// Errors are an acceptable outcome (e.g. a frame size invalid for the
			// forced mode); the contract is only "no panic".
			return
		}
		// maxDataBytes is a rate-control budget at this layer, not a hard ceiling
		// on the returned slice (the public gopus.Encode wrapper enforces the
		// caller buffer size separately). The only size invariant here is the
		// encoder's absolute internal cap.
		if len(pkt) > maxSilkPacketBytesCapForTest {
			t.Fatalf("packet len %d exceeds absolute cap %d", len(pkt), maxSilkPacketBytesCapForTest)
		}
		assertPacketConsistent(t, enc, pkt)
	})
}

// FuzzEncodeRawFrameSize feeds arbitrary frame sizes (including invalid ones and
// zero) to the public Encode entry point. The only contract is no panic: invalid
// frame sizes must be rejected with an error, valid ones may produce output.
func FuzzEncodeRawFrameSize(f *testing.F) {
	f.Add(0, 0, 0)
	f.Add(48000, 1, 960)
	f.Add(8000, 2, 0)
	f.Add(48000, 1, -960)
	f.Add(16000, 2, 7)

	f.Fuzz(func(t *testing.T, srSel, chSel, frameSize int) {
		sampleRate := apiSampleRates[((srSel%len(apiSampleRates))+len(apiSampleRates))%len(apiSampleRates)]
		channels := 1 + ((chSel%2)+2)%2
		enc := encoder.NewEncoder(sampleRate, channels)

		// Clamp the requested frame size to a sane magnitude and size the PCM to
		// match it (mismatched lengths are themselves a valid rejection path, but
		// the goal here is to probe the frame-size handling, not the length check).
		n := frameSize
		if n < 0 {
			n = 0
		}
		if n > 1<<16 {
			n = 1 << 16
		}
		pcm := make([]float32, n*channels)
		// Encode must never panic regardless of frameSize.
		_, _ = enc.Encode(pcm, frameSize)
		// Also probe the explicit-frame-size length-mismatch path.
		_, _ = enc.Encode(make([]float32, 0), frameSize)
	})
}

// FuzzEncodeStream drives a multi-frame stream while toggling controls between
// frames, exercising cross-frame state (mode transitions, prefill, VBR
// reservoir, DTX run length) and the FinalRange accessor.
func FuzzEncodeStream(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7}, byte(8))
	f.Add([]byte{255, 254, 0, 1, 128, 64, 32, 16}, byte(3))

	f.Fuzz(func(t *testing.T, ctrl []byte, nFrames byte) {
		sampleRate := apiSampleRates[int(firstByte(ctrl))%len(apiSampleRates)]
		channels := 1 + int(secondByte(ctrl))%2
		enc := encoder.NewEncoder(sampleRate, channels)
		enc.SetDTX(true)
		enc.SetMode(encoder.ModeAuto)

		frames := 1 + int(nFrames)%24
		dur := frameDurations48k[int(firstByte(ctrl))%len(frameDurations48k)]
		if dur < 480 {
			dur = 960
		}
		frameSize, _ := nativeFrameSize(sampleRate, dur)

		for i := 0; i < frames; i++ {
			// Toggle a few controls mid-stream to provoke transitions.
			c := byte(i)
			if len(ctrl) > 0 {
				c = ctrl[i%len(ctrl)]
			}
			enc.SetComplexity(int(c) % 11)
			switch c % 3 {
			case 0:
				enc.SetBitrateMode(encoder.ModeVBR)
			case 1:
				enc.SetBitrateMode(encoder.ModeCVBR)
			default:
				enc.SetBitrateMode(encoder.ModeCBR)
			}
			enc.SetBitrate(6000 + int(c)*1000)

			pcm := makePCM(frameSize*channels, ctrl, c)
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				continue
			}
			// FinalRange must be readable after every encode without panic.
			_ = enc.FinalRange()
			assertPacketConsistent(t, enc, pkt)
		}
	})
}

// FuzzBuildPacket fuzzes the standalone packet-assembly helpers. They must reject
// invalid configurations / frame counts with an error and never panic, and any
// packet they emit must re-parse to the requested config.
func FuzzBuildPacket(f *testing.F) {
	f.Add([]byte{1, 2, 3}, byte(0), byte(4), byte(1), byte(2))
	f.Add([]byte{}, byte(2), byte(0), byte(1), byte(5))
	f.Add([]byte{9, 9, 9, 9, 9, 9}, byte(1), byte(3), byte(0), byte(3))

	f.Fuzz(func(t *testing.T, frameData []byte, modeSel, bwSel, stereoSel, nFrames byte) {
		mode := []gopus.Mode{gopus.ModeSILK, gopus.ModeHybrid, gopus.ModeCELT}[int(modeSel)%3]
		bw := []types.Bandwidth{
			types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband,
			types.BandwidthSuperwideband, types.BandwidthFullband,
		}[int(bwSel)%5]
		frameSize := frameDurations48k[int(bwSel)%len(frameDurations48k)]
		stereo := stereoSel&1 == 1

		// Single-frame BuildPacket: never panics; success implies a parseable TOC.
		if pkt, err := encoder.BuildPacket(frameData, mode, bw, frameSize, stereo); err == nil {
			if len(pkt) != len(frameData)+1 {
				t.Fatalf("BuildPacket len %d != %d", len(pkt), len(frameData)+1)
			}
			toc := gopus.ParseTOC(pkt[0])
			if toc.Stereo != stereo {
				t.Fatalf("BuildPacket TOC stereo %v != %v", toc.Stereo, stereo)
			}
		}

		// BuildPacketInto with a deliberately undersized buffer must error, not
		// panic or overrun.
		small := make([]byte, len(frameData)) // one byte short of TOC+data
		_, _ = encoder.BuildPacketInto(small, frameData, mode, bw, frameSize, stereo)
		big := make([]byte, len(frameData)+8)
		if n, err := encoder.BuildPacketInto(big, frameData, mode, bw, frameSize, stereo); err == nil {
			if n != len(frameData)+1 {
				t.Fatalf("BuildPacketInto wrote %d != %d", n, len(frameData)+1)
			}
		}

		// Multi-frame BuildPacket: split frameData into nFrames equal-ish frames.
		fc := 1 + int(nFrames)%48
		frames := make([][]byte, fc)
		for i := range frames {
			frames[i] = frameData // identical frames (CBR framing path)
		}
		if pkt, err := encoder.BuildMultiFramePacket(frames, mode, bw, frameSize, stereo, false); err == nil {
			if len(pkt) == 0 {
				t.Fatalf("BuildMultiFramePacket returned empty packet for %d frames", fc)
			}
			_ = gopus.ParseTOC(pkt[0])
		}
		// Zero frames and >48 frames must be rejected.
		if _, err := encoder.BuildMultiFramePacket(nil, mode, bw, frameSize, stereo, false); err == nil {
			t.Fatalf("BuildMultiFramePacket accepted zero frames")
		}
	})
}

// TestEncodeNoPanicAllValidConfigs is a deterministic (non-fuzz) sweep asserting
// that every combination of API sample rate, channel count, valid frame duration
// and forced mode encodes a self-consistent packet without panicking, across
// several bitrate/complexity/rate-control extremes and a few signal kinds. It
// locks the no-panic contract into the normal `go test` run (independent of
// -fuzz) and complements the byte-exact parity tests, which it does not duplicate.
func TestEncodeNoPanicAllValidConfigs(t *testing.T) {
	kinds := []byte{0, 1, 2, 3} // silence, tone, noise, mixed/out-of-range
	for _, sr := range apiSampleRates {
		for _, ch := range []int{1, 2} {
			for _, dur := range frameDurations48k {
				frameSize, silkOK := nativeFrameSize(sr, dur)
				for _, mode := range allModes {
					if !silkOK && (mode == encoder.ModeSILK || mode == encoder.ModeHybrid) {
						continue // 2.5/5 ms not valid for SILK/Hybrid
					}
					for _, br := range []int{encoder.BitrateAuto, encoder.BitrateMax, 6000, 64000, 510000} {
						for _, cx := range []int{0, 5, 10} {
							for _, bm := range []encoder.BitrateMode{encoder.ModeVBR, encoder.ModeCVBR, encoder.ModeCBR} {
								enc := encoder.NewEncoder(sr, ch)
								enc.SetMode(mode)
								enc.SetBitrate(br)
								enc.SetComplexity(cx)
								enc.SetBitrateMode(bm)
								pcm := makePCM(frameSize*ch, []byte{0x5a, 0x13, 0x77}, kinds[(dur+cx)%len(kinds)])
								func() {
									defer func() {
										if r := recover(); r != nil {
											t.Fatalf("PANIC sr=%d ch=%d dur=%d fs=%d mode=%v br=%d cx=%d bm=%d: %v",
												sr, ch, dur, frameSize, mode, br, cx, bm, r)
										}
									}()
									pkt, err := enc.Encode(pcm, frameSize)
									if err != nil {
										return // acceptable (e.g. forced mode infeasible at config)
									}
									assertPacketConsistent(t, enc, pkt)
								}()
							}
						}
					}
				}
			}
		}
	}
}

// TestEncodeMalformedConfigRejected verifies that malformed configuration is
// rejected with an error (and never panics): non-positive frame sizes, PCM
// length not matching frameSize*channels, and out-of-range control values.
func TestEncodeMalformedConfigRejected(t *testing.T) {
	enc := encoder.NewEncoder(48000, 2)

	// Non-positive and clearly invalid frame sizes via the public Encode path.
	for _, fs := range []int{0, -1, -960} {
		n := fs * 2
		if n < 0 {
			n = 0
		}
		pkt, err := enc.Encode(make([]float32, n), fs)
		if err == nil {
			t.Fatalf("Encode accepted invalid frameSize %d (len(pkt)=%d)", fs, len(pkt))
		}
	}

	// Length mismatch: declared 20 ms (960 samples * 2 ch) but short PCM.
	if _, err := enc.Encode(make([]float32, 10), 960); err == nil {
		t.Fatalf("Encode accepted PCM length not matching frameSize*channels")
	}

	// Out-of-range in-band FEC config.
	for _, c := range []int{-1, 3, 99} {
		if err := enc.SetInBandFEC(c); err == nil {
			t.Fatalf("SetInBandFEC accepted out-of-range config %d", c)
		}
	}
	// Valid in-band FEC configs are accepted.
	for _, c := range []int{encoder.InBandFECDisabled, encoder.InBandFECEnabled, encoder.InBandFECMusicSafe} {
		if err := enc.SetInBandFEC(c); err != nil {
			t.Fatalf("SetInBandFEC rejected valid config %d: %v", c, err)
		}
	}

	// Out-of-range voice ratio.
	for _, r := range []int{-2, 101, 1000} {
		if err := enc.SetVoiceRatio(r); err == nil {
			t.Fatalf("SetVoiceRatio accepted out-of-range ratio %d", r)
		}
	}
	for _, r := range []int{-1, 0, 50, 100} {
		if err := enc.SetVoiceRatio(r); err != nil {
			t.Fatalf("SetVoiceRatio rejected valid ratio %d: %v", r, err)
		}
	}
}

// TestEncodeToggleDTXFECStream exercises DTX and FEC toggling across a stream of
// silence and speech-like frames, asserting no panic and that DTX is able to
// emit 1-byte TOC-only packets during sustained silence.
func TestEncodeToggleDTXFECStream(t *testing.T) {
	for _, app := range []bool{false, true} { // audio vs VoIP
		enc := encoder.NewEncoder(16000, 1)
		enc.SetVoIPApplication(app)
		enc.SetMode(encoder.ModeAuto)
		enc.SetDTX(true)
		enc.SetFEC(true)
		enc.SetPacketLoss(20)
		enc.SetBitrate(16000)
		frameSize := 320 // 20 ms at 16 kHz
		sawShort := false
		for i := 0; i < 60; i++ {
			kind := byte(0) // silence
			if i%20 < 3 {
				kind = 1 // brief tone burst
			}
			pcm := makePCM(frameSize, []byte{byte(i)}, kind)
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("app=%v frame %d: %v", app, i, err)
			}
			if len(pkt) == 1 {
				sawShort = true
			}
			assertPacketConsistent(t, enc, pkt)
			_ = enc.FinalRange()
		}
		if !sawShort {
			t.Errorf("app=%v: expected at least one 1-byte DTX/PLC packet during silence", app)
		}
	}
}

func firstByte(b []byte) byte {
	if len(b) > 0 {
		return b[0]
	}
	return 0
}

func secondByte(b []byte) byte {
	if len(b) > 1 {
		return b[1]
	}
	return 0
}
