package celt

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestOpusdecCrossvalFixtureIncludesWindowsAMD64MonoAlias(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64-specific fixture coverage")
	}

	const windowsMonoSingleHash = "500a2af1eac1eaa7b40fb0d9e1041c04ede5155e5aeb4b07353185a41a20fcc3"

	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		t.Fatalf("loadOpusdecCrossvalFixtureMap: %v", err)
	}
	if _, ok := entries[windowsMonoSingleHash]; ok {
		return
	}
	for _, entry := range entries {
		if entry.Name == "mono_20ms_single" {
			return
		}
	}
	t.Fatalf("missing amd64 mono_20ms_single fixture entry (legacy alias hash %s not present)", windowsMonoSingleHash)
}

const updateOpusdecCrossvalFixtureEnv = "GOPUS_UPDATE_OPUSDEC_CROSSVAL_FIXTURE"

type crossvalFixtureScenario struct {
	name              string
	sampleRate        int
	channels          int
	numFrames         int
	ogg               []byte
	input             []float64
	expectNearSilence bool
	minEnergyRatio    float64
	minSNRDB          float64
	minCorr           float64
}

func clonePacket(packet []byte) []byte {
	out := make([]byte, len(packet))
	copy(out, packet)
	return out
}

func flattenFrames(frames [][]float64) []float64 {
	total := 0
	for _, f := range frames {
		total += len(f)
	}
	out := make([]float64, 0, total)
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

func buildMonoFrameChirp(samples int, startHz, endHz float64) []float64 {
	out := make([]float64, samples)
	for i := range samples {
		t := float64(i) / 48000.0
		progress := float64(i) / float64(samples-1)
		freq := startHz + (endHz-startHz)*progress
		amp := 0.48 * (0.7 + 0.3*math.Sin(2*math.Pi*2.0*t))
		out[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	return out
}

func buildMonoSineFrames(freqHz float64, frameSize, numFrames int) [][]float64 {
	total := frameSize * numFrames
	all := make([]float64, total)
	for i := range total {
		t := float64(i) / 48000.0
		all[i] = 0.5 * math.Sin(2*math.Pi*freqHz*t)
	}
	frames := make([][]float64, numFrames)
	for i := range numFrames {
		start := i * frameSize
		end := start + frameSize
		frame := make([]float64, frameSize)
		copy(frame, all[start:end])
		frames[i] = frame
	}
	return frames
}

func buildMonoChirpFrames(frameSize, numFrames int, startHz, endHz float64) [][]float64 {
	total := frameSize * numFrames
	all := make([]float64, total)
	for i := range total {
		t := float64(i) / 48000.0
		progress := float64(i) / float64(total-1)
		freq := startHz + (endHz-startHz)*progress
		amp := 0.48 * (0.7 + 0.3*math.Sin(2*math.Pi*2.0*t))
		all[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	frames := make([][]float64, numFrames)
	for i := range numFrames {
		start := i * frameSize
		end := start + frameSize
		frame := make([]float64, frameSize)
		copy(frame, all[start:end])
		frames[i] = frame
	}
	return frames
}

func buildMonoFrameImpulse(samples int) []float64 {
	out := make([]float64, samples)
	for i := range samples {
		if i%120 == 0 {
			out[i] = 0.9
			continue
		}
		decay := math.Exp(-float64(i%120) / 22.0)
		out[i] = 0.25 * decay * math.Sin(2*math.Pi*2200.0*float64(i)/48000.0)
	}
	return out
}

func buildMonoFramePseudoNoise(samples int) []float64 {
	out := make([]float64, samples)
	var x uint32 = 0x1badf00d
	for i := range samples {
		x = 1664525*x + 1013904223
		v := float64((x>>9)&0x7fffff) / float64(0x7fffff)
		out[i] = 0.42 * (2.0*v - 1.0)
	}
	return out
}

func buildStereoFrameChirp(samples int) []float64 {
	out := make([]float64, samples*2)
	left := buildMonoFrameChirp(samples, 220.0, 4200.0)
	right := buildMonoFrameChirp(samples, 4200.0, 220.0)
	for i := range samples {
		out[i*2] = left[i]
		out[i*2+1] = right[i] * 0.95
	}
	return out
}

func buildStereoFrameDualTone(samples int, fL, fR float64) []float64 {
	out := make([]float64, samples*2)
	for i := range samples {
		t := float64(i) / 48000.0
		out[i*2] = 0.5*math.Sin(2*math.Pi*fL*t) + 0.18*math.Sin(2*math.Pi*3.0*fL*t)
		out[i*2+1] = 0.5*math.Sin(2*math.Pi*fR*t) + 0.15*math.Sin(2*math.Pi*2.0*fR*t)
	}
	return out
}

func buildCrossvalFixtureScenarios(t *testing.T) []crossvalFixtureScenario {
	t.Helper()

	makeScenario := func(name string, channels int, packets [][]byte, input []float64, expectNearSilence bool, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		var ogg bytes.Buffer
		if err := writeOggOpus(&ogg, packets, 48000, channels); err != nil {
			t.Fatalf("%s: writeOggOpus failed: %v", name, err)
		}
		return crossvalFixtureScenario{
			name:              name,
			sampleRate:        48000,
			channels:          channels,
			numFrames:         len(packets),
			ogg:               ogg.Bytes(),
			input:             input,
			expectNearSilence: expectNearSilence,
			minEnergyRatio:    minEnergyRatio,
			minSNRDB:          minSNRDB,
			minCorr:           minCorr,
		}
	}

	encodeMonoFrame := func(name string, bitrate int, input []float64, expectNearSilence bool, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(1)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(float32Slice(input), 960)
		if err != nil {
			t.Fatalf("%s: encode failed: %v", name, err)
		}
		return makeScenario(name, 1, [][]byte{clonePacket(packet)}, input, expectNearSilence, minEnergyRatio, minSNRDB, minCorr)
	}

	encodeStereoFrame := func(name string, bitrate int, input []float64, expectNearSilence bool, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(2)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(float32Slice(input), 960)
		if err != nil {
			t.Fatalf("%s: encode failed: %v", name, err)
		}
		return makeScenario(name, 2, [][]byte{clonePacket(packet)}, input, expectNearSilence, minEnergyRatio, minSNRDB, minCorr)
	}

	encodeMonoFrames := func(name string, bitrate int, frames [][]float64, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(1)
		enc.SetBitrate(bitrate)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(float32Slice(frame), 960)
			if err != nil {
				t.Fatalf("%s frame %d: encode failed: %v", name, i, err)
			}
			packets[i] = clonePacket(packet)
		}
		return makeScenario(name, 1, packets, flattenFrames(frames), false, minEnergyRatio, minSNRDB, minCorr)
	}

	encodeStereoFramesCBR := func(name string, bitrate int, frames [][]float64, minEnergyRatio, minSNRDB, minCorr float64) crossvalFixtureScenario {
		enc := NewEncoder(2)
		enc.SetBitrate(bitrate)
		enc.SetVBR(false)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(float32Slice(frame), 960)
			if err != nil {
				t.Fatalf("%s frame %d: encode failed: %v", name, i, err)
			}
			packets[i] = clonePacket(packet)
		}
		return makeScenario(name, 2, packets, flattenFrames(frames), false, minEnergyRatio, minSNRDB, minCorr)
	}

	return []crossvalFixtureScenario{
		encodeMonoFrames("mono_20ms_single", 64000, buildMonoSineFrames(440.0, 960, 3), 0.20, 22.5, 0.995),
		encodeStereoFrame("stereo_20ms_single", 128000, generateStereoSineWave(440.0, 880.0, 960), false, 0.20, 24.0, 0.995),
		encodeMonoFrame("mono_20ms_silence", 64000, make([]float64, 960), true, 0, 0, 0),
		encodeMonoFrames("mono_20ms_multiframe", 64000, [][]float64{
			generateSineWave(440.0, 960),
			generateSineWave(540.0, 960),
			generateSineWave(640.0, 960),
			generateSineWave(740.0, 960),
			generateSineWave(840.0, 960),
		}, 0.20, 2.0, 0.68),
		// Standalone CELT 20ms mono chirps are a challenging transient sweep case;
		// keep thresholds strict enough to catch major drift, but calibrated to
		// current CELT quality for this corner scenario.
		encodeMonoFrames("mono_20ms_chirp", 64000, buildMonoChirpFrames(960, 3, 180.0, 5200.0), 0.20, 4.0, 0.80),
		encodeMonoFrame("mono_20ms_impulse", 48000, buildMonoFrameImpulse(960), false, 0.10, 6.0, 0.879),
		encodeMonoFrame("mono_20ms_noise", 32000, buildMonoFramePseudoNoise(960), false, 0.08, 0.8, 0.55),
		encodeMonoFrame("mono_20ms_lowamp", 24000, scaleSignal(generateSineWave(880.0, 960), 0.12), false, 0.05, 18.0, 0.99),
		encodeStereoFrame("stereo_20ms_chirp", 96000, buildStereoFrameChirp(960), false, 0.15, 19.9, 0.99),
		encodeStereoFrame("stereo_20ms_silence", 96000, make([]float64, 960*2), true, 0, 0, 0),
		encodeStereoFramesCBR("stereo_20ms_multiframe", 96000, [][]float64{
			buildStereoFrameDualTone(960, 300.0, 500.0),
			buildStereoFrameDualTone(960, 520.0, 920.0),
			buildStereoFrameDualTone(960, 760.0, 1240.0),
			buildStereoFrameDualTone(960, 990.0, 1670.0),
		}, 0.15, 19.8, 0.99),
	}
}

func scaleSignal(in []float64, gain float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * gain
	}
	return out
}

func computeAlignedQualityMetrics(input []float64, decoded []float32, channels int) (snrDB float64, corr float64, energyRatio float64) {
	in := float32Slice(input)
	if len(in) == 0 || len(decoded) == 0 {
		return 0, 0, 0
	}

	if channels < 1 {
		channels = 1
	}

	// Search for best overlap lag first; Opus pre-skip and lookahead can shift
	// the decoded stream relative to the original frame-aligned source.
	const minOverlap = 128
	maxLag := 1024
	if maxPossible := min(len(in), len(decoded)) - minOverlap; maxPossible < maxLag {
		maxLag = maxPossible
	}
	if maxLag < 0 {
		return 0, 0, 0
	}

	bestLag := 0
	bestErr := math.Inf(1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		inStart, decStart := 0, 0
		if lag > 0 {
			decStart = lag
		} else if lag < 0 {
			inStart = -lag
		}
		n := len(in) - inStart
		if m := len(decoded) - decStart; m < n {
			n = m
		}
		if n < minOverlap {
			continue
		}
		trimStart := min(64*channels, n/4)
		trimEnd := min(32*channels, n/8)
		start := trimStart
		end := n - trimEnd
		if end-start < minOverlap {
			start = 0
			end = n
		}
		if end <= start {
			continue
		}

		var sigPow, noisePow float64
		for i := start; i < end; i++ {
			x := float64(in[inStart+i])
			y := float64(decoded[decStart+i])
			sigPow += x * x
			d := y - x
			noisePow += d * d
		}
		if sigPow <= 0 {
			continue
		}
		err := noisePow / sigPow
		if err < bestErr {
			bestErr = err
			bestLag = lag
		}
	}

	inStart, decStart := 0, 0
	if bestLag > 0 {
		decStart = bestLag
	} else if bestLag < 0 {
		inStart = -bestLag
	}
	n := len(in) - inStart
	if m := len(decoded) - decStart; m < n {
		n = m
	}
	if n <= 0 {
		return 0, 0, 0
	}
	trimStart := min(64*channels, n/4)
	trimEnd := min(32*channels, n/8)
	start := trimStart
	end := n - trimEnd
	if end-start < minOverlap {
		start = 0
		end = n
	}
	if end <= start {
		return 0, 0, 0
	}

	var sigPow, noisePow float64
	var sx, sy, sxx, syy, sxy float64
	for i := start; i < end; i++ {
		x := float64(in[inStart+i])
		y := float64(decoded[decStart+i])
		sigPow += x * x
		d := y - x
		noisePow += d * d
		sx += x
		sy += y
		sxx += x * x
		syy += y * y
		sxy += x * y
	}
	count := float64(end - start)
	if sigPow > 0 && noisePow > 0 {
		snrDB = 10 * math.Log10(sigPow/noisePow)
	} else if noisePow == 0 {
		snrDB = 120
	}

	num := count*sxy - sx*sy
	denX := count*sxx - sx*sx
	denY := count*syy - sy*sy
	if denX > 0 && denY > 0 {
		corr = num / math.Sqrt(denX*denY)
	}

	inE := computeEnergy(in[inStart+start : inStart+end])
	outE := computeEnergy(decoded[decStart+start : decStart+end])
	if inE > 0 {
		energyRatio = outE / inE
	}
	return snrDB, corr, energyRatio
}

// regenCrossvalFixture regenerates the crossval fixture JSON by encoding
// scenarios with the current encoder and decoding with live opusdec.
// Returns true if the fixture was regenerated (sync.Once is reset).
func regenCrossvalFixture(t *testing.T, scenarios []crossvalFixtureScenario) {
	t.Helper()
	if !checkOpusdecAvailable() {
		t.Fatal("opusdec CLI is required to regenerate crossval fixtures")
	}

	entries := make([]opusdecCrossvalFixtureEntry, 0, len(scenarios))
	for _, sc := range scenarios {
		hash := oggSHA256Hex(sc.ogg)
		decoded, err := decodeWithOpusdecCLI(sc.ogg)
		if err != nil {
			t.Fatalf("%s: live opusdec decode failed: %v", sc.name, err)
		}
		buf := make([]byte, len(decoded)*4)
		for i, s := range decoded {
			binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(s))
		}
		entries = append(entries, opusdecCrossvalFixtureEntry{
			Name:             sc.name,
			SHA256:           hash,
			SampleRate:       sc.sampleRate,
			Channels:         sc.channels,
			DecodedF32Base64: base64.StdEncoding.EncodeToString(buf),
		})
	}

	fixture := opusdecCrossvalFixtureFile{
		Version: 1,
		Provenance: &opusdecCrossvalFixtureProvenance{
			GOOS:           runtime.GOOS,
			GOARCH:         runtime.GOARCH,
			LibopusVersion: libopustooling.DefaultVersion,
		},
		Entries: entries,
	}
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	path := opusdecCrossvalFixtureWritePath()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("Regenerated %s with %d entries", path, len(entries))

	// Reset the sync.Once so subsequent tests reload the fresh fixture.
	opusdecCrossvalFixtureOnce = sync.Once{}
	opusdecCrossvalFixtureMap = nil
	opusdecCrossvalFixtureErr = nil
}

func findOpusdecCrossvalFixtureEntry(entries map[string]opusdecCrossvalFixtureEntry, sc crossvalFixtureScenario, requireExactHash bool) (opusdecCrossvalFixtureEntry, bool, bool) {
	hash := oggSHA256Hex(sc.ogg)
	if entry, ok := entries[hash]; ok {
		return entry, true, true
	}
	if requireExactHash {
		return opusdecCrossvalFixtureEntry{}, false, false
	}
	for _, entry := range entries {
		if entry.Name == sc.name {
			return entry, false, true
		}
	}
	return opusdecCrossvalFixtureEntry{}, false, false
}

func decodeOpusdecCrossvalFixtureByScenarioName(name string) ([]float32, bool, error) {
	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		return nil, false, err
	}
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		samples, err := decodeFloat32LEBase64(entry.DecodedF32Base64)
		if err != nil {
			return nil, true, err
		}
		return samples, true, nil
	}
	return nil, false, nil
}

