package testvectors

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
