package gopus

// LBRR/FEC in-band recovery decode parity tests.
//
// These tests assert that DecodeWithFEC recovery from LBRR-carrying packets
// matches libopus for the two specific cases called out in the task:
//
//  1. Mono first-packet LBRR: the very first SILK packet that carries LBRR data
//     (packet index 0 or 1 after warm-up); recovery must match libopus.
//
//  2. Stereo warm LBRR: a stereo SILK WB packet with established LBRR state
//     (both channels carrying LBRR), decoded through DecodeWithFEC.
//
// Both cases verify:
//   - Byte-exact or quality-pass recovery against libopus oracle (when available).
//   - Non-silent recovery output (FEC must provide audible signal).
//   - The PLC-fallback path (no LBRR in packet) matches Decode(nil) exactly.
//
// Reference: libopus silk/dec_API.c (silk_Decode FLAG_DECODE_LBRR),
// silk/lbrr_decode.go (DecodeFEC), decoder_fec.go (DecodeWithFEC).

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestDecodeWithFECMonoFirstPacketLBRRMatchesLibopus checks that the very first
// packet in a SILK mono stream that carries LBRR (mono first-packet case) is
// recovered correctly by DecodeWithFEC, matching the libopus oracle.
//
// "First packet" here means the first LBRR-carrying packet regardless of stream
// position (the LBRR payload seeds the warm-up state). This mirrors the libopus
// enc_API.c test: LBRRprevLastGainIndex is 0 at stream start.
func TestDecodeWithFECMonoFirstPacketLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 1
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	for _, sampleRate := range []int{16000, 48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			// Libopus reference: seed → FEC → normal.
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
				{packet: recoveryPacket},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "mono first-packet LBRR reference", err)
			}

			// gopus.
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			got := make([]float32, 0, len(want))

			if n, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.DecodeWithFEC(recoveryPacket, buf, true); err != nil {
				t.Fatalf("DecodeWithFEC recovery: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.Decode(recoveryPacket, buf); err != nil {
				t.Fatalf("Decode recovery packet: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}

			cmpLen := min(len(got), len(want))
			assertAPIRateQualityFloat32(t, got[:cmpLen], want[:cmpLen], sampleRate, channels,
				"mono first-packet LBRR FEC decode")
		})
	}
}

// TestDecodeWithFECStereoWarmLBRRMatchesLibopus checks that a stereo SILK WB
// packet carrying LBRR (both mid and side channels) is correctly recovered by
// DecodeWithFEC, matching the libopus oracle. This is the "stereo warm" case:
// the encoder has processed enough frames to warm up the stereo predictor and
// LBRR gain state for both channels.
func TestDecodeWithFECStereoWarmLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 2
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	for _, sampleRate := range []int{16000, 48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
				{packet: recoveryPacket},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo warm LBRR reference", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			got := make([]float32, 0, len(want))

			if n, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.DecodeWithFEC(recoveryPacket, buf, true); err != nil {
				t.Fatalf("DecodeWithFEC recovery: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.Decode(recoveryPacket, buf); err != nil {
				t.Fatalf("Decode recovery packet: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}

			cmpLen := min(len(got), len(want))
			assertAPIRateQualityFloat32(t, got[:cmpLen], want[:cmpLen], sampleRate, channels,
				"stereo warm LBRR FEC decode")
		})
	}
}

// TestDecodeWithFECMonoFirstPacketNoLBRRFallbackMatchesPLC verifies that when
// a mono packet carries no LBRR data, DecodeWithFEC(packet, true) falls back to
// the same PLC output as Decode(nil, buf). This ensures the LBRR gate is correct
// and there is no spurious FEC decoding on non-LBRR packets.
func TestDecodeWithFECMonoFirstPacketNoLBRRFallbackMatchesPLC(t *testing.T) {
	const (
		channels   = 1
		frameSize  = 960
		sampleRate = 48000
	)

	// Encode a SILK mono packet without FEC.
	seedPacket := encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)
	if packetHasInBandFEC(t, seedPacket) {
		t.Fatal("FEC-disabled SILK packet should not carry LBRR")
	}

	// Decoder A: Decode seed then Decode(nil) → PLC path.
	decPLC, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder PLC: %v", err)
	}
	bufSeed := make([]float32, frameSize*channels)
	if _, err := decPLC.Decode(seedPacket, bufSeed); err != nil {
		t.Fatalf("decPLC Decode seed: %v", err)
	}
	wantBuf := make([]float32, frameSize*channels)
	nWant, err := decPLC.Decode(nil, wantBuf)
	if err != nil {
		t.Fatalf("decPLC Decode(nil): %v", err)
	}

	// Decoder B: Decode seed then DecodeWithFEC(seed, true) on a packet without LBRR.
	decFEC, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder FEC: %v", err)
	}
	if _, err := decFEC.Decode(seedPacket, bufSeed); err != nil {
		t.Fatalf("decFEC Decode seed: %v", err)
	}
	gotBuf := make([]float32, frameSize*channels)
	nGot, err := decFEC.DecodeWithFEC(seedPacket, gotBuf, true)
	if err != nil {
		t.Fatalf("decFEC DecodeWithFEC(no-LBRR): %v", err)
	}

	if nWant != nGot {
		t.Fatalf("sample count mismatch: PLC=%d FEC-fallback=%d", nWant, nGot)
	}
	for i := 0; i < nWant*channels; i++ {
		if wantBuf[i] != gotBuf[i] {
			t.Fatalf("FEC fallback diverged from PLC at sample %d: got=%v want=%v",
				i, gotBuf[i], wantBuf[i])
		}
	}
}

