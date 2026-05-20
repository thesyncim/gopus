package dnnblob

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDREDModelManifestsMatchLibopusReference(t *testing.T) {
	assertDREDManifestMatchesLibopus(t, "encoder", "dred_rdovae_enc_data.c", "rdovaeenc_arrays", dredEncoderRequiredRecordNames)
	assertDREDManifestMatchesLibopus(t, "decoder", "dred_rdovae_dec_data.c", "rdovaedec_arrays", dredDecoderRequiredRecordNames)
}

func assertDREDManifestMatchesLibopus(t *testing.T, label, fileName, arrayName string, got []string) {
	t.Helper()

	data := libopustest.ReadRefFileOrSkip(t, label+" manifest", "dnn", fileName)

	want := sortedRecordNames(parseLibopusWeightArrayNames(t, string(data), arrayName))
	got = sortedRecordNames(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s DRED manifest mismatch\n got=%q\nwant=%q", label, got, want)
	}
}

func parseLibopusWeightArrayNames(t *testing.T, source, arrayName string) []string {
	t.Helper()

	defined := make(map[string]bool)
	defineRe := regexp.MustCompile(`#define WEIGHTS_([A-Za-z0-9_]+)_DEFINED`)
	for _, m := range defineRe.FindAllStringSubmatch(source, -1) {
		defined[m[1]] = true
	}

	start := strings.Index(source, "const WeightArray "+arrayName+"[] = {")
	if start < 0 {
		t.Fatalf("libopus %s not found", arrayName)
	}
	body := source[start:]
	if end := strings.Index(body, "\n};"); end >= 0 {
		body = body[:end]
	}

	entryRe := regexp.MustCompile(`\{"([A-Za-z0-9_]+)",`)
	matches := entryRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("no libopus %s records found", arrayName)
	}

	names := make([]string, 0, len(matches))
	for _, m := range matches {
		if defined[m[1]] {
			names = append(names, m[1])
		}
	}
	if len(names) == 0 {
		t.Fatalf("no enabled libopus %s records found", arrayName)
	}
	return names
}
