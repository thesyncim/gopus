//go:build gopus_dred || gopus_osce

package gopus

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"math"
	"os"
	"testing"
)

func TestDecodeDREDInt24NoOverflowOrPrematureAlloc(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	initialCap := cap(dec.scratchPCM)

	// Overflowing frameSizeSamples * channels must return an error, never panic.
	pcm24 := make([]int32, 960*2)
	if _, err := dec.DecodeDREDInt24(NewDRED(), 0, pcm24, math.MaxInt/2+1); err == nil {
		t.Fatal("DecodeDREDInt24 accepted overflowing frameSizeSamples")
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeDREDInt24 grew scratchPCM cap from %d to %d on invalid call", initialCap, cap(dec.scratchPCM))
	}

	pcmF32 := make([]float32, 960*2)
	if _, err := dec.DecodeDRED(NewDRED(), 0, pcmF32, math.MaxInt/2+1); err == nil {
		t.Fatal("DecodeDRED accepted overflowing frameSizeSamples")
	}
}

func TestDREDDecoderParseAndProcessRetainsMetadata(t *testing.T) {
	libopustest.RequireOracle(t)
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	dred := NewDRED()

	available, dredEnd, err := dec.Parse(dred, packet, 960, 48000, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if available != 480 || dredEnd != 480 {
		t.Fatalf("Parse=(available=%d,dredEnd=%d) want (480,480)", available, dredEnd)
	}
	if dred.ProcessStage() != DREDProcessStageDeferred || !dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("stage after deferred parse = (%d, needs=%v, processed=%v) want (%d,true,false)", dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed(), DREDProcessStageDeferred)
	}
	if dred.RawProcessStage() != 1 {
		t.Fatalf("RawProcessStage()=%d want 1", dred.RawProcessStage())
	}
	if dred.Len() == 0 {
		t.Fatal("deferred parse did not retain DRED payload")
	}
	if got := dred.Parsed().Header.DredOffset; got != -4 {
		t.Fatalf("Parsed().Header.DredOffset=%d want -4", got)
	}
	if got := dred.Parsed().Header.DredFrameOffset; got != 8 {
		t.Fatalf("Parsed().Header.DredFrameOffset=%d want 8", got)
	}
	decodeWant, err := probeLibopusDREDDecode(packet, 960, 48000)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred decode", err)
	}
	if decodeWant.availableSamples < 0 {
		t.Fatalf("libopus dred decode returned error %d", decodeWant.availableSamples)
	}
	if got := dred.LatentCount(); got != decodeWant.nbLatents {
		t.Fatalf("LatentCount()=%d want %d", got, decodeWant.nbLatents)
	}
	state := make([]float32, internaldred.StateDim)
	if n := dred.FillState(state); n != internaldred.StateDim {
		t.Fatalf("FillState count=%d want %d", n, internaldred.StateDim)
	}
	assertFloat32BitsEqual(t, state, decodeWant.state[:], "state")
	latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
	wantLatents := decodeWant.nbLatents * internaldred.LatentStride
	if n := dred.FillLatents(latents); n != wantLatents {
		t.Fatalf("FillLatents count=%d want %d", n, wantLatents)
	}
	assertFloat32BitsEqual(t, latents[:wantLatents], decodeWant.latents, "latents")

	processed := NewDRED()
	if err := dec.Process(dred, processed); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if processed.ProcessStage() != DREDProcessStageProcessed || processed.NeedsProcessing() || !processed.Processed() {
		t.Fatalf("processed stage = (%d, needs=%v, processed=%v) want (%d,false,true)", processed.ProcessStage(), processed.NeedsProcessing(), processed.Processed(), DREDProcessStageProcessed)
	}
	if processed.RawProcessStage() != 2 {
		t.Fatalf("RawProcessStage()=%d want 2", processed.RawProcessStage())
	}
	result := processed.Result(960, 48000)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("Result=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", result)
	}
	if got := processed.Availability(960, 48000); got != result.Availability {
		t.Fatalf("Availability()=%+v want %+v", got, result.Availability)
	}
	if got := processed.MaxAvailableSamples(960, 48000); got != 480 {
		t.Fatalf("MaxAvailableSamples()=%d want 480", got)
	}
	quant := make([]int32, 6)
	if n := processed.FillQuantizerLevels(quant, 10080, 48000); n != 0 {
		t.Fatalf("FillQuantizerLevels count=%d want 0", n)
	}
	window := result.FeatureWindow(3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("FeatureWindow=%+v want base=5 recoverable=0 missing=2", window)
	}
	if got := processed.FeatureWindow(960, 48000, 3840, 960, 0); got != window {
		t.Fatalf("FeatureWindow()=%+v want %+v", got, window)
	}
	if err := dec.Process(processed, processed); err != nil {
		t.Fatalf("Process(processed, processed) error: %v", err)
	}
	processedState := make([]float32, internaldred.StateDim)
	if n := processed.FillState(processedState); n != internaldred.StateDim {
		t.Fatalf("processed FillState count=%d want %d", n, internaldred.StateDim)
	}
	assertFloat32BitsEqual(t, processedState, state, "processed state")
	processedLatents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
	if n := processed.FillLatents(processedLatents); n != wantLatents {
		t.Fatalf("processed FillLatents count=%d want %d", n, wantLatents)
	}
	assertFloat32BitsEqual(t, processedLatents[:wantLatents], latents[:wantLatents], "processed latents")
	if got, want := processed.FeatureCount(), decodeWant.nbLatents*4*internaldred.NumFeatures; got != want {
		t.Fatalf("FeatureCount()=%d want %d", got, want)
	}
	features := make([]float32, processed.FeatureCount())
	if n := processed.FillFeatures(features); n != len(features) {
		t.Fatalf("FillFeatures count=%d want %d", n, len(features))
	}
	for i, v := range features {
		if v != 0 {
			t.Fatalf("FillFeatures[%d]=%v want 0 for zeroed synthetic model", i, v)
		}
	}
}

func TestStandaloneDREDParseMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	base := makeValidCELTPacketForDREDTest(t)
	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "valid_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "first_extension_invalid_second_valid",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "short_experimental_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}},
			}),
			maxDREDSamples: 960,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dred := NewDRED()
			available, dredEnd, err := dec.Parse(dred, tc.packet, tc.maxDREDSamples, 48000, true)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if dred.ProcessStage() != DREDProcessStageDeferred || !dred.NeedsProcessing() || dred.Processed() {
				t.Fatalf("stage after deferred Parse = (%d, needs=%v, processed=%v) want (%d,true,false)", dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed(), DREDProcessStageDeferred)
			}

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred parse", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}
			if available != want.availableSamples {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if dredEnd != want.dredEndSamples {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}

			decodeWant, err := probeLibopusDREDDecode(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred decode", err)
			}
			if decodeWant.availableSamples < 0 {
				t.Fatalf("libopus dred decode returned error %d", decodeWant.availableSamples)
			}
			if dred.RawProcessStage() != decodeWant.processStage {
				t.Fatalf("RawProcessStage()=%d want %d", dred.RawProcessStage(), decodeWant.processStage)
			}
			if got := dred.Parsed().Header.DredOffset; got != int32(decodeWant.dredOffset) {
				t.Fatalf("Parsed().Header.DredOffset=%d want %d", got, decodeWant.dredOffset)
			}
			if got := dred.LatentCount(); got != decodeWant.nbLatents {
				t.Fatalf("LatentCount()=%d want %d", got, decodeWant.nbLatents)
			}

			state := make([]float32, internaldred.StateDim)
			if n := dred.FillState(state); n != internaldred.StateDim {
				t.Fatalf("FillState count=%d want %d", n, internaldred.StateDim)
			}
			assertFloat32BitsEqual(t, state, decodeWant.state[:], "state")

			latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
			wantLatents := decodeWant.nbLatents * internaldred.LatentStride
			if n := dred.FillLatents(latents); n != wantLatents {
				t.Fatalf("FillLatents count=%d want %d", n, wantLatents)
			}
			assertFloat32BitsEqual(t, latents[:wantLatents], decodeWant.latents, "latents")
		})
	}
}

func makeTwoFramePacketWithDREDForStandaloneTest(t *testing.T, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}
	body := makeExperimentalDREDPayloadBodyForTest(t, dredFrameOffset, dredOffset)
	packet := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, packet, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	return packet[:n]
}

func TestDREDDecoderParseRequiresModel(t *testing.T) {
	dec := NewDREDDecoder()
	dred := NewDRED()
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)

	if _, _, err := dec.Parse(dred, packet, 960, 48000, true); !errors.Is(err, ErrDREDModelNotLoaded) {
		t.Fatalf("Parse without model error=%v want=%v", err, ErrDREDModelNotLoaded)
	}
	if dred.RawProcessStage() != -1 {
		t.Fatalf("RawProcessStage()=%d want -1", dred.RawProcessStage())
	}
	if dred.ProcessStage() != DREDProcessStageEmpty || !dred.Empty() || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse without model mutated dred state: stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderSetDNNBlobRejectsNameOnlyModelBlob(t *testing.T) {
	dec := NewDREDDecoder()

	if err := dec.SetDNNBlob(makeNameCompleteDREDDecoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(name-only DRED decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
	if dec.ModelLoaded() {
		t.Fatal("DRED decoder retained name-only model blob")
	}
}

func TestDREDDecoderParseClearsStateWhenPacketHasNoDRED(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	withDRED := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(dred, withDRED, 960, 48000, false); err != nil {
		t.Fatalf("Parse(withDRED) error: %v", err)
	}
	if dred.Empty() {
		t.Fatal("expected retained DRED payload after Parse(withDRED)")
	}

	withoutDRED := makeValidCELTPacketForDREDTest(t)
	available, dredEnd, err := dec.Parse(dred, withoutDRED, 960, 48000, false)
	if err != nil {
		t.Fatalf("Parse(withoutDRED) error: %v", err)
	}
	if available != 0 || dredEnd != 0 {
		t.Fatalf("Parse(withoutDRED)=(available=%d,dredEnd=%d) want (0,0)", available, dredEnd)
	}
	if !dred.Empty() || dred.ProcessStage() != DREDProcessStageEmpty || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse(withoutDRED) left dred state stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderProcessRejectsEmptyState(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	if err := dec.Process(dred, dred); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Process(empty, empty) error=%v want=%v", err, ErrInvalidArgument)
	}
}

func TestDREDDecoderProcessDoesNotAllocate(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	src := NewDRED()
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(src, packet, 960, 48000, true); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	dst := NewDRED()

	allocs := testing.AllocsPerRun(1000, func() {
		if err := dec.Process(src, dst); err != nil {
			t.Fatalf("Process error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestDREDDecoderParseAndProcessDoesNotAllocate(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	dred := NewDRED()

	allocs := testing.AllocsPerRun(1000, func() {
		if _, _, err := dec.Parse(dred, packet, 960, 48000, false); err != nil {
			t.Fatalf("Parse error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestDREDDecoderParseClearsStateOnMalformedPacket(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	valid := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(dred, valid, 960, 48000, false); err != nil {
		t.Fatalf("Parse(valid) error: %v", err)
	}
	if dred.Empty() || !dred.Processed() {
		t.Fatal("expected processed retained DRED state after valid parse")
	}

	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, makeValidCELTPacketForDREDTest(t))
	if _, _, err := dec.Parse(dred, malformed, 960, 48000, false); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Parse(malformed) error=%v want=%v", err, ErrInvalidPacket)
	}
	if !dred.Empty() || dred.Len() != 0 || dred.ProcessStage() != DREDProcessStageEmpty || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse(malformed) left stale dred state: len=%d stage=%d needs=%v processed=%v", dred.Len(), dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed())
	}
}

func buildLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, includeInternal)
}

func cachedLibopusDREDHelperPath(cache *libopustest.HelperCache, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return cache.Path(func() (string, error) {
		return buildLibopusDREDHelper(sourceFile, outputBase, includeInternal)
	})
}

var (
	libopusPitchDNNModelBlobHelper libopustest.HelperCache
	libopusPLCModelBlobHelper      libopustest.HelperCache
	libopusFARGANModelBlobHelper   libopustest.HelperCache
)

func getLibopusPitchDNNModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusPitchDNNModelBlobHelper, "libopus_pitchdnn_model_blob.c", "gopus_libopus_pitchdnn_model_blob", true)
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusPLCModelBlobHelper, "libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob", true)
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusFARGANModelBlobHelper, "libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob", true)
}

func runModelBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDecoderNeuralModelBlob() ([]byte, error) {
	pitchPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	plcPath, err := getLibopusPLCModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	farganPath, err := getLibopusFARGANModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	pitchBlob, err := runModelBlobHelper(pitchPath)
	if err != nil {
		return nil, err
	}
	plcBlob, err := runModelBlobHelper(plcPath)
	if err != nil {
		return nil, err
	}
	farganBlob, err := runModelBlobHelper(farganPath)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func requireLibopusDecoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder neural model", err)
	}
	return blob
}

