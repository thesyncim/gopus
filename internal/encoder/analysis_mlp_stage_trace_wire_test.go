//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAnalysisMLPStageTraceRejectsMalformedGAML(t *testing.T) {
	valid := validAnalysisMLPStageTraceWire()
	if _, err := parseAnalysisMLPStageTrace(valid); err != nil {
		t.Fatalf("parse valid GAML fixture: %v", err)
	}

	const headerBytes = 4 + 6*4 + 2*64
	const stageOneOffset = headerBytes
	const stageTwoOffset = stageOneOffset + 5*4 + (25+32)*4
	const stageThreeOffset = stageTwoOffset + 5*4 + (32+24+24)*4
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "magic", mutate: func(b []byte) []byte { b[0] = 'X'; return b }},
		{name: "version", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 4, 2); return b }},
		{name: "frame", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 8, 1); return b }},
		{name: "dense call count", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 12, 99); return b }},
		{name: "GRU call count", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 16, 49); return b }},
		{name: "stage count", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 20, 2); return b }},
		{name: "overflow", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 24, 1); return b }},
		{name: "analysis source hash", mutate: func(b []byte) []byte { b[28] ^= 1; return b }},
		{name: "MLP source hash", mutate: func(b []byte) []byte { b[92] ^= 1; return b }},
		{name: "dense input width", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageOneOffset+4, 24); return b }},
		{name: "GRU state width", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageTwoOffset+8, 23); return b }},
		{name: "dense2 output width", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageThreeOffset+16, 3); return b }},
		{name: "stage order", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageTwoOffset, 1); return b }},
		{name: "nonfinite value", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageOneOffset+5*4, 0x7fc00000); return b }},
		{name: "truncated payload", mutate: func(b []byte) []byte { return b[:len(b)-1] }},
		{name: "trailing byte", mutate: func(b []byte) []byte { return append(b, 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			malformed := append([]byte(nil), valid...)
			malformed = tc.mutate(malformed)
			if _, err := parseAnalysisMLPStageTrace(malformed); err == nil {
				t.Fatal("malformed GAML payload was accepted")
			}
		})
	}
}

func TestAnalysisMLPStageTraceDriverAnchorsAreUnique(t *testing.T) {
	root := celtQuantTraceRepoRoot(t)
	path := filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pinned GANI driver: %v", err)
	}
	if got := analysisMLPStageTraceSHA256(source); got != libopusAnalysisInfoSourceSHA256 {
		t.Fatalf("GANI driver SHA256=%s want pinned %s", got, libopusAnalysisInfoSourceSHA256)
	}

	instrumented, err := instrumentAnalysisMLPStageDriver(string(source))
	if err != nil {
		t.Fatalf("instrument pinned GANI driver: %v", err)
	}
	flush := strings.Index(instrumented, "  fflush(stdout);\n")
	write := strings.Index(instrumented, "  if (!gopus_analysis_mlp_trace_write()) return 7;\n")
	cleanup := strings.Index(instrumented, "  free(pcm);\n")
	if write < 0 || flush <= write || cleanup <= flush || strings.Count(instrumented, "gopus_analysis_mlp_trace_write()") != 1 {
		t.Fatal("GAML writer must run once after GANO records and before the final flush and driver cleanup")
	}

	const includeAnchor = "#include \"modes.h\"\n"
	duplicateInclude := strings.Replace(string(source), includeAnchor, includeAnchor+includeAnchor, 1)
	if _, err := instrumentAnalysisMLPStageDriver(duplicateInclude); err == nil {
		t.Fatal("duplicated GANI declaration anchor was accepted")
	}
	const endAnchor = "  fflush(stdout);\n  free(pcm);\n  free(st);\n  return 0;"
	duplicateEnd := string(source) + "\n" + endAnchor
	if _, err := instrumentAnalysisMLPStageDriver(duplicateEnd); err == nil {
		t.Fatal("duplicated GANI trailer anchor was accepted")
	}
}

func validAnalysisMLPStageTraceWire() []byte {
	data := []byte("GAML")
	for _, value := range []uint32{1, 0, 100, 50, 3, 0} {
		data = appendAnalysisMLPTraceU32(data, value)
	}
	data = append(data, libopusAnalysisSourceSHA256...)
	data = append(data, libopusMLPSourceSHA256...)
	data = appendAnalysisMLPTraceStage(data, 1, 25, 0, 0, 32, 25+32)
	data = appendAnalysisMLPTraceStage(data, 2, 32, 24, 24, 0, 32+24+24)
	data = appendAnalysisMLPTraceStage(data, 3, 24, 0, 0, 2, 24+2)
	return data
}

func appendAnalysisMLPTraceStage(data []byte, stage, inputs, before, after, outputs, floatCount uint32) []byte {
	for _, value := range []uint32{stage, inputs, before, after, outputs} {
		data = appendAnalysisMLPTraceU32(data, value)
	}
	for range floatCount {
		data = appendAnalysisMLPTraceU32(data, 0)
	}
	return data
}

func appendAnalysisMLPTraceU32(data []byte, value uint32) []byte {
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], value)
	return append(data, word[:]...)
}

func putAnalysisMLPTraceU32(data []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
}

func analysisMLPStageTraceSHA256(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
