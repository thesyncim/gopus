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
	// libopus reference input the same way for apples-to-apples compliance stats
	// where this has shown measurable drift in harness parity.
	if mode == encoder.ModeHybrid ||
		(mode == encoder.ModeSILK && (bandwidth == types.BandwidthNarrowband || bandwidth == types.BandwidthMediumband)) {
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

	// For 40/60ms hybrid frames, mirror gopus packetization: encode 20ms
	// hybrid frames and wrap them in a single code-3 packet.
	if mode == encoder.ModeHybrid && frameSize > 960 && frameSize%960 == 0 {
		subframesPerPacket := frameSize / 960
		if subframesPerPacket < 2 || subframesPerPacket > 3 {
			return nil
		}
		subframeSamples := 960 * channels
		packets := make([][]byte, 0, numFrames)
		for i := 0; i < numFrames; i++ {
			frames := make([][]byte, 0, subframesPerPacket)
			sameSize := true
			prevSize := -1
			packetBase := i * samplesPerFrame
			for j := 0; j < subframesPerPacket; j++ {
				start := packetBase + j*subframeSamples
				end := start + subframeSamples
				data, n := enc.EncodeFloat(samples[start:end], 960)
				if n <= 0 || len(data) < 1 {
					return nil
				}
				// BuildMultiFramePacket expects frame payloads without TOC.
				payload := make([]byte, len(data)-1)
				copy(payload, data[1:])
				frames = append(frames, payload)
				if prevSize >= 0 && len(payload) != prevSize {
					sameSize = false
				}
				prevSize = len(payload)
			}
			packet, err := encoder.BuildMultiFramePacket(
				frames,
				types.ModeHybrid,
				bandwidth,
				960,
				channels == 2,
				!sameSize,
			)
			if err != nil {
				return nil
			}
			packets = append(packets, packet)
		}
		return packets
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
