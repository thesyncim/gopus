package testvectors

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

// fixtureProducerOpusDemo resolves the C tree recorded by the fixture. If the
// generator records a source-tree path, that path chooses the tree directly;
// otherwise complete compiler provenance must identify one candidate.
func fixtureProducerOpusDemo(t *testing.T, generator string, want libopusFixtureProvenance) string {
	t.Helper()
	if want.GOOS != runtime.GOOS || want.GOARCH != runtime.GOARCH {
		fixtureProducerUnavailable(t, "fixture producer platform %s/%s is unavailable on %s/%s", want.GOOS, want.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	version := want.LibopusVersion
	if version == "" {
		version = libopustooling.DefaultVersion
	}
	if suffix, ok := fixtureProducerSourceSuffix(generator, version); ok {
		path, err := resolveFixtureProducerOpusDemo(version, suffix)
		if err != nil {
			fixtureProducerUnavailable(t, "resolve recorded fixture producer %q: %v", generator, err)
			return ""
		}
		got, ok := libopustooling.LibopusBuildProvenanceForTool(path)
		settingsMatch := fixtureProducerSettingsMatch(want, got)
		if fixtureHasBasicProducerProvenance(want) {
			settingsMatch = fixtureProducerBasicSettingsMatch(want, got)
		}
		if !ok || !settingsMatch {
			fixtureProducerUnavailable(t, "recorded fixture producer %q has mismatched or missing compiler provenance", generator)
			return ""
		}
		return path
	}
	if !fixtureHasCompleteCompilerProvenance(want) {
		fixtureProducerUnavailable(t, "fixture generator %q does not identify a source tree and its compiler provenance is incomplete", generator)
		return ""
	}
	var matchingPath string
	for _, suffix := range []string{"", "-scalar", "-simd"} {
		path, err := resolveFixtureProducerOpusDemo(version, suffix)
		if err != nil {
			continue
		}
		got, ok := libopustooling.LibopusBuildProvenanceForTool(path)
		if ok && fixtureProducerSettingsMatch(want, got) {
			if matchingPath != "" {
				fixtureProducerUnavailable(t, "fixture provenance matches multiple opus_demo trees (%s and %s)", matchingPath, path)
				return ""
			}
			matchingPath = path
		}
	}
	if matchingPath != "" {
		return matchingPath
	}
	fixtureProducerUnavailable(t, "no pinned opus_demo matches fixture producer platform/compiler/configuration (%s/%s, %s, %q)",
		want.GOOS, want.GOARCH, want.CCTarget, want.Configure)
	return ""
}

func fixtureProducerSourceSuffix(generator, version string) (string, bool) {
	name := strings.ReplaceAll(generator, "\\", "/")
	tool := path.Base(name)
	if tool != "opus_demo" && tool != "opus_demo.exe" {
		return "", false
	}
	tree := path.Base(path.Dir(name))
	base := "opus-" + version
	if tree == base {
		return "", true
	}
	for _, suffix := range []string{"-scalar", "-simd"} {
		if tree == base+suffix {
			return suffix, true
		}
	}
	return "", false
}

func resolveFixtureProducerOpusDemo(version, suffix string) (string, error) {
	roots := libopustooling.DefaultSearchRoots()
	switch suffix {
	case "":
		return libopustooling.FindOrEnsureDefaultOpusDemo(version, roots)
	case "-scalar":
		return libopustooling.FindOrEnsureOpusDemoForVariant(version, roots, libopustooling.LibopusReferenceScalar)
	case "-simd":
		return libopustooling.FindOrEnsureOpusDemoForVariant(version, roots, libopustooling.LibopusReferenceSIMD)
	default:
		return "", fmt.Errorf("unsupported recorded libopus tree suffix %q", suffix)
	}
}

func fixtureHasCompleteCompilerProvenance(p libopusFixtureProvenance) bool {
	return p.HostOS != "" && p.HostArch != "" && p.HostBits != "" && p.CC != "" && p.CCPath != "" &&
		p.CCTarget != "" && p.CCVersion != "" && p.Configure != "" && p.CFLAGS != "" && p.LibopusBuildStampSHA256 != ""
}

func fixtureHasBasicProducerProvenance(p libopusFixtureProvenance) bool {
	return p.GOOS != "" && p.GOARCH != "" && p.LibopusVersion != "" && p.QEXT != "" &&
		p.HostOS == "" && p.HostArch == "" && p.HostBits == "" && p.CC == "" && p.CCPath == "" &&
		p.CCTarget == "" && p.CCVersion == "" && p.Configure == "" && p.CFLAGS == "" && p.CPPFLAGS == "" &&
		p.LDFLAGS == "" && p.LibopusBuildStampSHA256 == ""
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

func fixtureProducerBasicSettingsMatch(want libopusFixtureProvenance, got libopustooling.LibopusBuildProvenance) bool {
	return got.GOOS == want.GOOS && got.GOARCH == want.GOARCH &&
		got.LibopusVersion == want.LibopusVersion && got.QEXT == want.QEXT
}

func TestFixtureProducerSourceSuffixUsesRecordedTree(t *testing.T) {
	for _, tc := range []struct {
		name      string
		generator string
		want      string
		ok        bool
	}{
		{name: "default", generator: "tmp_check/opus-1.6.1/opus_demo", want: "", ok: true},
		{name: "scalar", generator: "/build/gopus/tmp_check/opus-1.6.1-scalar/opus_demo", want: "-scalar", ok: true},
		{name: "simd windows path", generator: `tmp_check\opus-1.6.1-simd\opus_demo.exe`, want: "-simd", ok: true},
		{name: "descriptive generator", generator: "gen_corpus_decoder_parity_fixture via opus_demo opus-1.6.1"},
		{name: "wrong version", generator: "tmp_check/opus-1.6.10/opus_demo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fixtureProducerSourceSuffix(tc.generator, libopustooling.DefaultVersion)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("fixtureProducerSourceSuffix(%q)=(%q,%t), want (%q,%t)", tc.generator, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestFixtureProducerSettingsMatchDoesNotIgnoreIncompleteCompilerFields(t *testing.T) {
	want := libopusFixtureProvenance{
		GOOS: "linux", GOARCH: "amd64", LibopusVersion: libopustooling.DefaultVersion, QEXT: "0",
		HostOS: "Linux", HostArch: "x86_64", HostBits: "64", CC: "cc", CCPath: "/usr/bin/cc",
		CCTarget: "x86_64-linux-gnu", CCVersion: "gcc test", Configure: "--enable-static --disable-shared",
		CFLAGS: "-O3 -DNDEBUG", CPPFLAGS: "", LDFLAGS: "",
	}
	got := libopustooling.LibopusBuildProvenance{
		GOOS: "linux", GOARCH: "amd64", LibopusVersion: libopustooling.DefaultVersion, QEXT: "0",
		HostOS: "Linux", HostArch: "x86_64", HostBits: "64", CC: "cc", CCPath: "/usr/bin/cc",
		CCTarget: "x86_64-linux-gnu", CCVersion: "gcc test", Configure: "--enable-static --disable-shared",
		CFLAGS: "-O3 -DNDEBUG", CPPFLAGS: "unexpected", LDFLAGS: "",
	}
	if fixtureProducerSettingsMatch(want, got) {
		t.Fatal("complete fixture provenance accepted a compiler flag mismatch")
	}
	if fixtureHasBasicProducerProvenance(want) {
		t.Fatal("compiler provenance was incorrectly treated as legacy basic provenance")
	}
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
	opusDemo := fixtureProducerOpusDemo(t, fixture.Generator, fixture.Provenance)
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
	opusDemo := fixtureProducerOpusDemo(t, fixture.Generator, fixture.Provenance)
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
