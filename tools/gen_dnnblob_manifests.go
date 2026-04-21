package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type manifestSpec struct {
	VarName    string
	SourceFile string
	ArrayName  string
}

var (
	arrayBlockRE = regexp.MustCompile(`(?s)const WeightArray (\w+)\[\] = \{\n(.*?)\n\};`)
	recordNameRE = regexp.MustCompile(`\{"([^"]+)",`)
)

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		fail("getwd", err)
	}

	specs := []manifestSpec{
		{VarName: "plcRequiredRecordNames", SourceFile: "tmp_check/opus-1.6.1/dnn/plc_data.c", ArrayName: "plcmodel_arrays"},
		{VarName: "farganRequiredRecordNames", SourceFile: "tmp_check/opus-1.6.1/dnn/fargan_data.c", ArrayName: "fargan_arrays"},
		{VarName: "osceLACERequiredRecordNames", SourceFile: "tmp_check/opus-1.6.1/dnn/lace_data.c", ArrayName: "lacelayers_arrays"},
		{VarName: "osceNoLACERequiredRecordNames", SourceFile: "tmp_check/opus-1.6.1/dnn/nolace_data.c", ArrayName: "nolacelayers_arrays"},
		{VarName: "osceBWERequiredRecordNames", SourceFile: "tmp_check/opus-1.6.1/dnn/bbwenet_data.c", ArrayName: "bbwenetlayers_arrays"},
	}

	var out bytes.Buffer
	out.WriteString("package dnnblob\n\n")
	out.WriteString("// Code generated from pinned libopus 1.6.1 DNN weight manifests. DO NOT EDIT.\n\n")

	for _, spec := range specs {
		path := filepath.Join(repoRoot, spec.SourceFile)
		names, err := parseManifest(path, spec.ArrayName)
		if err != nil {
			fail(spec.ArrayName, err)
		}
		fmt.Fprintf(&out, "var %s = []string{\n", spec.VarName)
		for _, name := range names {
			fmt.Fprintf(&out, "\t%q,\n", name)
		}
		out.WriteString("}\n\n")
	}

	dest := filepath.Join(repoRoot, "internal/dnnblob/model_manifests_generated.go")
	if err := os.WriteFile(dest, out.Bytes(), 0o644); err != nil {
		fail("write generated file", err)
	}
}

func parseManifest(path, arrayName string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	matches := arrayBlockRE.FindAllStringSubmatch(string(data), -1)
	for _, match := range matches {
		if len(match) != 3 || match[1] != arrayName {
			continue
		}
		body := match[2]
		found := recordNameRE.FindAllStringSubmatch(body, -1)
		if len(found) == 0 {
			return nil, fmt.Errorf("array %s has no records", arrayName)
		}
		names := make([]string, 0, len(found))
		seen := make(map[string]struct{}, len(found))
		for _, item := range found {
			name := strings.TrimSpace(item[1])
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	return nil, fmt.Errorf("array %s not found in %s", arrayName, path)
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