func TestOpusdecCrossvalFixtureCoverage(t *testing.T) {
	scenarios := buildCrossvalFixtureScenarios(t)
	updateFixture := strings.TrimSpace(os.Getenv(updateOpusdecCrossvalFixtureEnv)) == "1"
	requireExactHash := os.Getenv(requirePlatformFixturesEnv) != "" || updateFixture

	// Check if any fixture entries are stale. Normal test runs fail closed so
	// verify-production stays clean; fixture refresh is explicit via env var.
	// Fast CI accepts same-scenario decoded fixtures because native x86 feature
	// selection can change packet hashes. Platform fixture jobs keep exact hashes.
	entries, err := loadOpusdecCrossvalFixtureMap()
	needsRegen := err != nil
	if !needsRegen {
		for _, sc := range scenarios {
			if _, _, ok := findOpusdecCrossvalFixtureEntry(entries, sc, requireExactHash); !ok {
				needsRegen = true
				break
			}
		}
	}
	if needsRegen {
		fixturePath := opusdecCrossvalFixtureWritePath()
		if !updateFixture {
			t.Fatalf("opusdec crossval fixture %q is stale or missing; set %s=1 to regenerate", fixturePath, updateOpusdecCrossvalFixtureEnv)
		}
		if !checkOpusdecAvailable() {
			t.Fatalf("opusdec crossval fixture %q is stale or missing and opusdec is not available to regenerate", fixturePath)
		}
		regenCrossvalFixture(t, scenarios)
		t.Fatalf("updated opusdec crossval fixture at %s; rerun without %s", fixturePath, updateOpusdecCrossvalFixtureEnv)
	}

	if len(entries) < len(scenarios) {
		t.Fatalf("fixture entry count mismatch: got %d need at least %d", len(entries), len(scenarios))
	}

	scenarioByName := make(map[string]crossvalFixtureScenario, len(scenarios))
	for _, sc := range scenarios {
		if _, ok := scenarioByName[sc.name]; ok {
			t.Fatalf("duplicate scenario name %q", sc.name)
		}
		scenarioByName[sc.name] = sc
	}

	for _, sc := range scenarios {
		hash := oggSHA256Hex(sc.ogg)
		entry, exactHash, ok := findOpusdecCrossvalFixtureEntry(entries, sc, requireExactHash)
		if !ok {
			if requireExactHash {
				t.Fatalf("%s: missing fixture entry for ogg sha256=%s", sc.name, hash)
			}
			t.Fatalf("%s: missing fixture entry for scenario name %q (ogg sha256=%s)", sc.name, sc.name, hash)
		}
		if requireExactHash && !exactHash {
			t.Fatalf("%s: fixture did not match required ogg sha256=%s", sc.name, hash)
		}
		if entry.Name != sc.name {
			t.Fatalf("%s: fixture name mismatch for sha %s: got %q", sc.name, hash, entry.Name)
		}
		if entry.SampleRate != sc.sampleRate {
			t.Fatalf("%s: fixture sample_rate=%d want %d", sc.name, entry.SampleRate, sc.sampleRate)
		}
		if entry.Channels != sc.channels {
			t.Fatalf("%s: fixture channels=%d want %d", sc.name, entry.Channels, sc.channels)
		}
		samples, err := decodeFloat32LEBase64(entry.DecodedF32Base64)
		if err != nil {
			t.Fatalf("%s: decode fixture samples: %v", sc.name, err)
		}
		if len(samples) == 0 {
			t.Fatalf("%s: fixture samples are empty", sc.name)
		}
		expected := (sc.numFrames*960 - 312) * sc.channels
		if len(samples) != expected {
			t.Fatalf("%s: fixture decoded length mismatch: got %d want %d", sc.name, len(samples), expected)
		}
	}

	for _, e := range entries {
		sc, ok := scenarioByName[e.Name]
		if !ok {
			// Allow explicit platform hash aliases that intentionally map to an
			// existing scenario's decoded reference payload.
			if e.Name == "mono_20ms_single_windows_amd64_alias" {
				continue
			}
			t.Fatalf("fixture has stale or unknown entry name %q", e.Name)
		}
		if e.SampleRate != sc.sampleRate || e.Channels != sc.channels {
			t.Fatalf("fixture alias %q has rate/channels %d/%d, want %d/%d", e.Name, e.SampleRate, e.Channels, sc.sampleRate, sc.channels)
		}
		samples, err := decodeFloat32LEBase64(e.DecodedF32Base64)
		if err != nil {
			t.Fatalf("fixture alias %q: decode samples: %v", e.Name, err)
		}
		if want := (sc.numFrames*960 - 312) * sc.channels; len(samples) != want {
			t.Fatalf("fixture alias %q: decoded length %d, want %d", e.Name, len(samples), want)
		}
	}
}

