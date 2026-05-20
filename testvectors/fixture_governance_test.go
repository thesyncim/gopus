package testvectors

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestFixtureGeneratorScriptsBuildIgnore(t *testing.T) {
	requireTestTier(t, testTierFast)

	paths := []string{
		filepath.Join("..", "tools", "gen_libopus_decoder_matrix_fixture.go"),
		filepath.Join("..", "tools", "gen_libopus_decoder_loss_fixture.go"),
		filepath.Join("..", "tools", "gen_libopus_encoder_packet_fixture.go"),
		filepath.Join("..", "tools", "gen_libopus_encoder_variants_fixture.go"),
		filepath.Join("..", "tools", "gen_opusdec_crossval_fixture.go"),
	}
	for _, p := range paths {
		p := p
		t.Run(filepath.Base(p), func(t *testing.T) {
			f, err := os.Open(p)
			if err != nil {
				t.Fatalf("open generator script: %v", err)
			}
			defer f.Close()

			sc := bufio.NewScanner(f)
			if !sc.Scan() {
				t.Fatalf("missing first line in %s", p)
			}
			first := strings.TrimSpace(sc.Text())
			if first != "//go:build ignore" {
				t.Fatalf("first line must be //go:build ignore in %s, got %q", p, first)
			}
		})
	}
}

func TestGeneratedFilesDeclareGeneratedMarkerBeforePackage(t *testing.T) {
	requireTestTier(t, testTierFast)

	paths := []string{
		filepath.Join("..", "celt", "kissfft32_lpcnet_320_static.go"),
		filepath.Join("..", "celt", "math_utils_tables_static.go"),
		filepath.Join("..", "celt", "window_tables_static.go"),
		filepath.Join("..", "container", "ogg", "projection_demixing_defaults_data.go"),
		filepath.Join("..", "internal", "dnnblob", "model_manifests_generated.go"),
		filepath.Join("..", "internal", "dred", "stats_deadzone_tables.go"),
		filepath.Join("..", "internal", "dred", "stats_tables.go"),
		filepath.Join("..", "internal", "lpcnetplc", "analysis_tables_generated.go"),
		filepath.Join("..", "multistream", "projection_mixing_defaults_data.go"),
		filepath.Join("..", "silk", "libopus_tables.go"),
	}
	for _, p := range paths {
		p := p
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read generated file: %v", err)
			}
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "//go:build ") || strings.HasPrefix(trimmed, "// +build ") {
					continue
				}
				if !strings.HasPrefix(trimmed, "// Code generated ") || !strings.Contains(trimmed, "DO NOT EDIT.") {
					t.Fatalf("first non-build line in %s must be a standard generated marker, got %q", p, trimmed)
				}
				return
			}
			t.Fatalf("empty generated file %s", p)
		})
	}
}

func TestEncoderVariantsFixtureStableOrdering(t *testing.T) {
	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load variants fixture: %v", err)
	}
	prev := ""
	for i, c := range fixture.Cases {
		key := c.Name + "|" + c.Variant
		if i > 0 && key < prev {
			t.Fatalf("variants fixture not sorted at case[%d]: %q < %q", i, key, prev)
		}
		prev = key
	}
}

func TestEncoderPacketFixtureStableOrdering(t *testing.T) {
	fixture, err := loadEncoderCompliancePacketsFixture()
	if err != nil {
		t.Fatalf("load packets fixture: %v", err)
	}
	prev := ""
	for i, c := range fixture.Cases {
		key := fmt.Sprintf("%s|%s|%06d|%03d|%07d", c.Mode, c.Bandwidth, c.FrameSize, c.Channels, c.Bitrate)
		if i > 0 && key < prev {
			t.Fatalf("packets fixture not sorted at case[%d]: %q < %q", i, key, prev)
		}
		prev = key
	}
}

func TestFixtureGeneratorsUseLibopusOpusDemo(t *testing.T) {
	requireTestTier(t, testTierFast)

	variants, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load variants fixture: %v", err)
	}
	if !strings.Contains(strings.ToLower(variants.Generator), "opus_demo") {
		t.Fatalf("variants fixture generator must reference opus_demo, got %q", variants.Generator)
	}
	if !strings.Contains(strings.ToLower(variants.Generator), "opus-"+libopustooling.DefaultVersion) {
		t.Fatalf("variants fixture generator must reference pinned libopus %s, got %q", libopustooling.DefaultVersion, variants.Generator)
	}

	packets, err := loadEncoderCompliancePacketsFixture()
	if err != nil {
		t.Fatalf("load packets fixture: %v", err)
	}
	if !strings.Contains(strings.ToLower(packets.Generator), "opus_demo") {
		t.Fatalf("packets fixture generator must reference opus_demo, got %q", packets.Generator)
	}
	if !strings.Contains(strings.ToLower(packets.Generator), "opus-"+libopustooling.DefaultVersion) {
		t.Fatalf("packets fixture generator must reference pinned libopus %s, got %q", libopustooling.DefaultVersion, packets.Generator)
	}

	decoderMatrix, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}
	if !strings.Contains(strings.ToLower(decoderMatrix.Generator), "opus_demo") {
		t.Fatalf("decoder matrix fixture generator must reference opus_demo, got %q", decoderMatrix.Generator)
	}
	if !strings.Contains(strings.ToLower(decoderMatrix.Generator), "opus-"+libopustooling.DefaultVersion) {
		t.Fatalf("decoder matrix fixture generator must reference pinned libopus %s, got %q", libopustooling.DefaultVersion, decoderMatrix.Generator)
	}

	decoderLoss, err := loadLibopusDecoderLossFixture()
	if err != nil {
		t.Fatalf("load decoder loss fixture: %v", err)
	}
	if !strings.Contains(strings.ToLower(decoderLoss.Generator), "opus_demo") {
		t.Fatalf("decoder loss fixture generator must reference opus_demo, got %q", decoderLoss.Generator)
	}
	if !strings.Contains(strings.ToLower(decoderLoss.Generator), "opus-"+libopustooling.DefaultVersion) {
		t.Fatalf("decoder loss fixture generator must reference pinned libopus %s, got %q", libopustooling.DefaultVersion, decoderLoss.Generator)
	}
}
