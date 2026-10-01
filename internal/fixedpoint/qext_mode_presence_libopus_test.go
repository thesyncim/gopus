//go:build gopus_fixed_point && gopus_qext && gopus_custom_modes

package fixedpoint

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedQEXTBandModePresenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const magic = "GQPI"
	cases := []struct {
		fs, frame int
	}{
		{48000, 960}, {48000, 720}, {96000, 1920}, {96000, 1440},
		{24000, 480}, {12000, 240}, {32000, 640}, {16000, 320}, {8000, 160},
	}
	helper, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "fixed custom QEXT mode presence",
		OutputBase:  "gopus_custom_qext_mode_presence",
		SourceFile:  "libopus_custom_qext_mode_presence.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{"-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed custom QEXT mode presence", err)
		return
	}
	var input bytes.Buffer
	input.WriteString(magic)
	_ = binary.Write(&input, binary.LittleEndian, uint32(len(cases)))
	for _, tc := range cases {
		_ = binary.Write(&input, binary.LittleEndian, uint32(tc.fs))
		_ = binary.Write(&input, binary.LittleEndian, uint32(tc.frame))
	}
	output, err := libopustest.RunHelper(helper, input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(output)
	var gotMagic [4]byte
	if _, err := r.Read(gotMagic[:]); err != nil || string(gotMagic[:]) != "GQPO" {
		t.Fatalf("QEXT oracle response magic=%q error=%v", gotMagic, err)
	}
	read := func() uint32 {
		t.Helper()
		var v uint32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if count := read(); count != uint32(len(cases)) {
		t.Fatalf("QEXT oracle response count=%d want=%d", count, len(cases))
	}
	for _, tc := range cases {
		short, cacheSize, pointers := int(read()), int(read()), read()
		t.Run(fmt.Sprintf("fs%d_n%d_short%d", tc.fs, tc.frame, short), func(t *testing.T) {
			_, edges, logN, qextEnd, ok := fixedQEXTBandMode(tc.fs, short)
			cHasMode := cacheSize > 0 && pointers == 7
			if ok != cHasMode {
				t.Fatalf("Go QEXT side mode=%t, selected C cache size=%d pointers=%03b", ok, cacheSize, pointers)
			}
			if ok {
				if len(edges) != 15 || len(logN) != 14 || qextEnd == 0 {
					t.Fatalf("Go QEXT mode has incomplete side geometry")
				}
			} else if edges != nil || logN != nil || qextEnd != 0 || cacheSize != 0 || pointers != 0 {
				t.Fatalf("unsupported mode has Go side geometry or selected C cache: size=%d pointers=%03b", cacheSize, pointers)
			}
		})
	}
	if r.Len() != 0 {
		t.Fatalf("QEXT oracle has %d trailing bytes", r.Len())
	}
}