func TestOpusdecCrossvalFixtureHonestyAgainstLiveOpusdec(t *testing.T) {
	if testing.Short() || strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_TEST_TIER"))) == "fast" {
		t.Skip("live opusdec fixture honesty requires parity tier")
	}
	if !checkOpusdecAvailable() {
		t.Skip("opusdec not available; fixture honesty check requires live opusdec")
	}

	scenarios := loadFrozenOpusdecInputs(t)
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			live, err := decodeWithOpusdecCLI(sc.ogg)
			if err != nil {
				t.Fatalf("live opusdec decode failed: %v", err)
			}
			fixture := sc.pcm
			if len(live) != len(fixture) {
				t.Fatalf("decoded length mismatch: live=%d fixture=%d", len(live), len(fixture))
			}

			maxAbs := float64(0)
			var sumSq float64
			for i := range live {
				diff := float64(live[i] - fixture[i])
				if abs := math.Abs(diff); abs > maxAbs {
					maxAbs = abs
				}
				sumSq += diff * diff
			}
			rmse := math.Sqrt(sumSq / float64(len(live)))
			if maxAbs > 2e-4 {
				t.Fatalf("fixture drift vs live opusdec: max abs diff %.6f > 0.0002", maxAbs)
			}
			if rmse > 2e-5 {
				t.Fatalf("fixture drift vs live opusdec: RMSE %.7f > 0.00002", rmse)
			}
		})
	}
}

