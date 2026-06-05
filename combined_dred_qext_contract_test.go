//go:build gopus_dred && gopus_qext && !gopus_osce

package gopus

import (
	"reflect"
	"slices"
	"testing"
)

func TestCombinedDREDQEXTBuildOptionalExtensionContract(t *testing.T) {
	if !SupportsOptionalExtension(OptionalExtensionDNNBlob) {
		t.Fatal("combined gopus_dred,gopus_qext build does not report DNN blob support")
	}
	if !SupportsOptionalExtension(OptionalExtensionDRED) {
		t.Fatal("combined gopus_dred,gopus_qext build does not report DRED support")
	}
	if !SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Fatal("combined gopus_dred,gopus_qext build does not report QEXT support")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly reports OSCE BWE support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(extraDREDControl)
	if !ok {
		t.Fatal("combined build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	qext, ok := any(enc).(qextEncoderControl)
	if !ok {
		t.Fatal("combined build does not expose encoder QEXT control")
	}
	assertSupportedQEXTControl(t, qext)

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	if _, ok := any(dec).(extraOSCEBWEControl); ok {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly exposes decoder OSCE BWE control")
	}
	if _, ok := any(dec).(extraOSCELACEControl); ok {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly exposes decoder OSCE LACE control")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, msEnc)
	msDred, ok := any(msEnc).(extraDREDControl)
	if !ok {
		t.Fatal("combined build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	msQEXT, ok := any(msEnc).(qextEncoderControl)
	if !ok {
		t.Fatal("combined build does not expose multistream encoder QEXT control")
	}
	assertSupportedQEXTControl(t, msQEXT)

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	if _, ok := any(msDec).(extraOSCEBWEControl); ok {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly exposes multistream decoder OSCE BWE control")
	}
	if _, ok := any(msDec).(extraOSCELACEControl); ok {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly exposes multistream decoder OSCE LACE control")
	}
}

func TestCombinedDREDQEXTBuildPublicAPIContract(t *testing.T) {
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
				"DREDDuration", "DTXEnabled", "Encode", "EncodeFloat32", "EncodeInt16",
				"EncodeInt16Slice", "EncodeInt24", "EncodeInt24Slice", "ExpertFrameDuration",
				"FECEnabled", "FinalRange", "ForceChannels", "FrameSize", "InBandFEC", "InDTX", "LSBDepth",
				"Lookahead", "MaxBandwidth", "Mode", "PacketLoss", "PhaseInversionDisabled",
				"PredictionDisabled", "QEXT", "Reset", "SampleRate", "SetApplication",
				"SetBandwidth", "SetBandwidthAuto", "SetBitrate", "SetBitrateMode", "SetComplexity", "SetDNNBlob",
				"SetDREDDuration", "SetDTX", "SetExpertFrameDuration", "SetFEC",
				"SetForceChannels", "SetFrameSize", "SetInBandFEC", "SetLSBDepth", "SetMaxBandwidth",
				"SetMode", "SetPacketLoss", "SetPhaseInversionDisabled", "SetPredictionDisabled", "SetQEXT",
				"SetSignal", "SetVBR", "SetVBRConstraint", "Signal", "VADActivity", "VBR", "VBRConstraint",
			},
		},
		{
			name: "Decoder",
			got:  &Decoder{},
			want: []string{
				"Bandwidth", "Channels", "Complexity", "Decode", "DecodeDRED", "DecodeDREDInt24",
				"DecodeInt16", "DecodeInt24", "DecodeInt24Slice", "DecodeWithFEC", "FinalRange",
				"Gain", "IgnoreExtensions", "InDTX", "LastPacketDuration", "PhaseInversionDisabled",
				"Pitch", "Reset", "SampleRate", "SetComplexity", "SetDNNBlob", "SetGain",
				"SetIgnoreExtensions", "SetPhaseInversionDisabled",
			},
		},
		{
			name: "MultistreamEncoder",
			got:  &MultistreamEncoder{},
			want: []string{
				"Application", "Bandwidth", "Bitrate", "BitrateMode", "Channels", "Complexity",
				"CoupledStreams", "DREDDuration", "DTXEnabled", "Encode", "EncodeFloat32",
				"EncodeInt16", "EncodeInt16Slice", "EncodeInt24", "EncodeInt24Slice",
				"ExpertFrameDuration", "FECEnabled", "FinalRange", "ForceChannels", "FrameSize",
				"GetFinalRange", "InBandFEC", "LSBDepth", "Lookahead", "MaxBandwidth", "Mode", "PacketLoss",
				"PhaseInversionDisabled", "PredictionDisabled", "QEXT", "Reset", "SampleRate",
				"SetApplication", "SetBandwidth", "SetBandwidthAuto", "SetBitrate", "SetBitrateMode", "SetComplexity",
				"SetDNNBlob", "SetDREDDuration", "SetDTX", "SetExpertFrameDuration", "SetFEC",
				"SetForceChannels", "SetFrameSize", "SetInBandFEC", "SetLSBDepth", "SetMaxBandwidth",
				"SetMode", "SetPacketLoss", "SetPhaseInversionDisabled", "SetPredictionDisabled", "SetQEXT",
				"SetSignal", "SetVBR", "SetVBRConstraint", "Signal", "Streams", "VBR", "VBRConstraint",
			},
		},
		{
			name: "MultistreamDecoder",
			got:  &MultistreamDecoder{},
			want: []string{
				"Bandwidth", "Channels", "Complexity", "CoupledStreams", "Decode", "DecodeInt16",
				"DecodeInt24", "DecodeInt24Slice", "FinalRange",
				"Gain", "GetFinalRange", "IgnoreExtensions", "LastPacketDuration",
				"PhaseInversionDisabled", "Reset", "SampleRate", "SetComplexity", "SetDNNBlob",
				"SetGain", "SetIgnoreExtensions", "SetPhaseInversionDisabled", "Streams",
			},
		},
		{
			name: "DREDDecoder",
			got:  &DREDDecoder{},
			want: []string{"ModelLoaded", "Parse", "Process", "SetDNNBlob"},
		},
		{
			name: "DRED",
			got:  &DRED{},
			want: []string{
				"Availability", "Clear", "Empty", "FeatureCount", "FeatureWindow",
				"FillFeatures", "FillLatents", "FillQuantizerLevels", "FillState",
				"LatentCount", "Len", "MaxAvailableSamples", "NeedsProcessing", "Parsed",
				"ProcessStage", "Processed", "RawProcessStage", "Result",
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
			got := combinedBuildMethodNames(tc.got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s methods mismatch\n got: %v\nwant: %v", tc.name, got, tc.want)
			}
		})
	}
}

func combinedBuildMethodNames(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	slices.Sort(names)
	return names
}
