//go:build gopus_fixed_point && gopus_qext && !gopus_dred && !gopus_osce

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedHybridQEXTPayloadMatchesSelectedLibopus(t *testing.T) {
	packets, want := selectedHybridQEXTSequence(t, libopustest.DecodeDiffFormatInt24)
	decoder, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	for i, packet := range packets {
		got, err := decoder.DecodeToInt24(packet, hybridQEXTFrameSize)
		if want[i].Code < 0 {
			if err == nil {
				t.Fatalf("step %d malformed packet returned %d samples without an error", i, len(got))
			}
			if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
				t.Fatalf("step %d malformed final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
			}
			continue
		}
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		pcm := want[i].Int24()
		if int32(len(got)) != want[i].Code || len(got) != len(pcm) {
			t.Fatalf("step %d sample count Go=%d C=%d", i, len(got), want[i].Code)
		}
		for sample := range got {
			if got[sample] != pcm[sample] {
				t.Fatalf("step %d sample %d Go=%d C=%d", i, sample, got[sample], pcm[sample])
			}
		}
		if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
			t.Fatalf("step %d final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
		}
	}
}

func TestFixedHybridQEXTPayloadWarmZeroAllocs(t *testing.T) {
	packets, _ := selectedHybridQEXTSequence(t, libopustest.DecodeDiffFormatInt24)
	decoder, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	sequence := func() bool {
		for _, index := range []int{0, 3} {
			pcm, handled, err := decoder.DecodeToResFixed(packets[index], hybridQEXTFrameSize)
			if err != nil || !handled || len(pcm) != hybridQEXTFrameSize {
				return false
			}
		}
		return true
	}
	for range 3 {
		if !sequence() {
			t.Fatal("warm fixed Hybrid QEXT decode sequence failed")
		}
	}
	failed := false
	allocs := testing.AllocsPerRun(20, func() {
		if !sequence() {
			failed = true
		}
	})
	if failed {
		t.Fatal("fixed Hybrid QEXT decode sequence failed during allocation measurement")
	}
	if allocs != 0 {
		t.Fatalf("warm fixed Hybrid QEXT DecodeToResFixed allocs/op=%.2f, want 0", allocs)
	}
}
