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
		filepath.Join("..", "tools", "gen_libopus_encoder_packet_fixture.go"),
		filepath.Join("..", "tools", "gen_libopus_encoder_variants_fixture.go"),
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
}
