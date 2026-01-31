// Package cgo tests SILK decoder state on bandwidth change.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12SilkStateOnBWChange tests SILK decoder state reset on bandwidth change.
func TestTV12SilkStateOnBWChange(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder directly
	silkDec := silk.NewDecoder()

	t.Log("=== Testing SILK decoder state across bandwidth changes ===")

	// Track state
	prevBW := silk.Bandwidth(255)
	prevFsKHz := 0

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)
		fsKHz := config.SampleRate / 1000

		// Log bandwidth changes
		if silkBW != prevBW {
			t.Logf("Packet %d: BW change %v->%v (fsKHz %d->%d)",
				i, prevBW, silkBW, prevFsKHz, fsKHz)
		}

		prevBW = silkBW
		prevFsKHz = fsKHz

		// Decode the packet
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		_, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		// Get decoder state for debugging
		state := silkDec.ExportState()

		// Report around bandwidth transitions
		if i == 136 || i == 137 || i == 825 || i == 826 || i == 827 {
			t.Logf("  Packet %d state: fsKHz=%d, lpcOrder=%d, ltpMemLen=%d, prevGainQ16=%d",
				i, state.FsKHz, state.LpcOrder, state.LtpMemLen, state.PrevGainQ16)
			// Show first few sLPC values
			t.Logf("    sLPC[0..3] = [%d, %d, %d, %d]",
				state.SLPCQ14Buf[0], state.SLPCQ14Buf[1], state.SLPCQ14Buf[2], state.SLPCQ14Buf[3])
			// Show first few outBuf values
			if len(state.OutBuf) >= 4 {
				t.Logf("    outBuf[0..3] = [%d, %d, %d, %d]",
					state.OutBuf[0], state.OutBuf[1], state.OutBuf[2], state.OutBuf[3])
			}
		}
	}
}

// TestTV12SilkNativeCompare compares SILK output at native rate using DecodeFrame.
func TestTV12SilkNativeCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder directly
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 8kHz native rate
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8k libopus decoder")
	}
	defer libDec8k.Destroy()

	// Create 12kHz decoder
	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec12k == nil {
		t.Skip("Could not create 12k libopus decoder")
	}
	defer libDec12k.Destroy()

	t.Log("=== SILK native rate comparison ===")

	prevBW := silk.Bandwidth(255)

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Choose libopus decoder
		var libDec *LibopusDecoder
		var delay int
		if silkBW == silk.BandwidthNarrowband {
			libDec = libDec8k
			delay = 5
		} else {
			libDec = libDec12k
			delay = 10
			if silkBW == silk.BandwidthWideband {
				delay = 13
			}
		}

		// Decode with gopus
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Decode with libopus at native rate
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goNative)*2)
		if libSamples < 0 {
			continue
		}

		// Align and compare
		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}
		if alignedLen <= 0 {
			continue
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < alignedLen; j++ {
			diff := goNative[j] - libPcm[j+delay]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j+delay] * libPcm[j+delay])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Log bandwidth changes and failing packets
		isBWChange := silkBW != prevBW
		prevBW = silkBW

		if isBWChange || snr < 40 || (i >= 824 && i <= 828) {
			marker := ""
			if isBWChange {
				marker = " <-- BW CHANGE"
			}
			t.Logf("Packet %4d: BW=%v (%dkHz), NativeSNR=%6.1f dB%s",
				i, silkBW, config.SampleRate/1000, snr, marker)

			// Show first samples for failing packets
			if snr < 40 {
				t.Log("  First 10 samples (go vs lib with delay offset):")
				for j := 0; j < 10 && j < alignedLen; j++ {
					t.Logf("    [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
						j, goNative[j], libPcm[j+delay], goNative[j]-libPcm[j+delay])
				}
			}
		}
	}
}