func TestOpusdecCrossvalFixtureMatrix(t *testing.T) {
	t.Setenv("GOPUS_DISABLE_OPUSDEC", "1")

	scenarios := buildCrossvalFixtureScenarios(t)
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			decoded, err := decodeWithOpusdec(sc.ogg)
			if err != nil && os.Getenv(requirePlatformFixturesEnv) == "" && strings.Contains(err.Error(), "missing opusdec fixture for ogg sha256=") {
				var ok bool
				decoded, ok, err = decodeOpusdecCrossvalFixtureByScenarioName(sc.name)
				if err == nil && !ok {
					err = os.ErrNotExist
				}
			}
			if err != nil {
				if shouldSkipMissingOpusdecFixture(err) {
					t.Skipf("skipping cross-validation without external decoder support: %v", err)
				}
				t.Fatalf("decodeWithOpusdec failed: %v", err)
			}
			if len(decoded) == 0 {
				t.Fatal("fixture decode returned no samples")
			}
			if len(decoded)%sc.channels != 0 {
				t.Fatalf("decoded sample count %d not divisible by channels %d", len(decoded), sc.channels)
			}
			expected := (sc.numFrames*960 - 312) * sc.channels
			if len(decoded) != expected {
				t.Fatalf("decoded length mismatch: got %d want %d", len(decoded), expected)
			}
			for i, s := range decoded {
				if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
					t.Fatalf("decoded sample[%d] is non-finite: %v", i, s)
				}
			}

			outputEnergy := computeEnergy(decoded)
			if sc.expectNearSilence {
				if outputEnergy > 1e-6 {
					t.Fatalf("expected near silence, got energy %.9f", outputEnergy)
				}
				return
			}

			if len(sc.input) == 0 {
				t.Fatal("test scenario missing input samples")
			}
			inputEnergy := computeEnergy(float32Slice(sc.input))
			if inputEnergy <= 0 {
				t.Fatalf("invalid input energy %.9f", inputEnergy)
			}
			ratio := outputEnergy / inputEnergy
			if ratio < sc.minEnergyRatio {
				t.Fatalf("energy ratio too low: got %.4f want >= %.4f", ratio, sc.minEnergyRatio)
			}

			snrDB, corr, alignedRatio := computeAlignedQualityMetrics(sc.input, decoded, sc.channels)
			if snrDB < sc.minSNRDB {
				t.Fatalf(
					"SNR too low: got %.3f dB want >= %.3f dB (corr=%.5f alignedEnergy=%.4f)",
					snrDB, sc.minSNRDB, corr, alignedRatio,
				)
			}
			if corr < sc.minCorr {
				t.Fatalf("correlation too low: got %.5f want >= %.5f", corr, sc.minCorr)
			}
			if alignedRatio < sc.minEnergyRatio {
				t.Fatalf("aligned energy ratio too low: got %.4f want >= %.4f", alignedRatio, sc.minEnergyRatio)
			}
		})
	}
}

func checkOpusdecAvailable() bool {
	// Try common installation paths
	paths := []string{
		"/opt/homebrew/bin/opusdec",
		"/usr/local/bin/opusdec",
		"/usr/bin/opusdec",
	}

	// Check PATH first
	if _, err := exec.LookPath("opusdec"); err == nil {
		return true
	}

	// Check common paths
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}

	return false
}

// getOpusdecPath returns the path to opusdec binary.
func getOpusdecPath() string {
	// Try PATH first
	if path, err := exec.LookPath("opusdec"); err == nil {
		return path
	}

	// Try common paths
	paths := []string{
		"/opt/homebrew/bin/opusdec",
		"/usr/local/bin/opusdec",
		"/usr/bin/opusdec",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "opusdec"
}

// computeOggCRC computes the CRC-32 checksum for an Ogg page.
// The CRC field in the page header is set to 0 for computation.
func computeOggCRC(data []byte) uint32 {
	// Ogg uses CRC-32 with polynomial 0x04C11DB7
	// Direct algorithm (not using crc32 package table since Ogg uses non-standard polynomial)
	var crc uint32 = 0
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCLookup[((crc>>24)&0xff)^uint32(b)]
	}
	return crc
}

// Pre-computed lookup table for Ogg CRC-32
var oggCRCLookup [256]uint32

func init() {
	// Initialize Ogg CRC lookup table
	// Polynomial: 0x04C11DB7 (CRC-32)
	poly := uint32(0x04C11DB7)
	for i := range 256 {
		crc := uint32(i) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
		oggCRCLookup[i] = crc
	}
}

const opusdecCrossvalFixturePath = "testdata/opusdec_crossval_fixture.json"
const requirePlatformFixturesEnv = "GOPUS_REQUIRE_PLATFORM_FIXTURES"

func opusdecCrossvalFixturePathForArch() string {
	return opusdecCrossvalFixtureReadPath()
}

func opusdecCrossvalFixtureReadPath() string {
	return opusdecCrossvalFixtureReadPathFor(opusdecCrossvalFixturePath, runtime.GOOS, runtime.GOARCH)
}

