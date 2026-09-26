//go:build gopus_dred || gopus_osce

// encoder_dred_duration_differential_fuzz_test.go — differential fuzz for the
// DRED-carrying ENCODE path against the same-arch libopus DRED emit oracle,
// sweeping OPUS_SET_DRED_DURATION × primary mode × VBR/CBR. It complements the
// fixed-duration (80) DRED parity tests in encoder_dred_packet_libopus_parity_test.go
// by asserting the carried DRED payload is byte-exact across the full DRED
// redundancy-depth range.
//
// For each (mode, duration, rate-control) point the harness drives both the
// gopus encoder and the libopus opus_encode_float + OPUS_SET_DRED_DURATION oracle
// with identical voiced PCM across a 640-frame window, then asserts:
//   - the DRED emission frame index or absence matches,
//   - the first emitted DRED payload is byte-exact,
//   - the first emitted DRED-payload frame offset matches.
//
// The DRED payload passes through float DNN kernels, so each Go lane uses the
// instruction-paired libopus DRED reference. This harness compares the carried
// DRED payload, offset, and emission cadence independently of the primary frame.

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	encpkg "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestEncoderDREDDurationDifferentialFuzz sweeps the DRED redundancy depth across
// SILK/Hybrid/CELT primary modes and VBR/CBR, asserting the carried DRED payload
// matches libopus byte-for-byte at every duration where it emits, and that
// absence agrees where neither encoder emits in the complete search window.
func TestEncoderDREDDurationDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	// DRED durations in 10 ms units. libopus clamps to [0,104]; the high end
	// exercises the maximum 104-unit redundancy window. 0 means "no DRED" and is
	// excluded — this sweep targets the carried payload across the active range.
	//
	// The full active range includes short CBR durations. If no DRED fits in
	// the 640-frame window, both encoders must agree on that absence.
	durations := []int{8, 16, 32, 48, 64, 80, 96, 104}

	modes := []struct {
		name      string
		mode      encpkg.Mode
		public    Mode
		bandwidth Bandwidth
		channels  int
	}{
		{"silk_wb_mono", encpkg.ModeSILK, ModeSILK, BandwidthWideband, 1},
		{"silk_wb_stereo", encpkg.ModeSILK, ModeSILK, BandwidthWideband, 2},
		{"hybrid_fb_mono", encpkg.ModeHybrid, ModeHybrid, BandwidthFullband, 1},
		{"celt_fb_mono", encpkg.ModeCELT, ModeCELT, BandwidthFullband, 1},
		{"celt_fb_stereo", encpkg.ModeCELT, ModeCELT, BandwidthFullband, 2},
	}

	rcModes := []struct {
		name string
		cbr  bool
	}{
		{"vbr", false},
		{"cbr", true},
	}

	const frameSize = 960 // 20 ms at 48 kHz
	var (
		tested        int
		payloadOK     int
		indexMismatch int
		offsetMismat  int
		payloadFails  int
		absentOK      int
	)

	for _, m := range modes {
		for _, rc := range rcModes {
			for _, dur := range durations {
				name := fmt.Sprintf("%s/%s/dur%d", m.name, rc.name, dur)
				t.Run(name, func(t *testing.T) {
					tested++
					cfg := libopusDREDPacketConfig{
						FrameSize:    frameSize,
						ForceMode:    m.public,
						Bandwidth:    m.bandwidth,
						Channels:     m.channels,
						CBR:          rc.cbr,
						DREDDuration: dur,
					}
					if rc.cbr {
						cfg.Bitrate = 64000
						if m.public == ModeSILK {
							cfg.Bitrate = 32000
						}
					}
					packetInfo, err := emitLibopusDREDPacketWithConfigRecord(cfg, true)
					if err != nil {
						t.Fatalf("DRED duration packet oracle: %v", err)
					}
					bitrate := 0
					if rc.cbr {
						bitrate = cfg.Bitrate
					}
					settings := encoderDREDPacketSettings{
						mode: m.mode, bandwidth: m.bandwidth, frameSize: frameSize,
						channels: m.channels, bitrate: bitrate, cbr: rc.cbr, dredDuration: dur,
					}
					if len(packetInfo.packet) == 0 {
						gotPacket, gotPayload, _, gotFrameIndex := scanDREDEmissionWithSettings(t, settings, false)
						if gotPacket != nil || gotPayload != nil || gotFrameIndex != packetInfo.frameIndex {
							t.Fatalf("DRED absence mismatch: Go packet=%d payload=%d frame=%d C frame=%d",
								len(gotPacket), len(gotPayload), gotFrameIndex, packetInfo.frameIndex)
						}
						absentOK++
						return
					}
					wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
					if err != nil {
						t.Fatalf("findDREDPayload(libopus) error: %v", err)
					}
					if !ok {
						t.Fatalf("libopus %s packet missing DRED payload at duration %d", m.name, dur)
					}

					gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithSettings(t, settings)
					if ParseTOC(gotPacket[0]).Mode != m.public {
						t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, m.public)
					}
					if gotFrameIndex != packetInfo.frameIndex {
						indexMismatch++
						t.Fatalf("DRED emission frame index=%d want %d (dur=%d)", gotFrameIndex, packetInfo.frameIndex, dur)
					}
					if gotOffset != wantOffset {
						offsetMismat++
						t.Fatalf("DRED frameOffset=%d want %d (dur=%d)", gotOffset, wantOffset, dur)
					}
					if !bytes.Equal(gotPayload, wantPayload) {
						payloadFails++
						t.Fatalf("DRED payload mismatch at duration %d\n got=%x\nwant=%x", dur, gotPayload, wantPayload)
					}
					payloadOK++
				})
			}
		}
	}
	t.Logf("DRED duration differential sweep: %d specs; payload-exact=%d absent-exact=%d index-mismatch=%d offset-mismatch=%d payload-fails=%d",
		tested, payloadOK, absentOK, indexMismatch, offsetMismat, payloadFails)
}
