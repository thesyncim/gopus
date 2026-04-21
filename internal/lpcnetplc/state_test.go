package lpcnetplc

import "testing"

func TestStateLifecycle(t *testing.T) {
	var st State
	if st.Blend() != 0 {
		t.Fatalf("Blend()=%d want 0", st.Blend())
	}

	features := make([]float32, NumFeatures)
	for i := range features {
		features[i] = float32(i + 1)
	}
	st.FECAdd(features)
	st.FECAdd(nil)
	if st.FECFillPos() != 1 {
		t.Fatalf("FECFillPos()=%d want 1", st.FECFillPos())
	}
	if st.FECSkip() != 1 {
		t.Fatalf("FECSkip()=%d want 1", st.FECSkip())
	}

	var got [NumFeatures]float32
	if n := st.FillQueuedFeatures(0, got[:]); n != NumFeatures {
		t.Fatalf("FillQueuedFeatures count=%d want %d", n, NumFeatures)
	}
	for i, want := range features {
		if got[i] != want {
			t.Fatalf("queued[%d]=%v want %v", i, got[i], want)
		}
	}

	st.MarkConcealed()
	if st.Blend() != 1 {
		t.Fatalf("Blend()=%d want 1", st.Blend())
	}
	st.MarkUpdated()
	if st.Blend() != 0 {
		t.Fatalf("Blend()=%d want 0", st.Blend())
	}

	st.FECClear()
	if st.FECFillPos() != 0 || st.FECSkip() != 0 {
		t.Fatalf("post-clear = (fill=%d, skip=%d) want (0,0)", st.FECFillPos(), st.FECSkip())
	}

	st.MarkConcealed()
	st.Reset()
	if st.Blend() != 0 || st.FECFillPos() != 0 || st.FECSkip() != 0 {
		t.Fatalf("post-reset = (blend=%d, fill=%d, skip=%d) want (0,0,0)", st.Blend(), st.FECFillPos(), st.FECSkip())
	}
}

func TestFECAddDoesNotAllocate(t *testing.T) {
	var st State
	var features [NumFeatures]float32

	allocs := testing.AllocsPerRun(1000, func() {
		st.FECClear()
		st.FECAdd(features[:])
		st.FECAdd(nil)
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}
