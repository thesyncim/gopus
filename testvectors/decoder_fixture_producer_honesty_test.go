package testvectors

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

// fixtureProducerOpusDemo resolves the pinned C build matching the fixture's
// recorded compiler and configure settings. Paired Go comparisons use the
// separate build-aware reference helper.
func fixtureProducerOpusDemo(t *testing.T, want libopusFixtureProvenance) string {
	t.Helper()
	if want.GOOS != runtime.GOOS || want.GOARCH != runtime.GOARCH {
		fixtureProducerUnavailable(t, "fixture producer platform %s/%s is unavailable on %s/%s", want.GOOS, want.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	type candidate struct {
		suffix string
		ensure func(string, []string) bool
	}
	candidates := []candidate{
		{ensure: libopustooling.EnsureLibopus},
		{suffix: "-scalar", ensure: libopustooling.EnsureLibopusScalar},
		{suffix: "-simd", ensure: libopustooling.EnsureLibopusSIMD},
	}
	pathFor := func(c candidate) string {
		name := "opus_demo"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join("..", "tmp_check", "opus-"+libopustooling.DefaultVersion+c.suffix, name)
	}
	find := func() string {
		for _, c := range candidates {
			path := pathFor(c)
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				continue
			}
			got, ok := libopustooling.LibopusBuildProvenanceForTool(path)
			if ok && fixtureProducerSettingsMatch(want, got) {
				return path
			}
		}
		return ""
	}
	if path := find(); path != "" {
		return path
	}
	for _, c := range candidates {
		if !c.ensure(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()) {
			continue
		}
		if path := find(); path != "" {
			return path
		}
	}
	fixtureProducerUnavailable(t, "no pinned opus_demo matches fixture producer platform/compiler/configuration (%s/%s, %s, %q)",
		want.GOOS, want.GOARCH, want.CCTarget, want.Configure)
	return ""
}

func fixtureProducerUnavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	if libopustest.StrictRefRequired() {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func fixtureProducerSettingsMatch(want libopusFixtureProvenance, got libopustooling.LibopusBuildProvenance) bool {
	// The stamp format can change independently of the C build. Compare every
	// compiler and build setting recorded by the fixture, then require exact
	// decoded bytes below.
	return got.GOOS == want.GOOS && got.GOARCH == want.GOARCH &&
		got.LibopusVersion == want.LibopusVersion && got.QEXT == want.QEXT &&
		got.HostOS == want.HostOS && got.HostArch == want.HostArch && got.HostBits == want.HostBits &&
		got.CC == want.CC && got.CCPath == want.CCPath && got.CCTarget == want.CCTarget &&
		got.CCVersion == want.CCVersion && got.Configure == want.Configure &&
		got.CFLAGS == want.CFLAGS && got.CPPFLAGS == want.CPPFLAGS && got.LDFLAGS == want.LDFLAGS
}

func decodeFrozenPacketsWithProducer(t *testing.T, opusDemo string, sampleRate, channels int, packets [][]byte, ranges []uint32) []byte {
	t.Helper()
	if len(packets) != len(ranges) {
		t.Fatalf("fixture packet/range count mismatch: %d/%d", len(packets), len(ranges))
	}
	var bitstream bytes.Buffer
	for i, packet := range packets {
		if err := binary.Write(&bitstream, binary.BigEndian, uint32(len(packet))); err != nil {
			t.Fatal(err)
		}
		if err := binary.Write(&bitstream, binary.BigEndian, ranges[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := bitstream.Write(packet); err != nil {
			t.Fatal(err)
		}
	}
	bitPath := filepath.Join(t.TempDir(), "packets.bit")
	outPath := filepath.Join(filepath.Dir(bitPath), "decoded.f32")
	if err := os.WriteFile(bitPath, bitstream.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(opusDemo, "-d", fmt.Sprint(sampleRate), fmt.Sprint(channels), "-f32", bitPath, outPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture producer decode: %v (%s)", err, output)
	}
	decoded, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestCorpusDecoderFixtureProducerHonesty(t *testing.T) {
	requireTestTier(t, testTierExhaustive)
	fixture, err := loadCorpusFixture()
	if err != nil {
		t.Fatal(err)
	}
	opusDemo := fixtureProducerOpusDemo(t, fixture.Provenance)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ranges := make([]uint32, len(c.Packets))
			for i := range c.Packets {
				ranges[i] = c.Packets[i].FinalRange
			}
			got := decodeFrozenPacketsWithProducer(t, opusDemo, fixture.SampleRate, c.Channels, c.decodedPackets, ranges)
			want, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("frozen corpus PCM differs from producer decode: got %d bytes, want %d", len(got), len(want))
			}
		})
	}
}

func TestDecoderRateFixtureProducerHonesty(t *testing.T) {
	requireTestTier(t, testTierExhaustive)
	fixture, err := loadLibopusDecoderRateMatrixFixture()
	if err != nil {
		t.Fatal(err)
	}
	opusDemo := fixtureProducerOpusDemo(t, fixture.Provenance)
	for _, c := range fixture.Cases {
		t.Run(fmt.Sprintf("%s/rate%d", c.Name, c.APIRate), func(t *testing.T) {
			ranges := make([]uint32, len(c.Packets))
			for i := range c.Packets {
				ranges[i] = c.Packets[i].FinalRange
			}
			got := decodeFrozenPacketsWithProducer(t, opusDemo, c.APIRate, c.Channels, c.decodedPackets, ranges)
			want, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("frozen rate PCM differs from producer decode: got %d bytes, want %d", len(got), len(want))
			}
		})
	}
}
