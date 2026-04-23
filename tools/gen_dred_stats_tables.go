//go:build ignore
// +build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var dredArrays = []struct {
	goName string
	cName  string
}{
	{goName: "dredStateQuantScalesQ8", cName: "dred_state_quant_scales_q8"},
	{goName: "dredStateRQ8", cName: "dred_state_r_q8"},
	{goName: "dredStateP0Q8", cName: "dred_state_p0_q8"},
	{goName: "dredLatentQuantScalesQ8", cName: "dred_latent_quant_scales_q8"},
	{goName: "dredLatentRQ8", cName: "dred_latent_r_q8"},
	{goName: "dredLatentP0Q8", cName: "dred_latent_p0_q8"},
}

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		failf("getwd: %v", err)
	}

	srcPath := filepath.Join(repoRoot, "tmp_check", "opus-1.6.1", "dnn", "dred_rdovae_stats_data.c")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		failf("read %s: %v", srcPath, err)
	}

	arrays := make(map[string]string, len(dredArrays))
	for _, spec := range dredArrays {
		values, err := extractCArray(string(src), spec.cName)
		if err != nil {
			failf("extract %s: %v", spec.cName, err)
		}
		arrays[spec.goName] = values
	}

	var out bytes.Buffer
	fmt.Fprintln(&out, "package dred")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "// Code generated from libopus 1.6.1 dnn/dred_rdovae_stats_data.c for DRED entropy walking.")
	fmt.Fprintln(&out, "// DO NOT EDIT.")
	fmt.Fprintln(&out)
	for _, spec := range dredArrays {
		fmt.Fprintf(&out, "var %s = [...]uint8{\n", spec.goName)
		for _, line := range wrapCSV(arrays[spec.goName], 8) {
			fmt.Fprintf(&out, "\t%s,\n", line)
		}
		fmt.Fprintln(&out, "}")
		fmt.Fprintln(&out)
	}

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		failf("format generated output: %v", err)
	}

	dstPath := filepath.Join(repoRoot, "internal", "dred", "stats_tables.go")
	if err := os.WriteFile(dstPath, formatted, 0o644); err != nil {
		failf("write %s: %v", dstPath, err)
	}
}

func extractCArray(src, name string) (string, error) {
	pattern := fmt.Sprintf(`(?s)const opus_uint8 %s\[\d+\] = \{\s*(.*?)\s*\};`, regexp.QuoteMeta(name))
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(src)
	if len(m) != 2 {
		return "", fmt.Errorf("array not found")
	}
	values := strings.TrimSpace(m[1])
	values = strings.ReplaceAll(values, "\n", " ")
	values = strings.ReplaceAll(values, "\t", " ")
	values = strings.Join(strings.Fields(values), " ")
	values = strings.ReplaceAll(values, ", ", ",")
	values = strings.ReplaceAll(values, " ,", ",")
	values = strings.Trim(values, ", ")
	return values, nil
}

func wrapCSV(csv string, perLine int) []string {
	parts := strings.Split(csv, ",")
	lines := make([]string, 0, (len(parts)+perLine-1)/perLine)
	for len(parts) > 0 {
		n := perLine
		if len(parts) < n {
			n = len(parts)
		}
		lines = append(lines, strings.Join(parts[:n], ", "))
		parts = parts[n:]
	}
	return lines
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