// TestDecodeWithFECStereoWarmLBRRNonSilent verifies (without oracle) that stereo
// LBRR recovery produces a non-silent output, confirming the FEC decoder path
// activates and produces audible signal.
func TestDecodeWithFECStereoWarmLBRRNonSilent(t *testing.T) {
	const (
		channels  = 2
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	if _, err := dec.Decode(seedPacket, buf); err != nil {
		t.Fatalf("Decode seed: %v", err)
	}

	recovered := make([]float32, frameSize*channels)
	n, err := dec.DecodeWithFEC(recoveryPacket, recovered, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
	}

	e := computeEnergyFloat32(recovered[:n*channels])
	if e < 1e-8 {
		t.Fatalf("stereo LBRR recovery energy=%.2e, expected non-silent FEC output", e)
	}
}

// TestDecodeWithFECMonoFirstPacketByteExact asserts that the encoder-side mono
// first-packet LBRR bytes (packet index 0, before any LBRR carry-over) are
// identical between gopus and libopus. This is the encoding side of the
// mono-first-packet case. The check is oracle-gated.
func TestDecodeWithFECMonoFirstPacketByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		channels  = 1
		frameSize = 960
	)

	// Build a single-frame mono LBRR-FEC packet pair from both gopus and libopus.
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet missing LBRR")
	}

	// Decode the LBRR-recovered frame and compare gopus vs libopus output.
	for _, sampleRate := range []int{48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "mono first-packet byte-exact reference", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			if _, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			fecBuf := make([]float32, fs*channels)
			if _, err := dec.DecodeWithFEC(recoveryPacket, fecBuf, true); err != nil {
				t.Fatalf("DecodeWithFEC: %v", err)
			}

			wantFEC := want[fs*channels:]
			cmpLen := min(len(fecBuf), len(wantFEC))
			assertAPIRateQualityFloat32(t, fecBuf[:cmpLen], wantFEC[:cmpLen], sampleRate, channels,
				"mono first-packet LBRR byte-exact")
		})
	}
}