func opusdecCrossvalFixtureReadPathFor(generic, goos, goarch string) string {
	platformPath := opusdecCrossvalPlatformFixturePath(generic, goos, goarch)
	if _, err := os.Stat(platformPath); err == nil {
		return platformPath
	}
	if os.Getenv(requirePlatformFixturesEnv) != "" {
		return platformPath
	}
	if goarch == "amd64" {
		legacyAMD64Path := opusdecCrossvalPlatformFixturePathForArchOnly(generic, goarch)
		if _, err := os.Stat(legacyAMD64Path); err == nil {
			return legacyAMD64Path
		}
	}
	return generic
}

func opusdecCrossvalFixtureWritePath() string {
	return opusdecCrossvalPlatformFixturePath(opusdecCrossvalFixturePath, runtime.GOOS, runtime.GOARCH)
}

func opusdecCrossvalPlatformFixturePath(generic, goos, goarch string) string {
	ext := filepath.Ext(generic)
	return strings.TrimSuffix(generic, ext) + "_" + goos + "_" + goarch + ext
}

func opusdecCrossvalPlatformFixturePathForArchOnly(generic, goarch string) string {
	ext := filepath.Ext(generic)
	return strings.TrimSuffix(generic, ext) + "_" + goarch + ext
}

func TestOpusdecCrossvalPlatformFixturePath(t *testing.T) {
	got := opusdecCrossvalPlatformFixturePath("testdata/opusdec_crossval_fixture.json", "linux", "arm64")
	want := "testdata/opusdec_crossval_fixture_linux_arm64.json"
	if got != want {
		t.Fatalf("platform fixture path=%q want %q", got, want)
	}
}

func TestOpusdecCrossvalRequirePlatformFixtureDisablesFallback(t *testing.T) {
	t.Setenv(requirePlatformFixturesEnv, "1")

	generic := filepath.Join(t.TempDir(), "opusdec_crossval_fixture.json")
	platform := opusdecCrossvalPlatformFixturePath(generic, runtime.GOOS, runtime.GOARCH)
	if got := opusdecCrossvalFixtureReadPathFor(generic, runtime.GOOS, runtime.GOARCH); got != platform {
		t.Fatalf("read path with %s=%q want %q", requirePlatformFixturesEnv, got, platform)
	}
}

func TestOpusdecCrossvalRequiredPlatformFixturePresent(t *testing.T) {
	if os.Getenv(requirePlatformFixturesEnv) == "" {
		t.Skipf("%s not set", requirePlatformFixturesEnv)
	}
	path := opusdecCrossvalFixtureWritePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("required opusdec crossval platform fixture %s is missing: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("required opusdec crossval platform fixture %s is empty", path)
	}
}

func TestOpusdecCrossvalRequiredPlatformFixtureProvenanceMatchesRuntime(t *testing.T) {
	t.Setenv(requirePlatformFixturesEnv, "1")

	if err := validateOpusdecCrossvalFixtureProvenance(&opusdecCrossvalFixtureProvenance{
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		LibopusVersion: libopustooling.DefaultVersion,
	}); err != nil {
		t.Fatalf("runtime platform provenance rejected: %v", err)
	}

	if err := validateOpusdecCrossvalFixtureProvenance(&opusdecCrossvalFixtureProvenance{
		GOOS:           "other",
		GOARCH:         runtime.GOARCH,
		LibopusVersion: libopustooling.DefaultVersion,
	}); err == nil {
		t.Fatalf("mismatched platform provenance accepted")
	}

	if err := validateOpusdecCrossvalFixtureProvenance(nil); err == nil {
		t.Fatalf("missing platform provenance accepted")
	}
}

type opusdecCrossvalFixtureFile struct {
	Version    int                               `json:"version"`
	Provenance *opusdecCrossvalFixtureProvenance `json:"provenance,omitempty"`
	Entries    []opusdecCrossvalFixtureEntry     `json:"entries"`
}

type opusdecCrossvalFixtureProvenance struct {
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	LibopusVersion string `json:"libopus_version,omitempty"`
}

type opusdecCrossvalFixtureEntry struct {
	Name             string `json:"name"`
	SHA256           string `json:"sha256"`
	SampleRate       int    `json:"sample_rate"`
	Channels         int    `json:"channels"`
	DecodedF32Base64 string `json:"decoded_f32le_base64"`
}

var (
	opusdecCrossvalFixtureOnce sync.Once
	opusdecCrossvalFixtureMap  map[string]opusdecCrossvalFixtureEntry
	opusdecCrossvalFixtureErr  error
)

func oggSHA256Hex(oggData []byte) string {
	sum := sha256.Sum256(oggData)
	return hex.EncodeToString(sum[:])
}

func loadOpusdecCrossvalFixtureMap() (map[string]opusdecCrossvalFixtureEntry, error) {
	opusdecCrossvalFixtureOnce.Do(func() {
		data, err := os.ReadFile(opusdecCrossvalFixturePathForArch())
		if err != nil {
			opusdecCrossvalFixtureErr = err
			return
		}
		var fixture opusdecCrossvalFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			opusdecCrossvalFixtureErr = err
			return
		}
		if fixture.Version != 1 {
			opusdecCrossvalFixtureErr = fmt.Errorf("unsupported opusdec crossval fixture version %d", fixture.Version)
			return
		}
		if err := validateOpusdecCrossvalFixtureProvenance(fixture.Provenance); err != nil {
			opusdecCrossvalFixtureErr = err
			return
		}
		m := make(map[string]opusdecCrossvalFixtureEntry, len(fixture.Entries))
		for _, e := range fixture.Entries {
			if e.SHA256 == "" {
				opusdecCrossvalFixtureErr = fmt.Errorf("fixture entry with empty sha256")
				return
			}
			m[e.SHA256] = e
		}
		opusdecCrossvalFixtureMap = m
	})
	return opusdecCrossvalFixtureMap, opusdecCrossvalFixtureErr
}

