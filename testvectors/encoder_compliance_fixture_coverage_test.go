package testvectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	encoderComplianceRefQDriftToleranceDB = 0.35
	updateEncoderComplianceRefQEnv        = "GOPUS_UPDATE_ENCODER_REF_Q"
)

func TestEncoderComplianceReferenceFixtureCoverage(t *testing.T) {
	fixture, err := loadEncoderComplianceReferenceQFixture()
	if err != nil {
		t.Fatalf("load encoder compliance reference fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported encoder compliance reference fixture version %d", fixture.Version)
	}

	seen := make(map[string]struct{}, len(fixture.Cases))
	expectedOrder := make(map[string]int, len(encoderComplianceSummaryCases()))
	for idx, tc := range encoderComplianceSummaryCases() {
		orderKey := fmt.Sprintf("%d/%d/%d/%d/%d", tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
		expectedOrder[orderKey] = idx
	}
	prevOrderIndex := -1
	for i, row := range fixture.Cases {
		mode, err := parseFixtureMode(row.Mode)
		if err != nil {
			t.Fatalf("fixture row %d has invalid mode %q: %v", i, row.Mode, err)
		}
		bw, err := parseFixtureBandwidth(row.Bandwidth)
		if err != nil {
			t.Fatalf("fixture row %d has invalid bandwidth %q: %v", i, row.Bandwidth, err)
		}
		key := fmt.Sprintf("%d/%d/%d/%d/%d", mode, bw, row.FrameSize, row.Channels, row.Bitrate)
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate reference fixture row for key %s", key)
		}
		seen[key] = struct{}{}
		orderIndex, ok := expectedOrder[key]
		if !ok {
			t.Fatalf("unexpected reference fixture row not in summary matrix: mode=%s bandwidth=%s frame=%d channels=%d bitrate=%d",
				row.Mode, row.Bandwidth, row.FrameSize, row.Channels, row.Bitrate)
		}
		if i > 0 && orderIndex < prevOrderIndex {
			t.Fatalf("reference fixture out of canonical summary order at row %d: idx=%d prev=%d", i, orderIndex, prevOrderIndex)
		}
		prevOrderIndex = orderIndex
		if math.IsNaN(row.LibQ) || math.IsInf(row.LibQ, 0) {
			t.Fatalf("fixture row %d has invalid lib_q %v", i, row.LibQ)
		}
		if rounded := math.Round(row.LibQ*100) / 100; math.Abs(row.LibQ-rounded) > 1e-9 {
			t.Fatalf("fixture row %d has non-canonical lib_q precision %.10f (expected 2 decimals)", i, row.LibQ)
		}
	}

	var missing []string
	for _, tc := range encoderComplianceSummaryCases() {
		if _, ok := lookupEncoderComplianceReferenceQ(tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate); ok {
			continue
		}
		if _, ok := findLongFrameFixtureCase(tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate); ok {
			continue
		}
		missing = append(missing, tc.name)
	}
	if len(missing) > 0 {
		t.Fatalf("missing libopus reference fixture coverage for summary cases: %s", strings.Join(missing, ", "))
	}
}

func TestLongFrameReferenceFixtureCoverage(t *testing.T) {
	fixture, err := loadLongFrameFixtureCached()
	if err != nil {
		t.Fatalf("load long-frame fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported long-frame fixture version %d", fixture.Version)
	}

	for _, target := range longFrameFixtureTargets() {
		c, ok := findLongFrameFixtureCase(target.Mode, target.Bandwidth, target.FrameSize, target.Channels, target.Bitrate)
		if !ok {
			t.Fatalf("missing long-frame fixture target %s", target.Name)
		}
		if len(c.Packets) == 0 {
			t.Fatalf("%s: fixture has no packets", target.Name)
		}
		if math.IsNaN(c.LibQ) || math.IsInf(c.LibQ, 0) {
			t.Fatalf("%s: fixture has invalid lib_q=%v", target.Name, c.LibQ)
		}
	}
}

