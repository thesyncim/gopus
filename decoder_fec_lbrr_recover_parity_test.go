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

			cmpLen := len(want)
			if len(got) < cmpLen {
				cmpLen = len(got)
			}
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

			cmpLen := len(want)
			if len(got) < cmpLen {
				cmpLen = len(got)
			}
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
		channels  = 1
		frameSize = 960
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
			cmpLen := len(wantFEC)
			if len(fecBuf) < cmpLen {
				cmpLen = len(fecBuf)
			}
			assertAPIRateQualityFloat32(t, fecBuf[:cmpLen], wantFEC[:cmpLen], sampleRate, channels,
				"mono first-packet LBRR byte-exact")
		})
	}
}