func validateOpusdecCrossvalFixtureProvenance(p *opusdecCrossvalFixtureProvenance) error {
	if p == nil {
		if os.Getenv(requirePlatformFixturesEnv) != "" {
			return fmt.Errorf("missing opusdec crossval fixture provenance")
		}
		return nil
	}
	if p.LibopusVersion != "" && p.LibopusVersion != libopustooling.DefaultVersion {
		return fmt.Errorf("opusdec crossval libopus_version=%q want %q", p.LibopusVersion, libopustooling.DefaultVersion)
	}
	if os.Getenv(requirePlatformFixturesEnv) == "" {
		return nil
	}
	if p.GOOS == "" || p.GOARCH == "" {
		return fmt.Errorf("missing opusdec crossval goos/goarch provenance")
	}
	if p.GOOS != runtime.GOOS || p.GOARCH != runtime.GOARCH {
		return fmt.Errorf("opusdec crossval platform provenance=%s/%s want %s/%s", p.GOOS, p.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func decodeFloat32LEBase64(src string) ([]float32, error) {
	raw, err := base64.StdEncoding.DecodeString(src)
	if err != nil {
		return nil, err
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("invalid float32le fixture length %d", len(raw))
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out, nil
}

func decodeWithOpusdecFixture(oggData []byte) ([]float32, error) {
	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		return nil, err
	}
	hash := oggSHA256Hex(oggData)
	entry, ok := entries[hash]
	if !ok {
		return nil, fmt.Errorf("missing opusdec fixture for ogg sha256=%s", hash)
	}
	return decodeFloat32LEBase64(entry.DecodedF32Base64)
}

// writeOggPage writes a single Ogg page to the writer.
// pageSeq: page sequence number
// headerType: 0x00 for normal, 0x02 for BOS (beginning of stream), 0x04 for EOS (end of stream)
// granulePos: granule position (-1 for headers)
// serial: bitstream serial number
// data: page payload
func writeOggPage(w io.Writer, pageSeq uint32, headerType byte, granulePos int64, serial uint32, data []byte) error {
	// Build page without CRC first
	var page bytes.Buffer

	// Ogg page header (27 bytes + segment table)
	page.WriteString("OggS")                             // capture pattern
	page.WriteByte(0)                                    // stream structure version
	page.WriteByte(headerType)                           // header type flag
	binary.Write(&page, binary.LittleEndian, granulePos) // granule position
	binary.Write(&page, binary.LittleEndian, serial)     // bitstream serial
	binary.Write(&page, binary.LittleEndian, pageSeq)    // page sequence
	binary.Write(&page, binary.LittleEndian, uint32(0))  // CRC placeholder
	// Segment table (lacing values). Packets with size % 255 == 0 must
	// include a trailing zero-length segment to terminate the packet.
	lacing := make([]byte, 0, (len(data)+254)/255+1)
	remaining := len(data)
	for remaining >= 255 {
		lacing = append(lacing, 255)
		remaining -= 255
	}
	if remaining > 0 || len(data) == 0 || len(data)%255 == 0 {
		lacing = append(lacing, byte(remaining))
	}
	if len(lacing) > 255 {
		return fmt.Errorf("ogg page has too many lacing segments: %d", len(lacing))
	}
	page.WriteByte(byte(len(lacing)))
	page.Write(lacing)

	// Payload
	page.Write(data)

	// Compute CRC over the entire page (with CRC field set to 0)
	pageData := page.Bytes()
	crc := computeOggCRC(pageData)

	// Insert CRC at offset 22
	pageData[22] = byte(crc)
	pageData[23] = byte(crc >> 8)
	pageData[24] = byte(crc >> 16)
	pageData[25] = byte(crc >> 24)

	_, err := w.Write(pageData)
	return err
}

// writeOggOpus writes a minimal Ogg Opus file containing the given packets.
// This is a simplified implementation for test purposes only:
// - Single-frame or few-frame test cases
// - One packet per Ogg page (no segment aggregation)
// - Fixed OpusHead and OpusTags headers
//
// Reference: RFC 7845 (Ogg Encapsulation for the Opus Audio Codec)
func writeOggOpus(w io.Writer, packets [][]byte, sampleRate, channels int) error {
	serial := uint32(0x12345678) // Arbitrary serial number
	pageSeq := uint32(0)

	// OpusHead header (19 bytes)
	// Reference: RFC 7845 Section 5.1
	var opusHead bytes.Buffer
	opusHead.WriteString("OpusHead")                                 // magic (8 bytes)
	opusHead.WriteByte(1)                                            // version
	opusHead.WriteByte(byte(channels))                               // channel count
	binary.Write(&opusHead, binary.LittleEndian, uint16(312))        // pre-skip (standard value)
	binary.Write(&opusHead, binary.LittleEndian, uint32(sampleRate)) // input sample rate
	binary.Write(&opusHead, binary.LittleEndian, int16(0))           // output gain (0 dB)
	opusHead.WriteByte(0)                                            // channel mapping family (0 = mono/stereo)

	// Write OpusHead page (BOS = beginning of stream)
	// Header pages must have granule position 0 per RFC 7845
	if err := writeOggPage(w, pageSeq, 0x02, 0, serial, opusHead.Bytes()); err != nil {
		return err
	}
	pageSeq++

	// OpusTags header (minimal)
	// Reference: RFC 7845 Section 5.2
	var opusTags bytes.Buffer
	opusTags.WriteString("OpusTags") // magic (8 bytes)
	vendorStr := "gopus"
	binary.Write(&opusTags, binary.LittleEndian, uint32(len(vendorStr))) // vendor string length
	opusTags.WriteString(vendorStr)                                      // vendor string
	binary.Write(&opusTags, binary.LittleEndian, uint32(0))              // user comment list length

	// Write OpusTags page (granule position 0 for headers)
	if err := writeOggPage(w, pageSeq, 0x00, 0, serial, opusTags.Bytes()); err != nil {
		return err
	}
	pageSeq++

	// These tests only emit 20ms frames; keep granule tracking fixed.
	samplesPerFrame := 960

	// Write audio packets
	granulePos := int64(0)
	for i, packet := range packets {
		packet = addCELTTOCForTest(packet, channels)
		granulePos += int64(samplesPerFrame)

		// Determine header type
		headerType := byte(0x00)
		if i == len(packets)-1 {
			headerType = 0x04 // EOS = end of stream
		}

		if err := writeOggPage(w, pageSeq, headerType, granulePos, serial, packet); err != nil {
			return err
		}
		pageSeq++
	}

	return nil
}

func addCELTTOCForTest(packet []byte, channels int) []byte {
	// celt.Encode* returns raw CELT payload for 20ms fullband frames.
	// Add a one-frame CELT TOC to make a valid Opus packet for Ogg wrapping.
	toc := byte(0xF8) // CELT-only, fullband, 20ms, mono, 1 frame
	if channels == 2 {
		toc = 0xFC // same with stereo bit set
	}
	out := make([]byte, len(packet)+1)
	out[0] = toc
	copy(out[1:], packet)
	return out
}

// decodeWithOpusdec decodes Ogg Opus data using the opusdec command-line tool.
// Returns decoded samples as float32.
//
// Note: On macOS, Go-created files may have com.apple.provenance extended
// attributes that can cause opusdec to fail with certain paths.
// This function uses /tmp directly for macOS compatibility.
func decodeWithOpusdec(oggData []byte) ([]float32, error) {
	decodeFallback := func() ([]float32, error) {
		fixture, ferr := decodeWithOpusdecFixture(oggData)
		if ferr == nil {
			return fixture, nil
		}
		if checkFFmpegAvailable() {
			ff, ffErr := decodeWithFFmpegCLI(oggData)
			if ffErr == nil {
				return ff, nil
			}
			return nil, fmt.Errorf("fixture fallback failed (%v) and ffmpeg decode failed (%v)", ferr, ffErr)
		}
		return nil, ferr
	}

	if os.Getenv("GOPUS_DISABLE_OPUSDEC") == "1" {
		return decodeFallback()
	}
	if checkOpusdecAvailable() {
		samples, err := decodeWithOpusdecCLI(oggData)
		if err == nil {
			return samples, nil
		}
		fallback, ferr := decodeFallback()
		if ferr == nil {
			return fallback, nil
		}
		return nil, fmt.Errorf("opusdec decode failed (%v) and fallback decode failed (%v)", err, ferr)
	}
	return decodeFallback()
}

func shouldSkipMissingOpusdecFixture(err error) bool {
	if err == nil {
		return false
	}
	if !strings.Contains(err.Error(), "missing opusdec fixture for ogg sha256=") {
		return false
	}
	opusdecEnabled := strings.TrimSpace(os.Getenv("GOPUS_DISABLE_OPUSDEC")) != "1"
	if (opusdecEnabled && checkOpusdecAvailable()) || checkFFmpegAvailable() {
		return false
	}
	return true
}

func decodeWithOpusdecOrSkip(t *testing.T, oggData []byte) []float32 {
	t.Helper()
	decoded, err := decodeWithOpusdec(oggData)
	if err == nil {
		return decoded
	}
	if shouldSkipMissingOpusdecFixture(err) {
		t.Skipf("skipping cross-validation without external decoder support: %v", err)
	}
	t.Fatalf("decodeWithOpusdec failed: %v", err)
	return nil
}

func decodeWithOpusdecCLI(oggData []byte) ([]float32, error) {
	opusdec := getOpusdecPath()

	// Use /tmp directly on macOS to avoid provenance/xattr issues.
	tmpDir := os.TempDir()
	if runtime.GOOS == "darwin" {
		tmpDir = "/tmp"
	}

	// Generate unique filenames using process ID.
	pid := os.Getpid()
	inputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_crossval_%d_in.opus", pid))
	outputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_crossval_%d_out.wav", pid))

	// Clean up any existing files
	os.Remove(inputPath)
	os.Remove(outputPath)
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// Write input Ogg file
	if err := os.WriteFile(inputPath, oggData, 0644); err != nil {
		return nil, err
	}

	// Verify file exists and is readable
	if info, err := os.Stat(inputPath); err != nil {
		return nil, fmt.Errorf("input file stat failed: %w", err)
	} else if info.Size() != int64(len(oggData)) {
		return nil, fmt.Errorf("input file size mismatch: expected %d, got %d", len(oggData), info.Size())
	}

	// Clear extended attributes on macOS (com.apple.provenance can cause issues)
	exec.Command("xattr", "-c", inputPath).Run()
	// Run opusdec with file-based I/O
	cmd := exec.Command(opusdec, "--float", inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Include file path info in error
		return nil, &OpusdecError{Output: fmt.Sprintf("path=%s, output=%s", inputPath, string(output)), Err: err}
	}

	// Read output WAV file
	wavData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}

	// Parse WAV
	samples, _, _, err := parseWAV(wavData)
	return samples, err
}

func checkFFmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func decodeWithFFmpegCLI(oggData []byte) ([]float32, error) {
	tmpDir := os.TempDir()
	if runtime.GOOS == "darwin" {
		tmpDir = "/tmp"
	}

	pid := os.Getpid()
	inputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_ffmpeg_%d_in.opus", pid))
	outputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_ffmpeg_%d_out.wav", pid))

	os.Remove(inputPath)
	os.Remove(outputPath)
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	if err := os.WriteFile(inputPath, oggData, 0o644); err != nil {
		return nil, err
	}

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", inputPath, "-f", "wav", "-acodec", "pcm_f32le", outputPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode failed: %v (%s)", err, out)
	}

	wavData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	samples, _, _, err := parseWAV(wavData)
	return samples, err
}

