//go:build gopus_extra_controls && !gopus_qext

package gopus_test

import (
	"github.com/thesyncim/gopus"
	"reflect"
	"slices"
	"testing"
)

func exportedMethodNamesExtra(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	slices.Sort(names)
	return names
}

func TestExtraControlsBuildPublicAPIContract(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want []string
	}{
		{
			name: "Encoder",
			got:  &gopus.Encoder{},
			want: []string{
				"Application", "Bandwidth", "Bitrate", "BitrateMode", "Channels", "Complexity",
				"DREDDuration", "DTXEnabled", "Encode", "EncodeFloat32", "EncodeInt16",
				"EncodeInt16Slice", "EncodeInt24", "EncodeInt24Slice", "ExpertFrameDuration",
				"FECEnabled", "FinalRange", "ForceChannels", "FrameSize", "InBandFEC", "InDTX", "LSBDepth",
				"Lookahead", "MaxBandwidth", "Mode", "PacketLoss", "PhaseInversionDisabled",
				"PredictionDisabled", "Reset", "SampleRate", "SetApplication",
				"SetBandwidth", "SetBandwidthAuto", "SetBitrate", "SetBitrateMode", "SetComplexity", "SetDNNBlob",
				"SetDREDDuration", "SetDTX", "SetExpertFrameDuration", "SetFEC",
				"SetForceChannels", "SetFrameSize", "SetInBandFEC", "SetLSBDepth", "SetMaxBandwidth",
				"SetMode", "SetPacketLoss", "SetPhaseInversionDisabled", "SetPredictionDisabled",
				"SetSignal", "SetVBR", "SetVBRConstraint", "Signal", "VADActivity", "VBR", "VBRConstraint",
			},
		},
		{
			name: "Decoder",
			got:  &gopus.Decoder{},
			want: []string{
				"Bandwidth", "Channels", "Complexity", "Decode", "DecodeDRED", "DecodeDREDInt24",
				"DecodeInt16", "DecodeInt24", "DecodeInt24Slice", "DecodeWithFEC", "FinalRange",
				"Gain", "IgnoreExtensions", "InDTX", "LastPacketDuration", "OSCEBWE", "OSCELACE",
				"PhaseInversionDisabled", "Pitch", "Reset", "SampleRate", "SetComplexity",
				"SetDNNBlob", "SetGain", "SetIgnoreExtensions", "SetOSCEBWE", "SetOSCELACE",
				"SetPhaseInversionDisabled",
			},
		},
		{
			name: "MultistreamEncoder",
			got:  &gopus.MultistreamEncoder{},
			want: []string{
				"Application", "Bandwidth", "Bitrate", "BitrateMode", "Channels", "Complexity",
				"CoupledStreams", "DREDDuration", "DTXEnabled", "Encode", "EncodeFloat32",
				"EncodeInt16", "EncodeInt16Slice", "EncodeInt24", "EncodeInt24Slice",
				"ExpertFrameDuration", "FECEnabled", "FinalRange", "ForceChannels", "FrameSize",
				"GetFinalRange", "InBandFEC", "LSBDepth", "Lookahead", "MaxBandwidth", "Mode", "PacketLoss",
				"PhaseInversionDisabled", "PredictionDisabled", "Reset", "SampleRate",
				"SetApplication", "SetBandwidth", "SetBandwidthAuto", "SetBitrate", "SetBitrateMode", "SetComplexity",
				"SetDNNBlob", "SetDREDDuration", "SetDTX", "SetExpertFrameDuration", "SetFEC",
				"SetForceChannels", "SetFrameSize", "SetInBandFEC", "SetLSBDepth", "SetMaxBandwidth",
				"SetMode", "SetPacketLoss", "SetPhaseInversionDisabled", "SetPredictionDisabled",
				"SetSignal", "SetVBR", "SetVBRConstraint", "Signal", "Streams", "VBR", "VBRConstraint",
			},
		},
		{
			name: "MultistreamDecoder",
			got:  &gopus.MultistreamDecoder{},
			want: []string{
				"Bandwidth", "Channels", "Complexity", "CoupledStreams", "Decode", "DecodeInt16",
				"DecodeInt24", "DecodeInt24Slice", "FinalRange",
				"Gain", "GetFinalRange", "IgnoreExtensions", "LastPacketDuration", "OSCEBWE",
				"OSCELACE", "PhaseInversionDisabled", "Reset", "SampleRate", "SetComplexity",
				"SetDNNBlob", "SetGain", "SetIgnoreExtensions", "SetOSCEBWE", "SetOSCELACE",
				"SetPhaseInversionDisabled", "Streams",
			},
		},
		{
			name: "DREDDecoder",
			got:  &gopus.DREDDecoder{},
			want: []string{"ModelLoaded", "Parse", "Process", "SetDNNBlob"},
		},
		{
			name: "DRED",
			got:  &gopus.DRED{},
			want: []string{
				"Availability", "Clear", "Empty", "FeatureCount", "FeatureWindow",
				"FillFeatures", "FillLatents", "FillQuantizerLevels", "FillState",
				"LatentCount", "Len", "MaxAvailableSamples", "NeedsProcessing", "Parsed",
				"ProcessStage", "Processed", "RawProcessStage", "Result",
			},
		},
		{
			name: "Reader",
			got:  &gopus.Reader{},
			want: []string{
				"Channels", "LastGranulePos", "Read", "Reset", "SampleRate",
			},
		},
		{
			name: "Writer",
			got:  &gopus.Writer{},
			want: []string{
				"Channels", "Close", "Flush", "Reset", "SampleRate", "SetBitrate",
				"SetComplexity", "SetDTX", "SetFEC", "Write",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := exportedMethodNamesExtra(tc.got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s methods mismatch\n got: %v\nwant: %v", tc.name, got, tc.want)
			}
		})
	}
}