const libopusDREDPacketOutputMagic = "GODP"
const libopusDREDPacketMaxFramesToTry = 640

type libopusDREDPacket struct {
	sampleRate     int
	maxDREDSamples int
	frameIndex     int
	packet         []byte
}

func scaleDREDSampleCount(samples, fromRate, toRate int) int {
	if samples <= 0 || fromRate <= 0 || toRate <= 0 || fromRate == toRate {
		return samples
	}
	return samples * toRate / fromRate
}

func scaleDREDSampleOffset(samples, fromRate, toRate int) int {
	if samples == 0 || fromRate <= 0 || toRate <= 0 || fromRate == toRate {
		return samples
	}
	return samples * toRate / fromRate
}

func libopusDREDRequestForDecoder(packetInfo libopusDREDPacket, decoderSampleRate int) (maxDREDSamples, sampleRate int) {
	sampleRate = decoderSampleRate
	if sampleRate <= 0 {
		sampleRate = packetInfo.sampleRate
	}
	maxDREDSamples = packetInfo.maxDREDSamples
	if packetInfo.sampleRate > 0 && sampleRate > 0 {
		maxDREDSamples = scaleDREDSampleCount(maxDREDSamples, packetInfo.sampleRate, sampleRate)
	}
	return maxDREDSamples, sampleRate
}

type libopusDREDPacketConfig struct {
	FrameSize     int
	ForceMode     Mode
	Bandwidth     Bandwidth
	Channels      int
	ForceChannels int
	Bitrate       int
	CBR           bool
	Multistream   bool
	// DREDDuration is the OPUS_SET_DRED_DURATION value passed to libopus. Zero
	// selects the helper default (80).
	DREDDuration int
}

var libopusDREDEmitPacketHelper libopustest.HelperCache

func getLibopusDREDEmitPacketHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDEmitPacketHelper, "libopus_dred_emit_packet.c", "gopus_libopus_dred_emit_packet", true)
}

func emitLibopusDREDPacket() (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func emitLibopusDREDPacketWithFrameSize(frameSize int) (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func emitLibopusDREDPacketWithConfig(cfg libopusDREDPacketConfig) (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfigRecord(cfg, false)
}

// The record variant returns frameIndex=640 and an empty packet when the
// selected libopus encoder emitted no DRED across the complete search window.
func emitLibopusDREDPacketWithConfigRecord(cfg libopusDREDPacketConfig, recordAbsence bool) (libopusDREDPacket, error) {
	binPath, err := getLibopusDREDEmitPacketHelperPath()
	if err != nil {
		return libopusDREDPacket{}, err
	}
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = encoderDREDBitrateForFrameSize(cfg.FrameSize)
	}
	forceModeEnv, err := libopusDREDForceModeEnv(cfg.ForceMode)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	bandwidthEnv, err := libopusDREDBandwidthEnv(cfg.Bandwidth)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	env := []string{
		fmt.Sprintf("GOPUS_DRED_FRAME_SIZE=%d", cfg.FrameSize),
		fmt.Sprintf("GOPUS_DRED_CHANNELS=%d", cfg.Channels),
		fmt.Sprintf("GOPUS_DRED_FORCE_MODE=%s", forceModeEnv),
		fmt.Sprintf("GOPUS_DRED_BANDWIDTH=%s", bandwidthEnv),
		fmt.Sprintf("GOPUS_DRED_BITRATE=%d", cfg.Bitrate),
		"GOPUS_DRED_PCM_STDIN=1",
	}
	if cfg.CBR {
		env = append(env, "GOPUS_DRED_CBR=1")
	}
	if cfg.ForceChannels != 0 {
		env = append(env, fmt.Sprintf("GOPUS_DRED_FORCE_CHANNELS=%d", cfg.ForceChannels))
	}
	if cfg.Multistream {
		env = append(env, "GOPUS_DRED_MULTISTREAM=1")
	}
	if cfg.DREDDuration != 0 {
		env = append(env, fmt.Sprintf("GOPUS_DRED_DURATION=%d", cfg.DREDDuration))
	}
	if recordAbsence {
		env = append(env, "GOPUS_DRED_EMISSION_RECORD=1")
	}
	out, err := libopustest.RunHelperEnv(binPath, libopusDREDPacketPCMInput(cfg, libopusDREDPacketMaxFramesToTry), env)
	if err != nil {
		return libopusDREDPacket{}, fmt.Errorf("run dred emit helper: %w", err)
	}

	reader, version, err := libopustest.NewOracleReaderVersion("dred emit", libopusDREDPacketOutputMagic, out)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	if recordAbsence && version != 3 || !recordAbsence && version != 1 && version != 2 {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper version=%d recordAbsence=%t", version, recordAbsence)
	}
	info := libopusDREDPacket{
		sampleRate:     int(reader.U32()),
		maxDREDSamples: int(reader.U32()),
	}
	packetLen := int(reader.U32())
	if version >= 2 {
		info.frameIndex = int(reader.U32())
	}
	packet := reader.Bytes(packetLen)
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDPacket{}, err
	}
	info.packet = append([]byte(nil), packet...)
	if len(info.packet) == 0 && recordAbsence && info.frameIndex == libopusDREDPacketMaxFramesToTry {
		return info, nil
	}
	if len(info.packet) == 0 {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper returned an empty packet")
	}
	toc := ParseTOC(info.packet[0])
	if toc.Mode != cfg.ForceMode {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper mode=%v want %v", toc.Mode, cfg.ForceMode)
	}
	if toc.Bandwidth != cfg.Bandwidth {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper bandwidth=%v want %v", toc.Bandwidth, cfg.Bandwidth)
	}
	if cfg.ForceChannels != 0 && toc.Stereo != (cfg.ForceChannels == 2) {
		gotChannels := 1
		if toc.Stereo {
			gotChannels = 2
		}
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper channels=%d want %d", gotChannels, cfg.ForceChannels)
	}
	packetDuration, err := opusPacketDurationSamples(info.packet)
	if err != nil {
		return libopusDREDPacket{}, fmt.Errorf("parse dred emit helper packet duration: %w", err)
	}
	if packetDuration != cfg.FrameSize {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper packet duration=%d want %d", packetDuration, cfg.FrameSize)
	}
	return info, nil
}

