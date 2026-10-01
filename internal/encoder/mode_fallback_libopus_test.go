//go:build !gopus_fixed_point

package encoder

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func TestForcedSILKShortFrameFallsBackToCELTFloatLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 240
		channels  = 1
	)
	pcm := make([]float32, frameSize*channels)
	seed := uint32(0x53484f52)
	for i := range pcm {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		pcm[i] = float32(int32(seed)>>8) * (0.15 / float32(1<<23))
	}
	params := libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthFullband,
		Bitrate: 128000, Complexity: 5, LSBDepth: 24, ForceChannels: channels, VBR: true,
		VBRConstraint: false, FrameSize: frameSize,
	}
	frames := []libopustest.OpusEncodeFixedMixedFrame{{Format: 1, FloatPCM: pcm}}
	want, err := probeFloatFixedExtensionsMixedOracle(params, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "float short-frame mode fallback oracle", err)
		return
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetBitrate(128000)
	enc.SetBitrateMode(ModeVBR)
	enc.SetVBRConstraint(false)
	enc.SetForceChannels(channels)
	enc.SetComplexity(5)
	enc.SetLSBDepth(24)
	got, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(want) != 1 || want[0].Status != 0 || !bytes.Equal(got, want[0].Packet) || enc.FinalRange() != want[0].FinalRange {
		if len(want) == 1 {
			t.Fatalf("short-frame fallback mismatch: mode=%s packet=%d/%d range=%08x/%08x",
				modeFixtureLabelFromConfig(int(got[0]>>3)), len(got), len(want[0].Packet), enc.FinalRange(), want[0].FinalRange)
		}
		t.Fatalf("short-frame oracle returned %d records, want 1", len(want))
	}
	if modeFixtureLabelFromConfig(int(got[0]>>3)) != "celt" {
		t.Fatalf("short-frame fallback mode=%s, want CELT", modeFixtureLabelFromConfig(int(got[0]>>3)))
	}
}

func TestFloatCELTEnergyMaskQ24OracleWire(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		channels  = 1
	)
	maskQ24 := make([]int32, channels*21)
	mask := make([]float32, len(maskQ24))
	for i := range maskQ24 {
		maskQ24[i] = int32(i%7-3) << 20
		mask[i] = float32(maskQ24[i]) * (1.0 / float32(1<<24))
	}
	frames := make([]libopustest.OpusEncodeFixedMixedFrame, 3)
	pcmFrames := make([][]float32, len(frames))
	for frame := range frames {
		pcm := make([]float32, frameSize*channels)
		seed := uint32(0x4d41534b + frame*131)
		for i := range pcm {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			pcm[i] = float32(int32(seed)>>8) * (0.15 / float32(1<<23))
		}
		pcmFrames[frame] = pcm
		frames[frame] = libopustest.OpusEncodeFixedMixedFrame{Format: 1, FloatPCM: pcm}
	}
	frames[0].EnergyMaskAction = libopustest.OpusEnergyMaskSet
	frames[0].EnergyMask = maskQ24
	frames[2].EnergyMaskAction = libopustest.OpusEnergyMaskClear
	params := libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband,
		Bitrate: 128000, Complexity: 5, ForceChannels: channels, VBR: true, FrameSize: frameSize,
	}
	want, err := probeFloatFixedExtensionsMixedOracle(params, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "float Q24 mask wire oracle", err)
		return
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetBitrate(128000)
	enc.SetBitrateMode(ModeVBR)
	enc.SetForceChannels(channels)
	enc.SetComplexity(5)
	enc.SetCELTEnergyMask(mask)
	for frame, pcm := range pcmFrames {
		if frame == 2 {
			enc.SetCELTEnergyMask(nil)
		}
		got, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", frame, err)
		}
		if want[frame].Status != 0 || !bytes.Equal(got, want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
			t.Fatalf("frame %d float mask mismatch: packet=%d/%d range=%08x/%08x",
				frame, len(got), len(want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
		}
	}
}

func probeFloatFixedExtensionsMixedOracle(p libopustest.OpusEncodeFixedParams, frames []libopustest.OpusEncodeFixedMixedFrame) ([]libopustest.OpusEncodeFixedRecord, error) {
	if extsupport.QEXT {
		return libopustest.ProbeOpusEncodeFloatQEXTRuntimeOffMixedRecords(p, frames)
	}
	return libopustest.ProbeOpusEncodeFloatBuildMixedRecords(p, frames)
}
