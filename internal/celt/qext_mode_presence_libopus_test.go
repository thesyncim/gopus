//go:build gopus_qext && gopus_custom_modes

package celt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTModePresenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct{ fs, frame int }{
		{48000, 960}, {48000, 720}, {96000, 1920}, {96000, 1440},
		{24000, 480}, {12000, 240}, {32000, 640}, {16000, 320}, {8000, 160},
	}
	helper, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "CELT custom QEXT mode presence",
		OutputBase:  "gopus_celt_custom_qext_presence",
		SourceFile:  "libopus_custom_qext_mode_presence.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{"-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT custom QEXT mode presence", err)
		return
	}
	var request bytes.Buffer
	request.WriteString("GQPI")
	_ = binary.Write(&request, binary.LittleEndian, uint32(len(cases)))
	for _, tc := range cases {
		_ = binary.Write(&request, binary.LittleEndian, uint32(tc.fs))
		_ = binary.Write(&request, binary.LittleEndian, uint32(tc.frame))
	}
	output, err := libopustest.RunHelper(helper, request.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(output)
	var magic [4]byte
	if _, err := r.Read(magic[:]); err != nil || string(magic[:]) != "GQPO" {
		t.Fatalf("QEXT presence oracle magic=%q error=%v", magic, err)
	}
	read := func() uint32 {
		t.Helper()
		var value uint32
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if count := read(); int(count) != len(cases) {
		t.Fatalf("QEXT presence oracle records=%d want=%d", count, len(cases))
	}
	for _, tc := range cases {
		short, cacheSize, pointers := int(read()), int(read()), read()
		t.Run(fmt.Sprintf("fs%d_n%d_short%d", tc.fs, tc.frame, short), func(t *testing.T) {
			cfg, ok := computeQEXTModeConfig(tc.fs, short)
			cHasMode := cacheSize > 0 && pointers == 7
			if ok != cHasMode {
				t.Fatalf("Go side mode=%t, selected C cache size=%d pointers=%03b", ok, cacheSize, pointers)
			}
			if ok {
				if len(cfg.EBands) != 15 || len(cfg.LogN) != 14 || cfg.EffBands <= 0 {
					t.Fatal("Go QEXT mode has incomplete side geometry")
				}
			} else if cfg.EBands != nil || cfg.LogN != nil || cfg.EffBands != 0 || cacheSize != 0 || pointers != 0 {
				t.Fatalf("unsupported mode has Go side geometry or C side cache: size=%d pointers=%03b", cacheSize, pointers)
			}
		})
	}
	if r.Len() != 0 {
		t.Fatalf("QEXT presence oracle has %d trailing bytes", r.Len())
	}
}
