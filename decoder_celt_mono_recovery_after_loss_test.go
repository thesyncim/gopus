//go:build !gopus_fixed_point

package gopus

import (
	"fmt"
	"math"
	"testing"
)

// TestCELTMonoRecoveryAfterLongGapMatchesLibopus is the focused regression for a
// mono CELT loss-recovery bug: libopus keeps the right-channel energy slot as a
// per-frame shadow copy of the left channel
// (`if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)`), and the
// loss-recovery prediction folds it back in
// (`oldBandE[i] = MAXG(oldBandE[i], oldBandE[nbEBands+i])`). Over a long
// concealment gap the noise PLC decays only the left channel; the recovery frame
// then folds the undecayed right shadow back into the coarse-energy prediction
// base. gopus mono decoders previously had no right-channel slot, so the recovery
// frame's energy prediction diverged (corr collapsed from 1.0 to ~0.1, peak PCM
// error ~0.3) even though the range coder stayed in lock-step.
//
// The decode plan replays the libopus opus_demo loss-recovery model against the
// stateful single-decoder oracle (libopus_refdecode_single.c). Short bursts stay
// in periodic PLC (which does not touch oldBandE) and were already bit-exact;
// long gaps cross into noise PLC and are the regressors. Both the per-step final
// range (integer entropy state) and the decoded PCM are asserted.
func TestCELTMonoRecoveryAfterLongGapMatchesLibopus(t *testing.T) {
	requireLibopusAPIRateRefdecodeHelper(t)

	type spec struct {
		name      string
		bw        Bandwidth
		channels  int
		bitrate   int
		frameMs   ExpertFrameDuration
		frameSamp int
		lossStart int
		lossEnd   int // exclusive
		nFrames   int
	}
	specs := []spec{
		{"swb_mono_20ms_longgap", BandwidthSuperwideband, 1, 64000, ExpertFrameDuration20Ms, 960, 8, 20, 26},
		{"swb_mono_10ms_longgap", BandwidthSuperwideband, 1, 64000, ExpertFrameDuration10Ms, 480, 8, 24, 30},
		{"fb_mono_20ms_longgap", BandwidthFullband, 1, 96000, ExpertFrameDuration20Ms, 960, 8, 22, 28},
		{"wb_mono_20ms_longgap", BandwidthWideband, 1, 48000, ExpertFrameDuration20Ms, 960, 8, 20, 26},
		// Burst (stays in periodic PLC) — already bit-exact, guards against
		// regressing the periodic path while fixing the noise path.
		{"swb_mono_20ms_burst", BandwidthSuperwideband, 1, 64000, ExpertFrameDuration20Ms, 960, 10, 15, 24},
		// Stereo long-gap — unaffected by the mono shadow, must stay bit-exact.
		{"swb_stereo_20ms_longgap", BandwidthSuperwideband, 2, 128000, ExpertFrameDuration20Ms, 960, 8, 20, 26},
	}

	const sampleRate = 48000
	for _, sp := range specs {
		t.Run(sp.name, func(t *testing.T) {
			packets := encodeCELTMonoRecoveryStream(t, sp.bw, sp.channels, sp.bitrate, sp.frameMs, sp.frameSamp, sp.nFrames)

			mask := make([]bool, sp.nFrames)
			for i := sp.lossStart; i < sp.lossEnd; i++ {
				mask[i] = true
			}

			steps, recoveryStepIdx := buildCELTLossPlan(packets, mask)
			if recoveryStepIdx < 0 {
				t.Fatalf("%s: no recovery step in plan", sp.name)
			}

			want, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, sp.channels, sp.frameSamp, steps)
			if err != nil {
				t.Skipf("%s: oracle unavailable: %v", sp.name, err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, sp.channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, sp.frameSamp*sp.channels)

			off := 0
			for i, s := range steps {
				n, e := dec.Decode(s.packet, buf)
				if e != nil {
					t.Fatalf("%s: step %d decode: %v", sp.name, i, e)
				}
				got := buf[:n*sp.channels]
				wantSlice := want[off : off+n*sp.channels]
				off += n * sp.channels

				if i < len(wantRanges) {
					if gr := dec.FinalRange(); gr != wantRanges[i] {
						t.Fatalf("%s: step %d final range gopus=%d libopus=%d (entropy desync)",
							sp.name, i, gr, wantRanges[i])
					}
				}

				maxAbs, idx := maxAbsDiff(got, wantSlice)
				// Float-domain tolerance. The pre-fix bug produced peak errors of
				// ~0.3 on the recovery frame; the algorithm-exact path lands at the
				// ~1e-6 float-noise level (single-tree float accumulation order).
				const tol = 5e-4
				if maxAbs > tol {
					kind := "normal"
					if s.packet == nil {
						kind = "PLC"
					} else if i == recoveryStepIdx {
						kind = "RECOVERY"
					}
					t.Fatalf("%s: step %d (%s) PCM diverges: maxAbs=%.6e at sample %d (tol=%.1e)",
						sp.name, i, kind, maxAbs, idx, tol)
				}
			}
		})
	}
}