func libopusDREDPacketPCMInput(cfg libopusDREDPacketConfig, maxFrames int) []byte {
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	out := make([]byte, 0, maxFrames*cfg.FrameSize*cfg.Channels*4)
	var tmp [4]byte
	for frameIdx := 0; frameIdx < maxFrames; frameIdx++ {
		for sampleIdx := 0; sampleIdx < cfg.FrameSize; sampleIdx++ {
			bits := math.Float32bits(encoderDREDVoicedSample(frameIdx, sampleIdx, cfg.FrameSize, 48000))
			binary.LittleEndian.PutUint32(tmp[:], bits)
			for ch := 0; ch < cfg.Channels; ch++ {
				out = append(out, tmp[:]...)
			}
		}
	}
	return out
}

func opusPacketDurationSamples(packet []byte) (int, error) {
	info, err := ParsePacket(packet)
	if err != nil {
		return 0, err
	}
	return info.TOC.FrameSize * info.FrameCount, nil
}

func libopusDREDForceModeEnv(mode Mode) (string, error) {
	switch mode {
	case ModeCELT:
		return "celt", nil
	case ModeHybrid:
		return "hybrid", nil
	case ModeSILK:
		return "silk", nil
	default:
		return "", fmt.Errorf("extra libopus dred packet force mode %v", mode)
	}
}

func libopusDREDBandwidthEnv(bandwidth Bandwidth) (string, error) {
	switch bandwidth {
	case BandwidthWideband:
		return "wb", nil
	case BandwidthSuperwideband:
		return "swb", nil
	case BandwidthFullband:
		return "fb", nil
	default:
		return "", fmt.Errorf("extra libopus dred packet bandwidth %v", bandwidth)
	}
}

func TestParsedDREDAvailabilityMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}

	twoFramePacket := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, twoFramePacket, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 8, -4)...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	twoFramePacket = twoFramePacket[:n]

	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "single_frame_offset_positive",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name:           "single_frame_offset_positive_large_request",
			packet:         buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)}}),
			maxDREDSamples: 10080,
		},
		{
			name:           "second_frame_negative_offset",
			packet:         twoFramePacket,
			maxDREDSamples: 960,
		},
		{
			name: "first_dred_extension_invalid_second_valid",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "short_experimental_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}},
			}),
			maxDREDSamples: 960,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, frameOffset, ok, err := findDREDPayload(tc.packet)
			if err != nil {
				t.Fatalf("findDREDPayload error: %v", err)
			}
			if !ok {
				t.Fatal("findDREDPayload returned ok=false")
			}

			parsed, err := internaldred.ParsePayload(payload, frameOffset)
			if err != nil {
				t.Fatalf("ParsePayload error: %v", err)
			}
			got := parsed.Availability(tc.maxDREDSamples, 48000)

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred parse", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}

			if got.AvailableSamples != want.availableSamples {
				t.Fatalf("AvailableSamples=%d want %d", got.AvailableSamples, want.availableSamples)
			}
			if got.EndSamples != want.dredEndSamples {
				t.Fatalf("EndSamples=%d want %d", got.EndSamples, want.dredEndSamples)
			}

			span := internaldred.LatentSpanSamples(48000)
			if span <= 0 {
				t.Fatal("invalid latent span")
			}
			wantLatents := (want.availableSamples + got.OffsetSamples) / span
			if wantLatents < 0 {
				wantLatents = 0
			}
			if got.MaxLatents != wantLatents {
				t.Fatalf("MaxLatents=%d want %d", got.MaxLatents, wantLatents)
			}
		})
	}
}

const (
	libopusDREDParseInputMagic  = "GODI"
	libopusDREDParseOutputMagic = "GODO"
)

type libopusDREDParseInfo struct {
	availableSamples int
	dredEndSamples   int
}

type libopusDREDDecodeInfo struct {
	availableSamples int
	dredEndSamples   int
	processStage     int
	nbLatents        int
	dredOffset       int
	state            [internaldred.StateDim]float32
	latents          []float32
}

var (
	libopusDREDParseHelper  libopustest.HelperCache
	libopusDREDDecodeHelper libopustest.HelperCache
)

func getLibopusDREDParseHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDParseHelper, "libopus_dred_parse_info.c", "gopus_libopus_dred_parse", false)
}

func getLibopusDREDDecodeHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDDecodeHelper, "libopus_dred_decode_info.c", "gopus_libopus_dred_decode", true)
}

func probeLibopusDREDParse(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDParseInfo, error) {
	binPath, err := getLibopusDREDParseHelperPath()
	if err != nil {
		return libopusDREDParseInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDParseInfo{}, fmt.Errorf("run dred helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred parse", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDParseInfo{}, err
	}
	reader.ExpectRemaining(8)
	ret := int(reader.I32())
	dredEnd := int(reader.I32())
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDParseInfo{}, err
	}
	return libopusDREDParseInfo{
		availableSamples: ret,
		dredEndSamples:   dredEnd,
	}, nil
}

func probeLibopusDREDDecode(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDDecodeInfo, error) {
	binPath, err := getLibopusDREDDecodeHelperPath()
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDDecodeInfo{}, fmt.Errorf("run dred decode helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred decode", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	info := libopusDREDDecodeInfo{
		availableSamples: int(reader.I32()),
		dredEndSamples:   int(reader.I32()),
		processStage:     int(reader.I32()),
		nbLatents:        int(reader.I32()),
		dredOffset:       int(reader.I32()),
	}

	for i := range info.state {
		info.state[i] = reader.Float32()
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		info.latents[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDDecodeInfo{}, err
	}
	return info, nil
}

type libopusDREDProcessInfo struct {
	availableSamples  int
	dredEndSamples    int
	processRet        int
	processStage      int
	nbLatents         int
	dredOffset        int
	secondProcessRet  int
	secondStage       int
	cloneProcessRet   int
	cloneStage        int
	secondStateHash   uint32
	secondLatentHash  uint32
	secondFeatureHash uint32
	cloneStateHash    uint32
	cloneLatentHash   uint32
	cloneFeatureHash  uint32
	state             [internaldred.StateDim]float32
	latents           []float32
	features          []float32
}

type libopusDREDRecoveryWindowInfo struct {
	availableSamples         int
	dredEndSamples           int
	processRet               int
	processStage             int
	nbLatents                int
	dredOffset               int
	featuresPerFrame         int
	neededFeatureFrames      int
	featureOffsetBase        int
	maxFeatureIndex          int
	recoverableFeatureFrames int
	missingPositiveFrames    int
	featureOffsets           []int
}

var (
	libopusDREDModelBlobHelper      libopustest.HelperCache
	libopusDREDProcessHelper        libopustest.HelperCache
	libopusDREDRecoveryWindowHelper libopustest.HelperCache
)

func getLibopusDREDModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDModelBlobHelper, "libopus_dred_model_blob.c", "gopus_libopus_dred_model_blob", true)
}

func getLibopusDREDProcessHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDProcessHelper, "libopus_dred_process_info.c", "gopus_libopus_dred_process", true)
}

func getLibopusDREDRecoveryWindowHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDRecoveryWindowHelper, "libopus_dred_recovery_window_info.c", "gopus_libopus_dred_recovery_window", true)
}

func probeLibopusDREDModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run dred model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDREDProcess(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDProcessInfo, error) {
	binPath, err := getLibopusDREDProcessHelperPath()
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDProcessInfo{}, fmt.Errorf("run dred process helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred process", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	info := libopusDREDProcessInfo{
		availableSamples:  int(reader.I32()),
		dredEndSamples:    int(reader.I32()),
		processRet:        int(reader.I32()),
		processStage:      int(reader.I32()),
		nbLatents:         int(reader.I32()),
		dredOffset:        int(reader.I32()),
		secondProcessRet:  int(reader.I32()),
		secondStage:       int(reader.I32()),
		cloneProcessRet:   int(reader.I32()),
		cloneStage:        int(reader.I32()),
		secondStateHash:   reader.U32(),
		secondLatentHash:  reader.U32(),
		secondFeatureHash: reader.U32(),
		cloneStateHash:    reader.U32(),
		cloneLatentHash:   reader.U32(),
		cloneFeatureHash:  reader.U32(),
	}

	for i := range info.state {
		info.state[i] = reader.Float32()
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		info.latents[i] = reader.Float32()
	}

	featureValues := info.nbLatents * 4 * internaldred.NumFeatures
	info.features = make([]float32, featureValues)
	for i := 0; i < featureValues; i++ {
		info.features[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDProcessInfo{}, err
	}
	return info, nil
}

func probeLibopusDREDRecoveryWindow(packet []byte, maxDREDSamples, sampleRate, frameSizeSamples, decodeOffsetSamples int, blend bool) (libopusDREDRecoveryWindowInfo, error) {
	binPath, err := getLibopusDREDRecoveryWindowHelperPath()
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	var blendFlag uint32
	if blend {
		blendFlag = 1
	}
	payload := libopustest.NewOraclePayload(
		libopusDREDParseInputMagic,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(int32(decodeOffsetSamples)),
		blendFlag,
		uint32(len(packet)),
	)
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("run dred recovery helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred recovery", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	info := libopusDREDRecoveryWindowInfo{
		availableSamples:         int(reader.I32()),
		dredEndSamples:           int(reader.I32()),
		processRet:               int(reader.I32()),
		processStage:             int(reader.I32()),
		nbLatents:                int(reader.I32()),
		dredOffset:               int(reader.I32()),
		featuresPerFrame:         int(reader.I32()),
		neededFeatureFrames:      int(reader.I32()),
		featureOffsetBase:        int(reader.I32()),
		maxFeatureIndex:          int(reader.I32()),
		recoverableFeatureFrames: int(reader.I32()),
		missingPositiveFrames:    int(reader.I32()),
	}

	info.featureOffsets = make([]int, info.neededFeatureFrames)
	for i := range info.featureOffsets {
		info.featureOffsets[i] = int(reader.I32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}
	return info, nil
}

func TestDREDControlModelBlobsMatchPinnedLibopusDigests(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name        string
		probe       func() ([]byte, error)
		validate    func(*dnnblob.Blob) error
		wantRecords int
		wantBytes   int
		wantSHA256  string
	}{
		{
			name:        "encoder",
			probe:       probeLibopusEncoderNeuralModelBlob,
			validate:    func(blob *dnnblob.Blob) error { return blob.ValidateEncoderControl() },
			wantRecords: 133,
			wantBytes:   317696,
			wantSHA256:  "164bfeab6f7d20932b006e9cdcaa1d26f2fd2f02d5e33cafee4fbb580be4c0b2",
		},
		{
			name:        "decoder",
			probe:       probeLibopusDecoderNeuralModelBlob,
			validate:    func(blob *dnnblob.Blob) error { return blob.ValidateDecoderControl(false) },
			wantRecords: 114,
			wantBytes:   1432768,
			wantSHA256:  "42d935199c9d86519ccd7be3bc8df0090553319d3e23b2e93cdfdf7a06c9ab95",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.probe()
			if err != nil {
				libopustest.HelperUnavailable(t, tc.name+" control model", err)
			}
			blob, err := dnnblob.Clone(raw)
			if err != nil {
				t.Fatalf("Clone(%s control model blob) error: %v", tc.name, err)
			}
			if err := tc.validate(blob); err != nil {
				t.Fatalf("Validate(%s control model blob) error: %v", tc.name, err)
			}
			if len(blob.Records) != tc.wantRecords {
				t.Fatalf("%s DRED control model records=%d want %d", tc.name, len(blob.Records), tc.wantRecords)
			}
			if len(raw) != tc.wantBytes {
				t.Fatalf("%s DRED control model bytes=%d want %d", tc.name, len(raw), tc.wantBytes)
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])
			if got != tc.wantSHA256 {
				t.Fatalf("%s DRED control model blob sha256=%s want %s", tc.name, got, tc.wantSHA256)
			}
		})
	}
}

func addDREDPayloadFuzzSeed(f *testing.F, extensions []packetExtensionData) {
	frames := [][]byte{{0x11, 0x22, 0x33}, {0x44, 0x55}}
	packet := make([]byte, 128)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err == nil {
		f.Add(append([]byte(nil), packet[:n]...))
	}
}

func FuzzFindDREDPayload_NoPanic(f *testing.F) {
	validPayload := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 1, Data: append([]byte{'D', dred.ExperimentalVersion}, validPayload...)},
	})
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0xaa}},
	})
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0x10}},
		{ID: dred.ExtensionID, Frame: 0, Data: append([]byte{'D', dred.ExperimentalVersion}, validPayload...)},
	})
	f.Add([]byte{0x7c, 0xc1, 0x03, 0x02, 0xff})
	f.Add([]byte{0xff, 0xff, 0x00, 'D', dred.ExperimentalVersion})
	f.Add([]byte{GenerateTOC(31, false, 3), 0xc1, 0x02, 0x01})

	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > 4096 {
			packet = packet[:4096]
		}
		payload, frameOffset, ok, err := findDREDPayload(packet)
		if err != nil {
			return
		}
		if !ok {
			if payload != nil || frameOffset != 0 {
				t.Fatalf("findDREDPayload returned stale data with ok=false: payload=%x frameOffset=%d", payload, frameOffset)
			}
			return
		}
		if frameOffset < 0 {
			t.Fatalf("negative DRED frame offset: %d", frameOffset)
		}
		if len(payload) > len(packet) {
			t.Fatalf("DRED payload length=%d exceeds packet length=%d", len(payload), len(packet))
		}
	})
}

