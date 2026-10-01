package celt

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"unsafe"
)

// scratchSliceState is one slice field of the encoder scratch after
// ensureScratch: its header and whether its elements are all zero.
type scratchSliceState struct {
	ptr      uintptr
	len, cap int
	zero     bool
}

// scratchSliceFields returns every slice-typed field reachable from v through
// embedded and nested structs, keyed by field path.
func scratchSliceFields(v reflect.Value, path string, out map[string]reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			scratchSliceFields(v.Field(i), path+"."+v.Type().Field(i).Name, out)
		}
	case reflect.Slice:
		out[path] = reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	}
}

// encoderScratchSlices returns the slice fields ensureScratch sizes.
func encoderScratchSlices(e *Encoder) map[string]reflect.Value {
	out := map[string]reflect.Value{}
	scratchSliceFields(reflect.ValueOf(&e.scratch).Elem(), "scratch", out)
	scratchSliceFields(reflect.ValueOf(&e.bandEncScratch).Elem(), "bandEncScratch", out)
	scratchSliceFields(reflect.ValueOf(&e.tfScratch).Elem(), "tfScratch", out)
	return out
}

// fillScratch writes a nonzero pattern over the full capacity of every
// numeric scratch slice, so a later zero fill is observable.
func fillScratch(slices map[string]reflect.Value) {
	for _, s := range slices {
		full := s.Slice3(0, s.Cap(), s.Cap())
		for i := 0; i < full.Len(); i++ {
			el := full.Index(i)
			switch el.Kind() {
			case reflect.Float32, reflect.Float64:
				el.SetFloat(1.5)
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
				el.SetInt(3)
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				el.SetUint(3)
			case reflect.Complex64:
				el.SetComplex(1.5)
			}
		}
	}
}

func captureScratch(slices map[string]reflect.Value) map[string]scratchSliceState {
	out := map[string]scratchSliceState{}
	for name, s := range slices {
		st := scratchSliceState{len: s.Len(), cap: s.Cap(), zero: true}
		if s.Cap() > 0 {
			st.ptr = s.Slice3(0, s.Cap(), s.Cap()).Index(0).Addr().Pointer()
		}
		for i := 0; i < s.Len(); i++ {
			if !s.Index(i).IsZero() {
				st.zero = false
				break
			}
		}
		out[name] = st
	}
	return out
}

// TestEncoderScratchResetMatchesSizing checks that the ensureScratch fast path
// for an unchanged scratch shape (resetScratchLengths) leaves every scratch
// slice with the same backing array, length and zero fill as the full sizing
// path, starting from the lengths a real encode leaves behind.
func TestEncoderScratchResetMatchesSizing(t *testing.T) {
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{120, 240, 480, 960} {
			for _, complexity := range []int{3, 10} {
				t.Run(fmt.Sprintf("ch%d_n%d_c%d", channels, frameSize, complexity), func(t *testing.T) {
					e := NewEncoder(channels)
					e.SetComplexity(complexity)
					rng := rand.New(rand.NewSource(int64(channels*1000 + frameSize + complexity)))
					pcm := make([]float32, frameSize*channels)
					for f := 0; f < 3; f++ {
						for i := range pcm {
							pcm[i] = float32(rng.NormFloat64() * 0.2)
						}
						if _, err := e.EncodeFrame(pcm, frameSize); err != nil {
							t.Fatal(err)
						}
					}
					if !e.scratchShapeValid {
						t.Fatal("scratch shape is not cached after encoding")
					}
					savedScratch, savedBand, savedTF := e.scratch, e.bandEncScratch, e.tfScratch

					run := func(fast bool) map[string]scratchSliceState {
						e.scratch, e.bandEncScratch, e.tfScratch = savedScratch, savedBand, savedTF
						fillScratch(encoderScratchSlices(e))
						e.scratchShapeValid = fast
						e.ensureScratch(frameSize)
						return captureScratch(encoderScratchSlices(e))
					}
					full := run(false)
					fast := run(true)
					for name, want := range full {
						if got := fast[name]; got != want {
							t.Errorf("%s: fast path %+v, sizing path %+v", name, got, want)
						}
					}
				})
			}
		}
	}
}