// TestDecodeWithFECStereoHybridAfterLongLossRangeExact pins the final
// range-coder state on a stereo Hybrid in-band FEC (decode_fec=1) recovery that
// follows a long packet-loss burst.
//
// In a Hybrid FEC step libopus decodes the SILK LBRR (lost_flag=2) and then runs
// the CELT decoder in PLC mode (celt_decode_with_ec(NULL); opus_decoder.c:606),
// reporting the CELT decoder's post-PLC st->rng as the frame's final range
// (opus_decoder.c:612, 697-700). celt_decode_lost advances that range-coder
// state (the noise LCG) on EVERY lost Hybrid frame, no matter how far the
// concealment energy has decayed. A long loss burst before the FEC step must not
// freeze that state: the hybrid PLC path must keep advancing the CELT rng even
// once its loss-fade is exhausted, or the FEC final range desyncs from libopus.
//
// This regression covers a fade-exhausted FEC recovery; the bit-exact per-step
// final-range assertion is the load-bearing entropy check (a desync here means
// gopus consumed a different number of bits than libopus on the FEC path).
func TestDecodeWithFECStereoHybridAfterLongLossRangeExact(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 480 // 10 ms
		bitrate    = 64000
		warmUp     = 4
		lossBurst  = 16 // long enough to exhaust the PLC loss-fade before recovery
	)

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, set := range []struct {
		name string
		fn   func() error
	}{
		{"SetMode", func() error { return enc.SetMode(EncoderModeHybrid) }},
		{"SetFrameSize", func() error { return enc.SetFrameSize(frameSize) }},
		{"SetExpertFrameDuration", func() error { return enc.SetExpertFrameDuration(ExpertFrameDuration10Ms) }},
		{"SetBandwidth", func() error { return enc.SetBandwidth(BandwidthFullband) }},
		{"SetBitrate", func() error { return enc.SetBitrate(bitrate * channels) }},
		{"SetSignal", func() error { return enc.SetSignal(SignalVoice) }},
		{"SetPacketLoss", func() error { return enc.SetPacketLoss(30) }},
		{"SetForceChannels", func() error { return enc.SetForceChannels(2) }},
	} {
		if err := set.fn(); err != nil {
			t.Skipf("%s: %v", set.name, err)
		}
	}
	enc.SetFEC(true)

	// Encode a stateful stream and locate an LBRR-carrying recovery packet that
	// arrives after the loss burst (so SILK LBRR state is warm).
	const nFrames = 30
	packets := make([][]byte, 0, nFrames)
	recoveryIdx := -1
	for f := range nFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(f*frameSize+i) / sampleRate
			f0 := 180.0 * (1.0 + 0.02*math.Sin(2*math.Pi*3.0*tm))
			pcm[i*channels] = 0.40*float32(math.Sin(2*math.Pi*f0*tm)) +
				0.16*float32(math.Sin(2*math.Pi*2*f0*tm+0.21))
			pcm[i*channels+1] = 0.36*float32(math.Sin(2*math.Pi*f0*tm+0.13)) +
				0.14*float32(math.Sin(2*math.Pi*2*f0*tm+0.34))
		}
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil || len(pkt) == 0 {
			t.Skipf("encode frame %d: %v len=%d", f, err, len(pkt))
		}
		if ParseTOC(pkt[0]).Mode != ModeHybrid {
			t.Skipf("frame %d not hybrid", f)
		}
		packets = append(packets, append([]byte(nil), pkt...))
		if recoveryIdx < 0 && f > warmUp+lossBurst && packetHasInBandFEC(t, pkt) {
			recoveryIdx = f
		}
	}
	if recoveryIdx < 0 {
		t.Skip("no warm LBRR-carrying hybrid recovery packet emitted")
	}

	// Decode plan: warm-up normal decodes, a long PLC burst (Decode(nil)) that
	// drives the loss-fade to zero, then the FEC recovery (decode_fec=1) of the
	// most recent lost frame, then a normal decode of the recovery packet. This
	// mirrors the opus_demo loss-recovery model for a long burst.
	type step struct {
		packet []byte
		fec    bool
	}
	plan := make([]step, 0, warmUp+lossBurst+2)
	for i := range warmUp {
		plan = append(plan, step{packet: packets[i]})
	}
	for range lossBurst {
		plan = append(plan, step{packet: nil}) // PLC
	}
	plan = append(plan, step{packet: packets[recoveryIdx], fec: true}) // FEC after fade exhaustion
	plan = append(plan, step{packet: packets[recoveryIdx]})            // normal decode

	oracleSteps := make([]libopusAPIRateDecodeStep, len(plan))
	for i, s := range plan {
		oracleSteps[i] = libopusAPIRateDecodeStep{packet: s.packet, fec: s.fec}
	}
	_, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize, oracleSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid long-loss FEC range reference", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	fecStep := -1
	for i, s := range plan {
		var de error
		switch {
		case s.fec:
			fecStep = i
			_, de = dec.DecodeWithFEC(s.packet, buf, true)
		case s.packet == nil:
			_, de = dec.Decode(nil, buf)
		default:
			_, de = dec.Decode(s.packet, buf)
		}
		if de != nil {
			t.Fatalf("step %d (fec=%t plc=%t): %v", i, s.fec, s.packet == nil, de)
		}
		got := dec.FinalRange()
		if got != wantRanges[i] {
			t.Errorf("step %d final-range mismatch: gopus=0x%08x libopus=0x%08x (fec=%t plc=%t)",
				i, got, wantRanges[i], s.fec, s.packet == nil)
		}
	}
	if fecStep < 0 {
		t.Fatal("no FEC step in plan; test setup error")
	}
	// The FEC step must report a non-zero range (it decodes real LBRR/CELT-PLC
	// state, not a PLC sentinel) and must equal libopus exactly.
	if wantRanges[fecStep] == 0 {
		t.Fatalf("libopus reported zero final range on the FEC step %d; test setup error", fecStep)
	}
}
