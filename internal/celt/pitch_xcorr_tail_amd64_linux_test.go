//go:build amd64 && goexperiment.simd && linux && !nosimd

package celt

import (
	"math"
	"os"
	"simd/archsimd"
	"syscall"
	"testing"
	"unsafe"
)

func TestXcorrKernelAVX8PageEndTails(t *testing.T) {
	if !archsimd.X86.FMA() {
		if os.Getenv("GOPUS_REQUIRE_XCORR_SIMD") == "1" {
			t.Fatal("native CELT xcorr SIMD job requires FMA")
		}
		t.Skip("FMA unavailable; CELT xcorr kernel selects its scalar path")
	}

	pageSize := os.Getpagesize()
	memory, err := syscall.Mmap(-1, 0, 4*pageSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatalf("mmap guard pages: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Munmap(memory) })
	if err := syscall.Mprotect(memory[pageSize:2*pageSize], syscall.PROT_NONE); err != nil {
		t.Fatalf("protect x guard page: %v", err)
	}
	if err := syscall.Mprotect(memory[3*pageSize:], syscall.PROT_NONE); err != nil {
		t.Fatalf("protect y guard page: %v", err)
	}

	xPage := memory[:pageSize]
	yPage := memory[2*pageSize : 3*pageSize]
	for length := 1; length <= 17; length++ {
		yLength := length + 7
		x := unsafe.Slice((*float32)(unsafe.Pointer(&xPage[len(xPage)-4*length])), length)
		y := unsafe.Slice((*float32)(unsafe.Pointer(&yPage[len(yPage)-4*yLength])), yLength)
		for i := range x {
			x[i] = float32(i)*0.25 - 0.5
		}
		for i := range y {
			y[i] = float32(i)*0.125 - 0.25
		}

		want := xcorrKernelAVX8Scalar(x, y, length)
		var got [8]float32
		xcorrKernelAVX8(&x[0], &y[0], &got, length)
		for corr := range 8 {
			if math.Float32bits(got[corr]) != math.Float32bits(want[corr]) {
				t.Fatalf("length=%d corr=%d: got %08x want %08x", length, corr, math.Float32bits(got[corr]), math.Float32bits(want[corr]))
			}
		}
	}
}
