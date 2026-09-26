//go:build ignore

package celt

// This exporter runs only in the pinned historical producer checkout. It calls
// that checkout's scenario builder and validates inputs against frozen hashes.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"simd/archsimd"
	"strings"
	"testing"
)

func TestExportFrozenOpusdecInputs(t *testing.T) {
	const producer = "1ee7f25232a25d640aa92ece60d952036c2b09bd"
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || runtime.Version() != "go1.27.1" ||
		os.Getenv("GOAMD64") != "v1" || os.Getenv("GOEXPERIMENT") != "simd" ||
		os.Getenv("GOPUS_FROZEN_PRODUCER_COMMIT") != producer {
		t.Fatal("recovery requires the recorded native Linux AMD64 Go 1.27.1 SIMD producer")
	}
	if !archsimd.X86.AVX() || !archsimd.X86.AVX2() || !archsimd.X86.FMA() {
		t.Fatal("recovery requires native AVX, AVX2 and FMA")
	}
	type entry struct {
		Name       string `json:"name"`
		SHA256     string `json:"sha256"`
		SampleRate int    `json:"sample_rate"`
		Channels   int    `json:"channels"`
		Frames     int    `json:"frames,omitempty"`
		File       string `json:"file,omitempty"`
	}
	var frozen struct {
		Entries []entry `json:"entries"`
	}
	data, err := os.ReadFile(os.Getenv("GOPUS_FROZEN_EXPECTED_FIXTURE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &frozen); err != nil {
		t.Fatal(err)
	}
	byHash := make(map[string]entry)
	names := make(map[string]bool)
	for _, e := range frozen.Entries {
		if _, ok := byHash[e.SHA256]; ok {
			t.Fatalf("duplicate frozen hash %s", e.SHA256)
		}
		byHash[e.SHA256] = e
		names[e.Name] = true
	}
	if len(frozen.Entries) != 18 || len(names) != 11 {
		t.Fatalf("frozen coverage hashes=%d names=%d, want18/11", len(frozen.Entries), len(names))
	}
	outDir := os.Getenv("GOPUS_FROZEN_INPUTS_OUT")
	if outDir == "" {
		t.Fatal("missing input artifact directory")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	cpuInfo, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		t.Fatal(err)
	}
	var cpu string
	for _, line := range strings.Split(string(cpuInfo), "\n") {
		if strings.HasPrefix(line, "model name") {
			_, cpu, _ = strings.Cut(line, ":")
			cpu = strings.TrimSpace(cpu)
			break
		}
	}
	if cpu == "" {
		t.Fatal("missing CPU model")
	}
	var entries []entry
	for _, sc := range buildCrossvalFixtureScenarios(t) {
		hash := sha256.Sum256(sc.ogg)
		sha := hex.EncodeToString(hash[:])
		want, ok := byHash[sha]
		if !ok || want.Name != sc.name || want.SampleRate != sc.sampleRate || want.Channels != sc.channels {
			t.Fatalf("%s historical Ogg does not match a frozen entry: sha256=%s", sc.name, sha)
		}
		if !names[sc.name] {
			t.Fatalf("duplicate or unknown scenario %s", sc.name)
		}
		delete(names, sc.name)
		want.File = sha + ".ogg"
		want.Frames = sc.numFrames
		if err := os.WriteFile(filepath.Join(outDir, want.File), sc.ogg, 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, want)
	}
	if len(names) != 0 || len(entries) != 11 {
		t.Fatalf("incomplete recovery: %d entries, missing names %v", len(entries), names)
	}
	manifest := map[string]any{
		"version": 1,
		"encoder_producer": map[string]any{
			"commit": producer, "go_version": runtime.Version(), "goos": runtime.GOOS,
			"goarch": runtime.GOARCH, "goamd64": "v1", "goexperiment": "simd",
			"cpu_model": cpu, "avx": true, "avx2": true, "fma": true,
		},
		"frozen_decoder_producer": map[string]any{
			"libopus_package": "1.4-1build1", "opus_tools_package": "0.2-1build3",
			"libopusfile_package": "0.12-4build3",
			"evidence_runs": []string{
				"https://github.com/thesyncim/gopus/actions/runs/36001803330",
				"https://github.com/thesyncim/gopus/actions/runs/36007082978",
			},
		},
		"entries": entries,
	}
	data, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recovered %d/11 scenario inputs, %d/18 frozen hashes; existing PCM/hash fixture is unchanged", len(entries), len(entries))
}
