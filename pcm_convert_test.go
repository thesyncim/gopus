package gopus

import (
	"runtime"
	"testing"
)

func TestConvertFloat32ToInt16Unit(t *testing.T) {
	src := []float32{-1, -0.75, -0.5, -1.5 / 32768, -0.5 / 32768, 0, 0.5 / 32768, 1.5 / 32768, 0.5, 0.75, 0.99999, 1}
	dst := make([]int16, len(src))
	ok := convertFloat32ToInt16Unit(dst, src, len(src))
	if runtime.GOARCH != "arm64" {
		if ok {
			t.Fatal("default conversion unexpectedly handled the vector")
		}
		return
	}
	if !ok {
		t.Fatal("arm64 conversion rejected in-range samples")
	}
	for i, v := range src {
		if want := float32ToInt16(v); dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}

	outOfRange := []float32{0, 1.01}
	if convertFloat32ToInt16Unit(make([]int16, len(outOfRange)), outOfRange, len(outOfRange)) {
		t.Fatal("arm64 conversion accepted out-of-range samples")
	}
}