func TestLongFrameReferenceFixtureHonestyWithLiveOpusdec(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not available; skipping live fixture honesty validation")
	}

	for _, target := range longFrameFixtureTargets() {
		target := target
		t.Run(target.Name, func(t *testing.T) {
			c, ok := findLongFrameFixtureCase(target.Mode, target.Bandwidth, target.FrameSize, target.Channels, target.Bitrate)
			if !ok {
				t.Fatalf("missing fixture case")
			}
			packets, err := decodeFixturePackets(c.Packets)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			numFrames := 48000 / c.FrameSize
			totalSamples := numFrames * c.FrameSize * c.Channels
			original := generateEncoderTestSignal(totalSamples, c.Channels)

			q, err := computeComplianceQualityFromPacketsWithLiveOpusdec(packets, original, c.Channels, c.FrameSize)
			if err != nil {
				if strings.Contains(err.Error(), "provenance") {
					t.Skipf("opusdec blocked by macOS provenance: %v", err)
				}
				t.Fatalf("compute live opusdec quality: %v", err)
			}
			if math.Abs(q-c.LibQ) > encoderComplianceRefQDriftToleranceDB {
				t.Fatalf("fixture libQ drift for %s: live=%.2f fixture=%.2f", target.Name, q, c.LibQ)
			}
		})
	}
}

func TestEncoderComplianceReferenceQFixtureHonestyWithLiveOpusdec(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not available; skipping encoder compliance ref-q fixture honesty validation")
	}
	opusDemo, ok := getFixtureOpusDemoPathForEncoder()
	if !ok {
		t.Skip("tmp_check opus_demo not found; skipping encoder compliance ref-q fixture honesty validation")
	}

	updateFixture := os.Getenv(updateEncoderComplianceRefQEnv) == "1"
	fixture, err := readEncoderComplianceReferenceQFixtureFile()
	if err != nil {
		t.Fatalf("load encoder compliance reference fixture: %v", err)
	}
	tmpDir, err := os.MkdirTemp("", "gopus-refq-fixture-honesty-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	updated := false
	for i := range fixture.Cases {
		row := fixture.Cases[i]
		liveQ, err := computeComplianceQualityFromLiveLibopusEncode(opusDemo, tmpDir, row)
		if err != nil {
			if strings.Contains(err.Error(), "provenance") {
				t.Skipf("opusdec blocked by macOS provenance: %v", err)
			}
			t.Fatalf("compute live opusdec quality for row %d: %v", i, err)
		}
		liveQRounded := math.Round(liveQ*100) / 100
		drift := math.Abs(liveQRounded - row.LibQ)
		if updateFixture {
			if drift > 1e-9 {
				fixture.Cases[i].LibQ = liveQRounded
				updated = true
			}
			t.Logf("row %d %s-%s-%d/%d/%d: live=%.2f fixture=%.2f drift=%.2f (update)",
				i, row.Mode, row.Bandwidth, row.FrameSize, row.Channels, row.Bitrate, liveQRounded, row.LibQ, drift)
			continue
		}
		if drift > encoderComplianceRefQDriftToleranceDB {
			t.Fatalf("ref-q fixture drift row %d (%s-%s-%d/%d/%d): live=%.2f fixture=%.2f drift=%.2f",
				i, row.Mode, row.Bandwidth, row.FrameSize, row.Channels, row.Bitrate, liveQRounded, row.LibQ, drift)
		}
	}

	if updateFixture {
		if !updated {
			t.Log("encoder compliance ref-q fixture already up to date")
			return
		}
		if err := writeEncoderComplianceReferenceQFixtureFile(fixture); err != nil {
			t.Fatalf("write encoder compliance reference fixture: %v", err)
		}
		t.Logf("updated %s from live opus_demo encode + opusdec decode measurements", encoderComplianceRefQFixturePath)
	}
}

