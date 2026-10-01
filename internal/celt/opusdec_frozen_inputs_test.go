package celt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Frozen honesty replays captured Ogg bytes and PCM from their recorded decoder
// producer. It does not depend on the current encoder or platform fixture generator.
type frozenOpusdecInput struct {
	name string
	ogg  []byte
	pcm  []float32
}

func loadFrozenOpusdecInputs(t *testing.T) []frozenOpusdecInput {
	t.Helper()
	const directory = "testdata/opusdec_frozen_inputs"
	readPinned := func(name, digest string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := oggSHA256Hex(data); got != digest {
			t.Fatalf("frozen %s sha256=%s want%s", name, got, digest)
		}
		return data
	}
	data := readPinned("expected_pcm.json", "61290ea23a385524b31ff43ee289fe94181df4a19712ef0da67788a5760b03c2")
	var expected opusdecCrossvalFixtureFile
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Version != 1 || len(expected.Entries) != 18 {
		t.Fatal("invalid frozen PCM coverage/version")
	}
	byHash := make(map[string]opusdecCrossvalFixtureEntry)
	names := make(map[string]bool)
	for _, e := range expected.Entries {
		if _, exists := byHash[e.SHA256]; exists {
			t.Fatalf("duplicate frozen hash%s", e.SHA256)
		}
		byHash[e.SHA256] = e
		names[e.Name] = true
	}
	if len(names) != 11 {
		t.Fatalf("frozen scenario names=%d want11", len(names))
	}
	var manifest struct {
		Version int `json:"version"`
		Entries []struct {
			Name       string `json:"name"`
			SHA256     string `json:"sha256"`
			SampleRate int    `json:"sample_rate"`
			Channels   int    `json:"channels"`
			Frames     int    `json:"frames"`
			File       string `json:"file"`
		} `json:"entries"`
	}
	data = readPinned("manifest.json", "a4ad202c90010efc05c4a138a197b04336b4a8d4766362f04e7623bb4040b3f0")
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || len(manifest.Entries) != len(names) {
		t.Fatal("invalid frozen input coverage/version")
	}
	scenarios := make([]frozenOpusdecInput, 0, len(names))
	for _, e := range manifest.Entries {
		want, ok := byHash[e.SHA256]
		if !ok || !names[e.Name] || want.Name != e.Name || want.SampleRate != e.SampleRate || want.Channels != e.Channels || e.Frames <= 0 || e.File != e.SHA256+".ogg" {
			t.Fatalf("invalid frozen input metadata %+v", e)
		}
		delete(names, e.Name)
		ogg := readPinned(e.File, e.SHA256)
		pcm, err := decodeFloat32LEBase64(want.DecodedF32Base64)
		if err != nil {
			t.Fatal(err)
		}
		if len(pcm) == 0 || len(pcm)%e.Channels != 0 {
			t.Fatalf("invalid frozen PCM length for%s", e.Name)
		}
		scenarios = append(scenarios, frozenOpusdecInput{e.Name, ogg, pcm})
	}
	if len(names) != 0 {
		t.Fatalf("unrecovered frozen scenarios: %v", names)
	}
	return scenarios
}

func TestOpusdecFrozenInputIntegrity(t *testing.T) {
	if got := len(loadFrozenOpusdecInputs(t)); got != 11 {
		t.Fatalf("frozen inputs=%d want11", got)
	}
}
