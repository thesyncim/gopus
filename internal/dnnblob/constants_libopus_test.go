package dnnblob

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

func TestDNNBlobConstantsMatchLibopusReference(t *testing.T) {
	defs := readLibopusNNetDefines(t)
	for _, tc := range []struct {
		name string
		got  int32
	}{
		{name: "WEIGHT_BLOCK_SIZE", got: headerSize},
		{name: "WEIGHT_TYPE_float", got: TypeFloat},
		{name: "WEIGHT_TYPE_int", got: TypeInt},
		{name: "WEIGHT_TYPE_qweight", got: TypeQWeight},
		{name: "WEIGHT_TYPE_int8", got: TypeInt8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := defs[tc.name]
			if !ok {
				t.Fatalf("libopus define %s not found", tc.name)
			}
			if tc.got != want {
				t.Fatalf("%s=%d want %d", tc.name, tc.got, want)
			}
		})
	}
}

func readLibopusNNetDefines(t *testing.T) map[string]int32 {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	path := filepath.Join(repoRoot, "tmp_check", "opus-1.6.1", "dnn", "nnet.h")
	data := readLibopusRefFileOrSkip(t, path, "nnet.h")

	defs := make(map[string]int32)
	re := regexp.MustCompile(`(?m)^#define\s+(WEIGHT_(?:BLOCK_SIZE|TYPE_[A-Za-z0-9_]+))\s+([0-9]+)\s*$`)
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		v, err := strconv.ParseInt(m[2], 10, 32)
		if err != nil {
			t.Fatalf("parse libopus %s=%q: %v", m[1], m[2], err)
		}
		defs[m[1]] = int32(v)
	}
	return defs
}