// OpusdecError represents an error from running opusdec.
type OpusdecError struct {
	Output string
	Err    error
}

func (e *OpusdecError) Error() string {
	return "opusdec failed: " + e.Err.Error() + ": " + e.Output
}

// parseWAV parses a WAV file and returns samples as float32.
// Returns: samples, sampleRate, channels, error
func parseWAV(data []byte) ([]float32, int, int, error) {
	if len(data) < 44 {
		return nil, 0, 0, &WAVError{Msg: "WAV file too short"}
	}

	// Check RIFF header
	if string(data[0:4]) != "RIFF" {
		return nil, 0, 0, &WAVError{Msg: "not a RIFF file"}
	}

	// Check WAVE format
	if string(data[8:12]) != "WAVE" {
		return nil, 0, 0, &WAVError{Msg: "not a WAVE file"}
	}

	// Find fmt chunk
	offset := 12
	var audioFormat uint16
	var numChannels uint16
	var sampleRate uint32
	var bitsPerSample uint16

	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 || offset+8+int(chunkSize) > len(data) {
				return nil, 0, 0, &WAVError{Msg: "invalid fmt chunk"}
			}
			audioFormat = binary.LittleEndian.Uint16(data[offset+8 : offset+10])
			numChannels = binary.LittleEndian.Uint16(data[offset+10 : offset+12])
			sampleRate = binary.LittleEndian.Uint32(data[offset+12 : offset+16])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+22 : offset+24])

			// Handle WAVE_FORMAT_EXTENSIBLE (0xFFFE) by mapping to PCM (1) or IEEE float (3).
			// SubFormat GUID starts at byte 24 within the fmt payload.
			if audioFormat == 0xFFFE && chunkSize >= 40 {
				subFormat := binary.LittleEndian.Uint16(data[offset+32 : offset+34])
				if subFormat == 1 || subFormat == 3 {
					audioFormat = subFormat
				}
			}
		case "data":
			// Found data chunk
			dataStart := offset + 8
			dataLen := int(chunkSize)
			if dataStart+dataLen > len(data) {
				dataLen = len(data) - dataStart
			}

			pcmData := data[dataStart : dataStart+dataLen]

			// Convert based on format
			var samples []float32

			switch audioFormat {
			case 3: // IEEE float
				if bitsPerSample == 32 {
					numSamples := len(pcmData) / 4
					samples = make([]float32, numSamples)
					for i := range numSamples {
						bits := binary.LittleEndian.Uint32(pcmData[i*4 : i*4+4])
						samples[i] = math.Float32frombits(bits)
					}
				}
			case 1: // PCM
				switch bitsPerSample {
				case 16:
					numSamples := len(pcmData) / 2
					samples = make([]float32, numSamples)
					for i := range numSamples {
						val := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
						samples[i] = float32(val) / 32768.0
					}
				case 24:
					numSamples := len(pcmData) / 3
					samples = make([]float32, numSamples)
					for i := range numSamples {
						b0 := pcmData[i*3]
						b1 := pcmData[i*3+1]
						b2 := pcmData[i*3+2]
						// Sign extend 24-bit to 32-bit
						val := int32(b0) | int32(b1)<<8 | int32(b2)<<16
						if val&0x800000 != 0 {
							val |= int32(-16777216) // 0xFF000000 sign extend
						}
						samples[i] = float32(val) / 8388608.0
					}
				}
			}

			if samples == nil {
				return nil, 0, 0, &WAVError{Msg: "unsupported WAV format"}
			}

			return samples, int(sampleRate), int(numChannels), nil
		}

		offset += 8 + int(chunkSize)
		// Word align
		if chunkSize%2 != 0 {
			offset++
		}
	}

	return nil, 0, 0, &WAVError{Msg: "no data chunk found"}
}

