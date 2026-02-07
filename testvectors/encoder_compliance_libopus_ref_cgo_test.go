//go:build cgo_libopus

package testvectors

import (
	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func libopusComplianceReferenceAvailable() bool {
	return true
}

func encodeWithLibopusComplianceReference(
	samples []float32,
	sampleRate, channels, bitrate, frameSize int,
	mode encoder.Mode,
	bandwidth types.Bandwidth,
) [][]byte {
	// SILK analysis in gopus enters through PCM16-rounded samples. Quantize the
	// libopus reference input the same way for apples-to-apples compliance stats.
	if mode == encoder.ModeSILK || mode == encoder.ModeHybrid {
		samples = quantizeFloat32SignalToPCM16(samples)
	}

	app := cgowrap.OpusApplicationAudio
	signal := cgowrap.OpusSignalMusic
	forceMode := 0

	switch mode {
	case encoder.ModeSILK:
		// Use libopus' SILK-only application mode for the reference path.
		// This matches our direct SILK parity tests and avoids VoIP-specific
		// control behavior skewing the compliance baseline.
		app = cgowrap.OpusApplicationRestrictedSilk
		signal = cgowrap.OpusSignalVoice
		forceMode = cgowrap.ModeSilkOnly
	case encoder.ModeHybrid:
		// Hybrid in gopus is currently tuned through the general Opus path
		// rather than VoIP-specific control logic.
		app = cgowrap.OpusApplicationAudio
		signal = cgowrap.OpusSignalMusic
		forceMode = cgowrap.ModeHybrid
	case encoder.ModeCELT:
		app = cgowrap.OpusApplicationAudio
		signal = cgowrap.OpusSignalMusic
		forceMode = cgowrap.ModeCeltOnly
	}

	enc, err := cgowrap.NewLibopusEncoder(sampleRate, channels, app)
	if err != nil || enc == nil {
		return nil
	}
	defer enc.Destroy()

	enc.SetBitrate(bitrate)
	enc.SetComplexity(10)
	enc.SetVBR(true)
	enc.SetSignal(signal)
	enc.SetBandwidth(mapToLibopusBandwidth(bandwidth))
	if forceMode != 0 {
		enc.SetForceMode(forceMode)
	}

	samplesPerFrame := frameSize * channels
	numFrames := len(samples) / samplesPerFrame
	if numFrames < 1 {
		return nil
	}

	packets := make([][]byte, 0, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		data, n := enc.EncodeFloat(samples[start:end], frameSize)
		if n <= 0 || len(data) == 0 {
			return nil
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		packets = append(packets, cp)
	}
	return packets
}

func mapToLibopusBandwidth(bw types.Bandwidth) int {
	switch bw {
	case types.BandwidthNarrowband:
		return cgowrap.OpusBandwidthNarrowband
	case types.BandwidthMediumband:
		return cgowrap.OpusBandwidthMediumband
	case types.BandwidthWideband:
		return cgowrap.OpusBandwidthWideband
	case types.BandwidthSuperwideband:
		return cgowrap.OpusBandwidthSuperwideband
	case types.BandwidthFullband:
		return cgowrap.OpusBandwidthFullband
	default:
		return cgowrap.OpusBandwidthFullband
	}
}
