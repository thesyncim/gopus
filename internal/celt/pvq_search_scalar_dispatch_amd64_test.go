//go:build amd64 && (!goexperiment.simd || nosimd)

package celt

import "testing"

func TestPVQSearchScalarDispatch(t *testing.T) {
	if useX86PVQSearchSSE2 {
		t.Fatal("scalar build selected the Go SIMD PVQ path")
	}
	t.Log("Go PVQ dispatch: scalar path")
}
