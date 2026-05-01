//go:build gopus_qext && !gopus_dred && !gopus_unsupported_controls
// +build gopus_qext,!gopus_dred,!gopus_unsupported_controls

package gopus

import (
	"reflect"
	"slices"
	"testing"
)

func exportedMethodNames(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	slices.Sort(names)
	return names
}

func TestQEXTBuildPublicAPIContract(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want []string
	}{
		{
			name: "Encoder",
			got:  &Encoder{},
			want: []string{
				"Application", "Bandwidth", "Bitrate", "BitrateMode", "Channels", "Complexity",
				"DTXEnabled", "Encode", "EncodeFloat32", "EncodeInt16", "EncodeInt16Slice",
				"EncodeInt24", "EncodeInt24Slice", "ExpertFrameDuration", "FECEnabled",
				"FinalRange", "ForceChannels", "FrameSize", "InDTX", "LSBDepth", "Lookahead",
				"MaxBandwidth", "PacketLoss", "PhaseInversionDisabled", "PredictionDisabled",
				"QEXT", "Reset", "SampleRate", "SetApplication", "SetBandwidth", "SetBitrate",
				"SetBitrateMode", "SetComplexity", "SetDNNBlob", "SetDTX", "SetExpertFrameDuration",
				"SetFEC", "SetForceChannels", "SetFrameSize", "SetLSBDepth", "SetMaxBandwidth",
				"SetPacketLoss", "SetPhaseInversionDisabled", "SetPredictionDisabled", "SetQEXT",
				"SetSignal", "SetVBR", "SetVBRConstraint", "Signal", "VADActivity", "VBR", "VBRConstraint",
			},
		},
		{
			name: "Decoder",
			got:  &Decoder{},
			want: []string{
				"Bandwidth", "Channels", "Decode", "DecodeInt16", "DecodeWithFEC", "FinalRange",
				"Gain", "IgnoreExtensions", "InDTX", "LastPacketDuration", "Pitch", "Reset",
				"SampleRate", "SetDNNBlob", "SetGain", "SetIgnoreExtensions",
			},
		},
		{
			name: "MultistreamEncoder",
			got:  &MultistreamEncoder{},
			want: []string{
				"Application", "Bandwidth", "Bitrate", "BitrateMode", "Channels", "Complexity",
				"CoupledStreams", "DTXEnabled", "Encode", "EncodeFloat32", "EncodeInt16",
				"EncodeInt16Slice", "EncodeInt24", "EncodeInt24Slice", "ExpertFrameDuration",
				"FECEnabled", "FinalRange", "ForceChannels", "FrameSize", "GetFinalRange", "LSBDepth",
				"Lookahead", "MaxBandwidth", "PacketLoss", "PhaseInversionDisabled", "PredictionDisabled",
				"QEXT", "Reset", "SampleRate", "SetApplication", "SetBandwidth", "SetBitrate",
				"SetBitrateMode", "SetComplexity", "SetDNNBlob", "SetDTX", "SetExpertFrameDuration",
				"SetFEC", "SetForceChannels", "SetFrameSize", "SetLSBDepth", "SetMaxBandwidth",
				"SetPacketLoss", "SetPhaseInversionDisabled", "SetPredictionDisabled", "SetQEXT",
				"SetSignal", "SetVBR", "SetVBRConstraint", "Signal", "Streams", "VBR", "VBRConstraint",
			},
		},
		{
			name: "MultistreamDecoder",
			got:  &MultistreamDecoder{},
			want: []string{
				"Channels", "CoupledStreams", "Decode", "DecodeInt16", "IgnoreExtensions",
				"Reset", "SampleRate", "SetDNNBlob", "SetIgnoreExtensions", "Streams",
			},
		},
		{
			name: "Reader",
			got:  &Reader{},
			want: []string{
				"Channels", "LastGranulePos", "Read", "Reset", "SampleRate",
			},
		},
		{
			name: "Writer",
			got:  &Writer{},
			want: []string{
				"Channels", "Close", "Flush", "Reset", "SampleRate", "SetBitrate",
				"SetComplexity", "SetDTX", "SetFEC", "Write",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := exportedMethodNames(tc.got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s methods mismatch\n got: %v\nwant: %v", tc.name, got, tc.want)
			}
		})
	}
}