func computeComplianceQualityFromLiveLibopusEncode(opusDemoPath, tmpDir string, row encoderComplianceRefQFixtureRow) (float64, error) {
	app, err := modeToOpusDemoApp(row.Mode)
	if err != nil {
		return 0, err
	}
	bwArg, err := bandwidthToOpusDemoArg(row.Bandwidth)
	if err != nil {
		return 0, err
	}
	frameArg, err := frameSizeSamplesToArg(row.FrameSize)
	if err != nil {
		return 0, err
	}

	numFrames := 48000 / row.FrameSize
	totalSamples := numFrames * row.FrameSize * row.Channels
	original := generateEncoderTestSignal(totalSamples, row.Channels)

	base := fmt.Sprintf("%s_%s_%d_%d_%d", row.Mode, row.Bandwidth, row.FrameSize, row.Channels, row.Bitrate)
	inputPath := filepath.Join(tmpDir, base+".f32")
	bitPath := filepath.Join(tmpDir, base+".bit")

	if err := writeFloat32LEFile(inputPath, original); err != nil {
		return 0, fmt.Errorf("write raw input: %w", err)
	}
	cmd := exec.Command(opusDemoPath,
		"-e", app, "48000", strconv.Itoa(row.Channels), strconv.Itoa(row.Bitrate),
		"-f32", "-cbr", "-complexity", "10", "-bandwidth", bwArg, "-framesize", frameArg,
		inputPath, bitPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("opus_demo encode failed: %v (%s)", err, out)
	}
	packets, _, err := parseOpusDemoEncodeBitstream(bitPath)
	if err != nil {
		return 0, fmt.Errorf("parse opus_demo bitstream: %w", err)
	}
	return computeComplianceQualityFromPacketsWithLiveOpusdec(packets, original, row.Channels, row.FrameSize)
}

func readEncoderComplianceReferenceQFixtureFile() (encoderComplianceRefQFixtureFile, error) {
	path := filepath.Join(encoderComplianceRefQFixturePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return encoderComplianceRefQFixtureFile{}, err
	}
	var fixture encoderComplianceRefQFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		return encoderComplianceRefQFixtureFile{}, err
	}
	return fixture, nil
}

func writeEncoderComplianceReferenceQFixtureFile(fixture encoderComplianceRefQFixtureFile) error {
	path := filepath.Join(encoderComplianceRefQFixturePath)
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func decodeCompliancePacketsWithLibopusReferenceOnly(packets [][]byte, channels, frameSize int) ([]float32, error) {
	decoded, helperErr := decodeWithLibopusReferencePacketsSingle(channels, frameSize, packets)
	if helperErr == nil {
		preSkip := OpusPreSkip * channels
		if len(decoded) > preSkip {
			decoded = decoded[preSkip:]
		}
		return decoded, nil
	}

	if checkOpusdecAvailableEncoder() {
		var oggBuf bytes.Buffer
		if err := writeOggOpusEncoder(&oggBuf, packets, channels, 48000, frameSize); err != nil {
			return nil, fmt.Errorf("write ogg opus: %w", err)
		}
		decoded, err := decodeWithOpusdec(oggBuf.Bytes())
		if err == nil {
			return decoded, nil
		}
		return nil, fmt.Errorf("libopus reference decode failed: direct helper failed (%v); opusdec decode failed: %w", helperErr, err)
	}

	return nil, fmt.Errorf("libopus reference decode failed: direct helper failed (%v); opusdec not available", helperErr)
}

func computeComplianceQualityFromPacketsWithLiveOpusdec(packets [][]byte, original []float32, channels, frameSize int) (float64, error) {
	decoded, err := decodeCompliancePacketsWithLibopusReferenceOnly(packets, channels, frameSize)
	if err != nil {
		return 0, err
	}
	if len(decoded) == 0 {
		return 0, fmt.Errorf("libopus reference decode produced no samples")
	}

	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}
	q, _ := ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, 960)
	return q, nil
}
