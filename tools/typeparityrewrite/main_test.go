package main

import (
	"go/parser"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteTypePreservesFunctionParameterFormatting(t *testing.T) {
	file := writeTempGoFile(t, "package p\n\nfunc f(x []float64) []float64 { return x }\n")
	rules := []rule{mustRule(t, "float64", "celtNorm", ctxTypes)}

	if _, _, err := rewriteFile(file, rules, nil, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, file)
	if !strings.Contains(got, "func f(x []celtNorm) []celtNorm {") {
		t.Fatalf("rewritten signature lost compact formatting:\n%s", got)
	}
}

func TestRewriteRSqrtUsesFloatWidthHelper(t *testing.T) {
	file := writeTempGoFile(t, `package p

import "math"

func f(energy celtNorm) float32 {
	scale := 1.0 / math.Sqrt(energy)
	return scale
}

func g(energy float64) float32 {
	scale := 1.0 / math.Sqrt(energy)
	return scale
}
`)
	rules := []rule{mustRule(t, "math.Sqrt", "celtRSqrt", ctxRSqrt)}

	if _, _, err := rewriteFile(file, rules, nil, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, file)
	if !strings.Contains(got, "scale := celtRSqrt(energy)") {
		t.Fatalf("rsqrt rewrite kept an unnecessary alias cast:\n%s", got)
	}
	if !strings.Contains(got, "scale := celtRSqrt(float32(energy))") {
		t.Fatalf("rsqrt rewrite did not cast a true float64 input:\n%s", got)
	}
}

func TestSimplifyCastsRemovesEquivalentFloatWidthAliases(t *testing.T) {
	file := writeTempGoFile(t, `package p

func f(v []celtNorm, energy opusVal16, x float64) {
	_ = float32(energy)
	_ = celtNorm(v[0])
	_ = float32(x)
	sign := float32(1.0)
	_ = sign
}
`)
	rules := []rule{mustRule(t, "float64", "float32", ctxSimplifyCasts)}

	if _, _, err := rewriteFile(file, rules, nil, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, file)
	if !strings.Contains(got, "_ = energy") {
		t.Fatalf("float-width scalar cast was not removed:\n%s", got)
	}
	if !strings.Contains(got, "_ = v[0]") {
		t.Fatalf("float-width slice element cast was not removed:\n%s", got)
	}
	if !strings.Contains(got, "_ = float32(x)") {
		t.Fatalf("true float64 cast was removed:\n%s", got)
	}
	if !strings.Contains(got, "sign := float32(1.0)") {
		t.Fatalf("cast on untyped constant was removed and would widen the local:\n%s", got)
	}
}

func TestAssignmentAndCallArgRewritesRespectScopeAndRuleTargets(t *testing.T) {
	file := writeTempGoFile(t, `package p

func f() {
	var x float64
	x = 1.5
	{
		x := "s"
		x = "t"
		_ = x
	}
	n := 1
	n = 2
	_ = n
}

func takeFloat(x float64) {}
func takeInt(x int) {}

func g() {
	takeFloat(1.5)
	takeInt(2)
}
`)
	rules := []rule{mustRule(t, "float64", "float32", ctxTypes|ctxAssignments|ctxCallArgs)}

	if _, _, err := rewriteFile(file, rules, nil, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, file)
	if strings.Contains(got, `float32("`) {
		t.Fatalf("shadowed string local was wrapped as float32:\n%s", got)
	}
	if strings.Contains(got, "int(2)") || strings.Contains(got, "takeInt(int(") {
		t.Fatalf("unchanged int target was wrapped:\n%s", got)
	}
	if !strings.Contains(got, "func takeFloat(x float32)") {
		t.Fatalf("float64 function parameter was not rewritten:\n%s", got)
	}
}

func TestGoFilesNormalizesAbsolutePathsUnderWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(subdir, "sample.go")
	if err := os.WriteFile(file, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	}()

	files, err := goFiles([]string{file})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "sub/sample.go" {
		t.Fatalf("goFiles(%q) = %v, want [sub/sample.go]", file, files)
	}
}

func TestAllowedLineSetHonorsDigestCounts(t *testing.T) {
	src := []byte("x := float64(1)\nx := float64(1)\n")
	entries := map[string]allowEntry{
		digestLine("x := float64(1)"): {count: 1, sample: "x := float64(1)"},
	}
	allowed := allowedLineSet(src, entries)
	if !allowed[1] {
		t.Fatalf("first matching digest line was not allowed")
	}
	if allowed[2] {
		t.Fatalf("second matching digest line exceeded allowlist count")
	}
}

func writeTempGoFile(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustRule(t *testing.T, from, to string, contexts rewriteContext) rule {
	t.Helper()
	toExpr, err := parser.ParseExpr(to)
	if err != nil {
		t.Fatal(err)
	}
	return rule{from: from, to: to, contexts: contexts, toExpr: toExpr}
}
