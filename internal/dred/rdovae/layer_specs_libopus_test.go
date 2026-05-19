package rdovae

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRDOVAELayerSpecsMatchLibopusReference(t *testing.T) {
	assertLayerSpecsMatchLibopus(t, "encoder", "dred_rdovae_enc_data.c", "init_rdovaeenc", EncoderLayerSpecs())
	assertLayerSpecsMatchLibopus(t, "decoder", "dred_rdovae_dec_data.c", "init_rdovaedec", DecoderLayerSpecs())
}

func assertLayerSpecsMatchLibopus(t *testing.T, label, fileName, initName string, got []LinearLayerSpec) {
	t.Helper()

	path := filepath.Join(repoRootForLayerSpecTest(t), "tmp_check", "opus-1.6.1", "dnn", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read libopus %s layer specs: %v", label, err)
	}
	want := parseLibopusLinearInitSpecs(t, string(data), initName)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s layer specs mismatch\n got=%#v\nwant=%#v", label, got, want)
	}
}

func repoRootForLayerSpecTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func parseLibopusLinearInitSpecs(t *testing.T, source, initName string) []LinearLayerSpec {
	t.Helper()

	start := strings.Index(source, "int "+initName+"(")
	if start < 0 {
		t.Fatalf("libopus %s not found", initName)
	}
	body := source[start:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}

	arg := `(?:NULL|"[^"]+")`
	re := regexp.MustCompile(`linear_init\(&model->([A-Za-z0-9_]+)\s*,\s*arrays\s*,\s*(` + arg + `)\s*,\s*(` + arg + `)\s*,\s*(` + arg + `)\s*,\s*(` + arg + `)\s*,\s*(` + arg + `)\s*,\s*(` + arg + `)\s*,\s*(` + arg + `)\s*,\s*([0-9]+)\s*,\s*([0-9]+)\)`)
	matches := re.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("no libopus linear_init specs found in %s", initName)
	}

	specs := make([]LinearLayerSpec, 0, len(matches))
	for _, m := range matches {
		if libopusStringArg(m[7]) != "" {
			t.Fatalf("%s layer %s uses unsupported diagonal weights %q", initName, m[1], m[7])
		}
		inputs, err := strconv.Atoi(m[9])
		if err != nil {
			t.Fatalf("%s layer %s inputs: %v", initName, m[1], err)
		}
		outputs, err := strconv.Atoi(m[10])
		if err != nil {
			t.Fatalf("%s layer %s outputs: %v", initName, m[1], err)
		}
		specs = append(specs, LinearLayerSpec{
			Name:         m[1],
			Bias:         libopusStringArg(m[2]),
			Subias:       libopusStringArg(m[3]),
			Weights:      libopusStringArg(m[4]),
			FloatWeights: libopusStringArg(m[5]),
			WeightsIdx:   libopusStringArg(m[6]),
			Scale:        libopusStringArg(m[8]),
			NbInputs:     inputs,
			NbOutputs:    outputs,
		})
	}
	return specs
}

func libopusStringArg(s string) string {
	if s == "NULL" {
		return ""
	}
	return strings.Trim(s, `"`)
}