// encodeCELTMonoRecoveryStream encodes a continuous voiced tone as a stateful
// CELT packet stream so the decoder builds real pitch/energy state across frames.
func encodeCELTMonoRecoveryStream(t *testing.T, bw Bandwidth, channels, bitrate int, frameMs ExpertFrameDuration, frameSamp, nFrames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Skipf("SetMode CELT: %v", err)
	}
	if err := enc.SetFrameSize(frameSamp); err != nil {
		t.Skipf("SetFrameSize(%d): %v", frameSamp, err)
	}
	if err := enc.SetExpertFrameDuration(frameMs); err != nil {
		t.Skipf("SetExpertFrameDuration: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Skipf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Skipf("SetBitrate: %v", err)
	}
	_ = enc.SetSignal(SignalMusic)
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Skipf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, nFrames)
	for f := range nFrames {
		pcm := celtRecoveryTonePCM(frameSamp, channels, sampleRate, f)
		pkt, e := encodeOneFrame(enc, pcm)
		if e != nil {
			t.Skipf("encode frame %d: %v", f, e)
		}
		if len(pkt) == 0 {
			t.Skipf("encoder produced empty packet at frame %d", f)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

func celtRecoveryTonePCM(frameSamples, channels, sampleRate, frameOffset int) []float32 {
	pcm := make([]float32, frameSamples*channels)
	base := 220.0
	for i := range frameSamples {
		gi := frameOffset*frameSamples + i
		tm := float64(gi) / float64(sampleRate)
		f0 := base * (1.0 + 0.02*math.Sin(2*math.Pi*3.0*tm))
		for c := range channels {
			ph := float64(c) * 0.13
			v := 0.42*math.Sin(2*math.Pi*f0*tm+ph) +
				0.18*math.Sin(2*math.Pi*2*f0*tm+0.21+ph) +
				0.08*math.Sin(2*math.Pi*3*f0*tm+0.43+ph)
			pcm[i*channels+c] = float32(v)
		}
	}
	return pcm
}

// buildCELTLossPlan replays opus_demo.c's loss handling for a CELT stream (no
// LBRR, so every lost frame is a PLC step): after a non-lost packet that follows
// lostCount losses, run 1+lostCount decode calls (lostCount PLC then 1 normal).
// Returns the flat step list and the index of the first post-gap recovery step.
func buildCELTLossPlan(packets [][]byte, mask []bool) ([]libopusAPIRateDecodeStep, int) {
	var steps []libopusAPIRateDecodeStep
	lost := 0
	recoveryStepIdx := -1
	for i, pkt := range packets {
		if mask[i] {
			lost++
			continue
		}
		runDecoder := 1 + lost
		for fr := range runDecoder {
			if fr < lost {
				steps = append(steps, libopusAPIRateDecodeStep{packet: nil})
			} else {
				if lost > 0 && recoveryStepIdx < 0 {
					recoveryStepIdx = len(steps)
				}
				steps = append(steps, libopusAPIRateDecodeStep{packet: pkt})
			}
		}
		lost = 0
	}
	return steps, recoveryStepIdx
}

func maxAbsDiff(got, want []float32) (float64, int) {
	n := min(len(want), len(got))
	maxAbs := 0.0
	idx := -1
	for i := 0; i < n; i++ {
		d := math.Abs(float64(got[i]) - float64(want[i]))
		if d > maxAbs {
			maxAbs = d
			idx = i
		}
	}
	return maxAbs, idx
}

var _ = fmt.Sprint
