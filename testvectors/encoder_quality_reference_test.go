package testvectors

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

type encoderQualityReferenceSettings struct {
	mode      encoder.Mode
	bandwidth types.Bandwidth
	frameSize int
	channels  int
	bitrate   int
}

type encoderQualityReference struct {
	packets     [][]byte
	finalRanges []uint32
	quality     packetQualityResult
	identity    string
	pcmSHA256   string
}

func runPairedLibopusQualityReference(settings encoderQualityReferenceSettings, signal []float32) (encoderQualityReference, error) {
	if settings.frameSize <= 0 || settings.channels <= 0 || settings.bitrate <= 0 {
		return encoderQualityReference{}, fmt.Errorf("invalid paired quality settings: frame=%d channels=%d bitrate=%d", settings.frameSize, settings.channels, settings.bitrate)
	}
	if len(signal) == 0 || len(signal)%(settings.frameSize*settings.channels) != 0 {
		return encoderQualityReference{}, fmt.Errorf("PCM length %d is not a nonempty multiple of frame samples %d", len(signal), settings.frameSize*settings.channels)
	}

	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return encoderQualityReference{}, fmt.Errorf("resolve paired libopus variant: %w", err)
	}
	opusDemo, err := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if err != nil {
		return encoderQualityReference{}, fmt.Errorf("find validated paired opus_demo: %w", err)
	}
	provenance, ok := libopustooling.LibopusBuildProvenanceForTool(opusDemo)
	if !ok {
		return encoderQualityReference{}, fmt.Errorf("validated opus_demo %q has no build provenance stamp", opusDemo)
	}
	if provenance.LibopusVersion != libopustooling.DefaultVersion {
		return encoderQualityReference{}, fmt.Errorf("paired opus_demo version=%q want %q", provenance.LibopusVersion, libopustooling.DefaultVersion)
	}

	app, err := modeToOpusDemoApp(fixtureModeName(settings.mode))
	if err != nil {
		return encoderQualityReference{}, err
	}
	bwArg, err := bandwidthToOpusDemoArg(fixtureBandwidthName(settings.bandwidth))
	if err != nil {
		return encoderQualityReference{}, err
	}
	frameArg, err := frameSizeSamplesToArg(settings.frameSize)
	if err != nil {
		return encoderQualityReference{}, err
	}

	tmpDir, err := os.MkdirTemp("", "gopus-paired-quality-reference-*")
	if err != nil {
		return encoderQualityReference{}, fmt.Errorf("create paired quality temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	rawPath := filepath.Join(tmpDir, "input.f32")
	bitPath := filepath.Join(tmpDir, "reference.bit")
	if err := writeFloat32LEFile(rawPath, signal); err != nil {
		return encoderQualityReference{}, fmt.Errorf("write paired quality PCM: %w", err)
	}
	packets, finalRanges, err := runOpusDemoCELTEncode(opusDemo, app, bwArg, frameArg, settings.bitrate, settings.channels, rawPath, bitPath)
	if err != nil {
		return encoderQualityReference{}, fmt.Errorf("run paired opus_demo: %w", err)
	}
	if len(packets) == 0 || len(packets) != len(finalRanges) {
		return encoderQualityReference{}, fmt.Errorf("paired opus_demo produced %d packets and %d final ranges", len(packets), len(finalRanges))
	}
	wantFrames := len(signal)/(settings.frameSize*settings.channels) + 1
	if len(packets) != wantFrames {
		return encoderQualityReference{}, fmt.Errorf("paired opus_demo flush cadence produced %d frames, want %d signal frames plus one flush frame", len(packets), wantFrames)
	}

	quality, err := qualityFromPacketsLibopusReferenceDetailed(packets, signal, settings.channels, settings.frameSize)
	if err != nil {
		return encoderQualityReference{}, fmt.Errorf("measure paired libopus quality: %w", err)
	}
	identity := fmt.Sprintf("variant=%s tool=%s libopus=%s stamp_sha256=%s target=%s CFLAGS=%q", variant, opusDemo, provenance.LibopusVersion, provenance.LibopusBuildStampSHA256, provenance.CCTarget, provenance.CFLAGS)
	return encoderQualityReference{
		packets:     packets,
		finalRanges: finalRanges,
		quality:     quality,
		identity:    identity,
		pcmSHA256:   testsignal.HashFloat32LE(signal),
	}, nil
}