func TestFindDREDPayload(t *testing.T) {
	frames := [][]byte{
		{0x11, 0x22, 0x33},
		{0x44, 0x55, 0x66},
	}
	wantPayload := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x12, 0x34}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 1, Data: append([]byte{'D', dred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok {
		t.Fatal("findDREDPayload ok=false want true")
	}
	if frameOffset != 8 {
		t.Fatalf("frameOffset=%d want 8", frameOffset)
	}
	if !bytes.Equal(payload, wantPayload) {
		t.Fatalf("payload=%x want %x", payload, wantPayload)
	}
}

func TestFindDREDPayloadSkipsWrongExperimentalHeader(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0xaa}},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if ok || payload != nil || frameOffset != 0 {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (nil,0,false)", payload, frameOffset, ok)
	}
}

func TestFindDREDPayloadSkipsEarlierInvalidDREDExtension(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	wantPayload := []byte{0xaa, 0xbb, 0xcc}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0x10}},
		{ID: dred.ExtensionID, Frame: 0, Data: append([]byte{'D', dred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok || frameOffset != 0 || !bytes.Equal(payload, wantPayload) {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (%x,0,true)", payload, frameOffset, ok, wantPayload)
	}
}

func TestFindDREDPayloadKeepsShortExperimentalPayload(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	wantPayload := []byte{0xaa, 0xbb}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: append([]byte{'D', dred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok || frameOffset != 0 || !bytes.Equal(payload, wantPayload) {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (%x,0,true)", payload, frameOffset, ok, wantPayload)
	}
}

func TestStandaloneDREDProcessMatchesLibopusOnRealPacket(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	want, err := probeLibopusDREDProcess(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred process", err)
	}
	if want.availableSamples < 0 {
		t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
	}
	if want.processRet != 0 {
		t.Fatalf("libopus dred process returned error %d", want.processRet)
	}
	if want.processStage != 2 {
		t.Fatalf("libopus processStage=%d want 2", want.processStage)
	}

	dec := NewDREDDecoder()
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		for _, name := range dnnblob.RequiredDREDDecoderRecordNames() {
			if !blob.HasRecord(name) {
				t.Fatalf("ValidateDREDDecoderControl(real model) error: %v (missing record %q)", err, name)
			}
		}
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	if _, err := rdovae.LoadDecoder(blob); err != nil {
		t.Fatalf("rdovae.LoadDecoder(real model) error: %v", err)
	}
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if available != want.availableSamples {
		t.Fatalf("available=%d want %d", available, want.availableSamples)
	}
	if dredEnd != want.dredEndSamples {
		t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
	}
	if got := dred.LatentCount(); got != want.nbLatents {
		t.Fatalf("LatentCount()=%d want %d", got, want.nbLatents)
	}
	state := make([]float32, len(want.state))
	if n := dred.FillState(state); n != len(want.state) {
		t.Fatalf("FillState count=%d want %d", n, len(want.state))
	}
	assertFloat32BitsEqual(t, state, want.state[:], "state")
	latents := make([]float32, want.nbLatents*internaldred.LatentStride)
	if n := dred.FillLatents(latents); n != len(latents) {
		t.Fatalf("FillLatents count=%d want %d", n, len(latents))
	}
	assertFloat32BitsEqual(t, latents, want.latents, "latents")

	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if got := dred.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := dred.FeatureCount(); got != len(want.features) {
		t.Fatalf("FeatureCount()=%d want %d", got, len(want.features))
	}
	features := make([]float32, dred.FeatureCount())
	if n := dred.FillFeatures(features); n != len(features) {
		t.Fatalf("FillFeatures count=%d want %d", n, len(features))
	}
	assertFloat32BitsEqual(t, features, want.features, "features")
}

func TestStandaloneDREDProcessMatchesLibopusOnQualityPacket(t *testing.T) {
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	_, packets := encodeDREDQualityPackets(t, encoderBlob)

	expected := 0
	haveExpected := false
	for frame, packet := range packets {
		if !dredQualityPacketDelivered(frame) {
			continue
		}
		if haveExpected {
			missing := frame - expected
			if missing > 0 {
				want, err := probeLibopusDREDProcess(packet, missing*dredQualityFrameSize, dredQualitySampleRate)
				if err != nil {
					libopustest.HelperUnavailable(t, "quality dred process", err)
				}
				if want.availableSamples <= 0 || want.processRet != 0 || want.processStage != 2 {
					t.Fatalf("libopus quality DRED process=(available=%d ret=%d stage=%d)", want.availableSamples, want.processRet, want.processStage)
				}

				dec := NewDREDDecoder()
				if err := dec.SetDNNBlob(modelBlob); err != nil {
					t.Fatalf("SetDNNBlob(real model): %v", err)
				}
				dred := NewDRED()
				available, dredEnd, err := dec.Parse(dred, packet, missing*dredQualityFrameSize, dredQualitySampleRate, true)
				if err != nil {
					t.Fatalf("Parse quality packet frame=%d: %v", frame, err)
				}
				if available != want.availableSamples || dredEnd != want.dredEndSamples {
					t.Fatalf("quality availability=(%d,%d) want (%d,%d)", available, dredEnd, want.availableSamples, want.dredEndSamples)
				}
				if err := dec.Process(dred, dred); err != nil {
					t.Fatalf("Process quality packet frame=%d: %v", frame, err)
				}
				features := make([]float32, dred.FeatureCount())
				if n := dred.FillFeatures(features); n != len(features) {
					t.Fatalf("FillFeatures count=%d want %d", n, len(features))
				}
				assertFloat32BitsEqual(t, features, want.features, "quality features")
				return
			}
		}
		expected = frame + 1
		haveExpected = true
	}
	t.Fatal("no delivered quality packet with preceding loss")
}

func TestStandaloneDREDProcessLifecycleMatchesLibopusOnRealPacket(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	want, err := probeLibopusDREDProcess(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred process", err)
	}
	if want.processRet != 0 || want.processStage != 2 {
		t.Fatalf("libopus process=(ret=%d, stage=%d) want (0,2)", want.processRet, want.processStage)
	}
	if want.secondProcessRet != 0 || want.secondStage != 2 {
		t.Fatalf("libopus second process=(ret=%d, stage=%d) want (0,2)", want.secondProcessRet, want.secondStage)
	}
	if want.cloneProcessRet != 0 || want.cloneStage != 2 {
		t.Fatalf("libopus clone process=(ret=%d, stage=%d) want (0,2)", want.cloneProcessRet, want.cloneStage)
	}

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	if _, _, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("first Process error: %v", err)
	}
	firstState := append([]float32(nil), dred.decoded.State[:]...)
	firstLatents := append([]float32(nil), dred.decoded.Latents[:dred.decoded.NbLatents*internaldred.LatentStride]...)
	firstFeatures := append([]float32(nil), dred.decoded.Features[:dred.decoded.NbLatents*4*internaldred.NumFeatures]...)
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("second Process error: %v", err)
	}
	if got := dred.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := dred.decoded.NbLatents; got != want.nbLatents {
		t.Fatalf("second NbLatents=%d want %d", got, want.nbLatents)
	}
	secondLatents := dred.decoded.Latents[:dred.decoded.NbLatents*internaldred.LatentStride]
	secondFeatures := dred.decoded.Features[:dred.decoded.NbLatents*4*internaldred.NumFeatures]
	assertLibopusDREDFloat32Hash(t, "second state", dred.decoded.State[:], want.secondStateHash)
	assertLibopusDREDFloat32Hash(t, "second latents", secondLatents, want.secondLatentHash)
	assertLibopusDREDFloat32Hash(t, "second features", secondFeatures, want.secondFeatureHash)
	assertFloat32BitsEqual(t, dred.decoded.State[:], firstState, "second state")
	assertFloat32BitsEqual(t, secondLatents, firstLatents, "second latents")
	assertFloat32BitsEqual(t, secondFeatures, firstFeatures, "second features")

	clone := NewDRED()
	if err := dec.Process(dred, clone); err != nil {
		t.Fatalf("clone Process error: %v", err)
	}
	if got := clone.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("clone ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := clone.decoded.NbLatents; got != want.nbLatents {
		t.Fatalf("clone NbLatents=%d want %d", got, want.nbLatents)
	}
	cloneLatents := clone.decoded.Latents[:clone.decoded.NbLatents*internaldred.LatentStride]
	cloneFeatures := clone.decoded.Features[:clone.decoded.NbLatents*4*internaldred.NumFeatures]
	assertLibopusDREDFloat32Hash(t, "clone state", clone.decoded.State[:], want.cloneStateHash)
	assertLibopusDREDFloat32Hash(t, "clone latents", cloneLatents, want.cloneLatentHash)
	assertLibopusDREDFloat32Hash(t, "clone features", cloneFeatures, want.cloneFeatureHash)
	assertFloat32BitsEqual(t, clone.decoded.State[:], firstState, "clone state")
	assertFloat32BitsEqual(t, cloneLatents, firstLatents, "clone latents")
	assertFloat32BitsEqual(t, cloneFeatures, firstFeatures, "clone features")
}

func assertLibopusDREDFloat32Hash(t *testing.T, label string, data []float32, want uint32) {
	t.Helper()
	if got := libopusDREDFloat32Hash(data); got != want {
		t.Fatalf("%s hash=0x%08x want 0x%08x", label, got, want)
	}
}

func libopusDREDFloat32Hash(data []float32) uint32 {
	h := uint32(2166136261)
	for _, sample := range data {
		h ^= math.Float32bits(sample)
		h *= 16777619
	}
	return h
}

func TestStandaloneDREDRecoveryQueueMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	type queueCase struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}
	scenarios := []struct {
		name           string
		decoderRate    int
		maxDREDSamples int
		sampleRate     int
		tests          []queueCase
	}{
		{
			name:           "48k",
			decoderRate:    48000,
			maxDREDSamples: packetInfo.maxDREDSamples,
			sampleRate:     packetInfo.sampleRate,
			tests: []queueCase{
				{name: "blend_off_negative_full_frame", decodeOffsetSamples: -960, frameSizeSamples: 960, blend: false},
				{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
				{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 480, blend: false},
				{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 2880, blend: false},
				{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: true},
				{name: "blend_off_late_recovery", decodeOffsetSamples: 3840, frameSizeSamples: 960, blend: false},
				{name: "blend_off_beyond_payload", decodeOffsetSamples: 9600, frameSizeSamples: 960, blend: false},
				{name: "blend_on_half_frame", decodeOffsetSamples: 960, frameSizeSamples: 480, blend: true},
				{name: "blend_on_late_10ms", decodeOffsetSamples: 1920, frameSizeSamples: 480, blend: true},
			},
		},
		{
			name:        "16k",
			decoderRate: 16000,
			tests: []queueCase{
				{name: "blend_off_negative_full_frame", decodeOffsetSamples: -320, frameSizeSamples: 320, blend: false},
				{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: false},
				{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 160, blend: false},
				{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
				{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: true},
				{name: "blend_off_late_recovery", decodeOffsetSamples: 1280, frameSizeSamples: 320, blend: false},
				{name: "blend_on_half_frame", decodeOffsetSamples: 320, frameSizeSamples: 160, blend: true},
			},
		},
	}
	for i := range scenarios {
		if scenarios[i].maxDREDSamples == 0 || scenarios[i].sampleRate == 0 {
			scenarios[i].maxDREDSamples, scenarios[i].sampleRate = libopusDREDRequestForDecoder(packetInfo, scenarios[i].decoderRate)
		}
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			dec := NewDREDDecoder()
			if err := dec.SetDNNBlob(modelBlob); err != nil {
				t.Fatalf("SetDNNBlob(real model) error: %v", err)
			}

			dred := NewDRED()
			available, dredEnd, err := dec.Parse(dred, packetInfo.packet, scenario.maxDREDSamples, scenario.sampleRate, true)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if err := dec.Process(dred, dred); err != nil {
				t.Fatalf("Process error: %v", err)
			}
			result := dred.Result(scenario.maxDREDSamples, scenario.sampleRate)

			for _, tc := range scenario.tests {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, scenario.maxDREDSamples, scenario.sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
					if err != nil {
						libopustest.HelperUnavailable(t, "dred recovery", err)
					}
					if want.availableSamples != available {
						t.Fatalf("available=%d want %d", available, want.availableSamples)
					}
					if want.dredEndSamples != dredEnd {
						t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
					}

					var plc lpcnetplc.State
					allocs := testing.AllocsPerRun(1000, func() {
						if tc.blend {
							plc.MarkConcealed()
						} else {
							plc.MarkUpdated()
						}
						internaldred.QueueProcessedFeatures(&plc, result, &dred.decoded, tc.decodeOffsetSamples, tc.frameSizeSamples)
					})
					if allocs != 0 {
						t.Fatalf("AllocsPerRun=%v want 0", allocs)
					}

					if tc.blend {
						plc.MarkConcealed()
					} else {
						plc.MarkUpdated()
					}
					got := internaldred.QueueProcessedFeatures(&plc, result, &dred.decoded, tc.decodeOffsetSamples, tc.frameSizeSamples)
					if got.FeaturesPerFrame != want.featuresPerFrame ||
						got.NeededFeatureFrames != want.neededFeatureFrames ||
						got.FeatureOffsetBase != want.featureOffsetBase ||
						got.MaxFeatureIndex != want.maxFeatureIndex ||
						got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
						got.MissingPositiveFrames != want.missingPositiveFrames {
						t.Fatalf("QueueProcessedFeatures window=%+v want %+v", got, want)
					}
					if plc.FECFillPos() != want.recoverableFeatureFrames {
						t.Fatalf("FECFillPos()=%d want %d", plc.FECFillPos(), want.recoverableFeatureFrames)
					}
					if plc.FECSkip() != want.missingPositiveFrames {
						t.Fatalf("FECSkip()=%d want %d", plc.FECSkip(), want.missingPositiveFrames)
					}

					queued := 0
					var gotFeatures [lpcnetplc.NumFeatures]float32
					for _, featureOffset := range want.featureOffsets {
						if featureOffset < 0 || featureOffset > want.maxFeatureIndex {
							continue
						}
						if n := plc.FillQueuedFeatures(queued, gotFeatures[:]); n != lpcnetplc.NumFeatures {
							t.Fatalf("FillQueuedFeatures(%d) count=%d want %d", queued, n, lpcnetplc.NumFeatures)
						}
						start := featureOffset * internaldred.NumFeatures
						assertFloat32BitsEqual(t, gotFeatures[:], dred.decoded.Features[start:start+internaldred.NumFeatures], "queued features")
						queued++
					}
					if queued != plc.FECFillPos() {
						t.Fatalf("queued=%d want fill=%d", queued, plc.FECFillPos())
					}
				})
			}
		})
	}
}

func TestStandaloneDREDRecoveryWindowMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}

	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}{
		{name: "blend_off_negative_offset", decodeOffsetSamples: -480, frameSizeSamples: 960, blend: false},
		{name: "blend_off_negative_full_frame", decodeOffsetSamples: -960, frameSizeSamples: 960, blend: false},
		{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
		{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 480, blend: false},
		{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 2880, blend: false},
		{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: true},
		{name: "blend_off_late_recovery", decodeOffsetSamples: 3840, frameSizeSamples: 960, blend: false},
		{name: "blend_off_beyond_payload", decodeOffsetSamples: 9600, frameSizeSamples: 960, blend: false},
		{name: "blend_on_half_frame", decodeOffsetSamples: 960, frameSizeSamples: 480, blend: true},
		{name: "blend_on_late_10ms", decodeOffsetSamples: 1920, frameSizeSamples: 480, blend: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred recovery", err)
			}
			if want.availableSamples != available {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if want.dredEndSamples != dredEnd {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}
			if want.processRet != 0 {
				t.Fatalf("libopus processRet=%d want 0", want.processRet)
			}
			if want.processStage != 2 {
				t.Fatalf("libopus processStage=%d want 2", want.processStage)
			}

			initFrames := 2
			if tc.blend {
				initFrames = 0
			}
			got := dred.FeatureWindow(packetInfo.maxDREDSamples, packetInfo.sampleRate, tc.decodeOffsetSamples, tc.frameSizeSamples, initFrames)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("FeatureWindow=%+v want %+v", got, want)
			}

			offsets := make([]int32, got.NeededFeatureFrames)
			if n := got.FillFeatureOffsets(offsets); n != len(want.featureOffsets) {
				t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(want.featureOffsets))
			}
			for i, wantOffset := range want.featureOffsets {
				if offsets[i] != int32(wantOffset) {
					t.Fatalf("featureOffsets[%d]=%d want %d", i, offsets[i], wantOffset)
				}
			}
		})
	}
}