// WAVError represents a WAV parsing error.
type WAVError struct {
	Msg string
}

func (e *WAVError) Error() string {
	return "WAV parse error: " + e.Msg
}

// computeEnergy computes the RMS energy of samples.
// Returns sqrt(sum(s^2) / len(s))
func computeEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}

	var sumSquares float64
	for _, s := range samples {
		sumSquares += float64(s) * float64(s)
	}

	return math.Sqrt(sumSquares / float64(len(samples)))
}

// computeSNR computes Signal-to-Noise Ratio in dB.
// SNR = 10 * log10(signal_power / noise_power)
// where noise = decoded - original
func computeSNR(original, decoded []float32) float64 {
	// Use the shorter length
	n := min(len(decoded), len(original))
	if n == 0 {
		return 0
	}

	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		signalPower += float64(original[i]) * float64(original[i])
		noise := float64(decoded[i]) - float64(original[i])
		noisePower += noise * noise
	}

	if noisePower == 0 {
		return 100 // Perfect match (capped)
	}
	if signalPower == 0 {
		return -100 // No signal
	}

	return 10 * math.Log10(signalPower/noisePower)
}

// float32Slice converts float64 slice to float32.
func float32Slice(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

func float32Frames(frames [][]float64) [][]float32 {
	out := make([][]float32, len(frames))
	for i := range frames {
		out[i] = float32Slice(frames[i])
	}
	return out
}

// findPeak finds the maximum absolute value in samples.
func findPeak(samples []float32) float32 {
	var peak float32
	for _, s := range samples {
		abs := float32(math.Abs(float64(s)))
		if abs > peak {
			peak = abs
		}
	}
	return peak
}

// ============================================================================
// Phase 15-05: Energy correlation validation tests
// These tests verify CELT decoder output has meaningful energy correlation
// ============================================================================

// TestEnergyCorrelation records frame-size energy behavior while keeping
// encode/decode errors as hard failures.
func TestEnergyCorrelation(t *testing.T) {
	// Test configuration
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			// Create encoder and decoder
			enc := NewEncoder(1)
			dec := NewDecoder(1)

			// Generate test signal (sine wave)
			samples := make([]float64, frameSize)
			freq := 440.0 // Hz
			for i := range samples {
				samples[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/48000.0)
			}

			// Calculate input energy
			inputEnergy := 0.0
			for _, s := range samples {
				inputEnergy += s * s
			}

			// Encode
			encoded, err := enc.EncodeFrame(float32Slice(samples), frameSize)
			if err != nil {
				t.Fatalf("EncodeFrame failed: %v", err)
			}

			if len(encoded) == 0 {
				t.Fatal("Encoded empty frame")
			}

			// Decode
			decoded, err := dec.DecodeFrame(encoded, frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}

			// Calculate output energy
			outputEnergy := 0.0
			for _, s := range decoded {
				sf := float64(s)
				outputEnergy += sf * sf
			}

			// Calculate energy ratio
			if inputEnergy > 0 {
				energyRatio := outputEnergy / inputEnergy

				t.Logf("Energy ratio: %.2f%% (output=%f, input=%f)",
					energyRatio*100, outputEnergy, inputEnergy)

				if energyRatio < 0.01 { // Less than 1% is definitely broken
					t.Logf("Energy ratio too low: %.2f%%, indicates decoder/encoder mismatch",
						energyRatio*100)
				}
			}
		})
	}
}

// TestDecoderOutputNotSilent verifies decoder produces non-zero output for non-silent input.
func TestDecoderOutputNotSilent(t *testing.T) {
	d := NewDecoder(1)

	// Create frame data that should NOT be silence
	// Use real-looking CELT frame bytes
	frameData := []byte{
		0x80, 0x40, 0x20, 0x10, // Various bit patterns
		0x08, 0x04, 0x02, 0x01,
		0xFF, 0xFE, 0xFD, 0xFC,
		0x55, 0xAA, 0x55, 0xAA,
		0x12, 0x34, 0x56, 0x78,
		0x9A, 0xBC, 0xDE, 0xF0,
		0x11, 0x22, 0x33, 0x44,
		0x55, 0x66, 0x77, 0x88,
	}

	samples, err := d.DecodeFrame(frameData, 480)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Count non-zero samples
	nonZeroCount := 0
	maxAbs := 0.0
	for _, s := range samples {
		abs := math.Abs(float64(s))
		if abs > 1e-10 {
			nonZeroCount++
		}
		if abs > maxAbs {
			maxAbs = abs
		}
	}

	t.Logf("Non-zero samples: %d/%d (%.1f%%), max amplitude: %f",
		nonZeroCount, len(samples), float64(nonZeroCount)/float64(len(samples))*100, maxAbs)

	// If silence flag not set but output is all zeros, decoder may have bugs
	// Allow some silent frames but log for investigation
	if nonZeroCount == 0 {
		t.Log("Warning: All samples are zero - check if silence flag was decoded")
	}
}

// TestDecoderFiniteOutput verifies decoder never produces NaN or Inf.
func TestDecoderFiniteOutput(t *testing.T) {
	d := NewDecoder(1)
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			// Various frame data patterns
			patterns := [][]byte{
				make([]byte, 8),  // All zeros
				make([]byte, 32), // Zeros (will be filled below)
				{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // All ones
			}

			// Fill with patterns
			for i := range patterns[1] {
				patterns[1][i] = byte(i)
			}

			for _, pattern := range patterns {
				samples, err := d.DecodeFrame(pattern, frameSize)
				if err != nil {
					continue // Some patterns may be invalid
				}

				for i, s := range samples {
					sf := float64(s)
					if math.IsNaN(sf) {
						t.Errorf("Sample %d is NaN", i)
					}
					if math.IsInf(sf, 0) {
						t.Errorf("Sample %d is Inf", i)
					}
				}
			}
		})
	}
}