func TestStandaloneDREDRecoveryWindow16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	maxDREDSamples, sampleRate := libopusDREDRequestForDecoder(packetInfo, 16000)

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}

	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, maxDREDSamples, sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}{
		{name: "blend_off_negative_offset", decodeOffsetSamples: -160, frameSizeSamples: 320, blend: false},
		{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: false},
		{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 160, blend: false},
		{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
		{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: true},
		{name: "blend_off_late_recovery", decodeOffsetSamples: 1280, frameSizeSamples: 320, blend: false},
		{name: "blend_on_half_frame", decodeOffsetSamples: 320, frameSizeSamples: 160, blend: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, maxDREDSamples, sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred recovery", err)
			}
			if want.availableSamples != available {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if want.dredEndSamples != dredEnd {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}
			if want.processRet != 0 {
				t.Fatalf("libopus processRet=%d want 0", want.processRet)
			}
			if want.processStage != 2 {
				t.Fatalf("libopus processStage=%d want 2", want.processStage)
			}

			initFrames := 2
			if tc.blend {
				initFrames = 0
			}
			got := dred.FeatureWindow(maxDREDSamples, sampleRate, tc.decodeOffsetSamples, tc.frameSizeSamples, initFrames)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("FeatureWindow=%+v want %+v", got, want)
			}

			offsets := make([]int32, got.NeededFeatureFrames)
			if n := got.FillFeatureOffsets(offsets); n != len(want.featureOffsets) {
				t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(want.featureOffsets))
			}
			for i, wantOffset := range want.featureOffsets {
				if offsets[i] != int32(wantOffset) {
					t.Fatalf("featureOffsets[%d]=%d want %d", i, offsets[i], wantOffset)
				}
			}
		})
	}
}
