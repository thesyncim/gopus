//go:build !gopus_dred && !gopus_osce && !gopus_qext && !gopus_fixed_point && !gopus_custom_modes && (amd64 || arm64)

package gopus_test

import (
	"reflect"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/silk"
)

func typePtrStats(t reflect.Type) (size uintptr, ptrdata uintptr, ptrCount int) {
	size = t.Size()
	var walk func(t reflect.Type, base uintptr)
	walk = func(t reflect.Type, base uintptr) {
		switch t.Kind() {
		case reflect.Pointer, reflect.UnsafePointer, reflect.Map, reflect.Chan, reflect.Func:
			ptrCount++
			if end := base + t.Size(); end > ptrdata {
				ptrdata = end
			}
		case reflect.Slice, reflect.String:
			ptrCount++
			if end := base + uintptr(reflect.TypeOf((*byte)(nil)).Size()); end > ptrdata {
				ptrdata = end
			}
		case reflect.Interface:
			ptrCount += 2
			if end := base + t.Size(); end > ptrdata {
				ptrdata = end
			}
		case reflect.Array:
			if t.Len() == 0 {
				return
			}
			elem := t.Elem()
			_, epd, epc := typePtrStats(elem)
			if epc == 0 {
				return
			}
			ptrCount += epc * t.Len()
			last := base + uintptr(t.Len()-1)*elem.Size() + epd
			if last > ptrdata {
				ptrdata = last
			}
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				walk(f.Type, base+f.Offset)
			}
		}
	}
	walk(t, 0)
	return size, ptrdata, ptrCount
}

// TestGCLayoutPtrdataBudgets guards the pointer/noscan tail layout of core
// long-lived structs so large scalar arrays stay outside Go's GC scan prefix.
func TestGCLayoutPtrdataBudgets(t *testing.T) {
	cases := []struct {
		name        string
		sample      any
		maxPtrRatio float64
		maxPtrCount int
	}{
		{name: "Decoder", sample: gopus.Decoder{}, maxPtrRatio: 0.45},
		{name: "Encoder", sample: gopus.Encoder{}, maxPtrRatio: 0.10},
		{name: "Repacketizer", sample: gopus.Repacketizer{}, maxPtrRatio: 0.10, maxPtrCount: 2},
		{name: "silk.Decoder", sample: silk.Decoder{}, maxPtrRatio: 0.20},
		{name: "silk.Encoder", sample: silk.Encoder{}, maxPtrRatio: 0.30},
		{name: "encoder.Encoder", sample: encoder.Encoder{}, maxPtrRatio: 0.10},
		{name: "ogg.Writer", sample: ogg.Writer{}, maxPtrRatio: 0.10},
	}

	for _, tc := range cases {
		rt := reflect.TypeOf(tc.sample)
		sz, pd, pc := typePtrStats(rt)
		ratio := float64(pd) / float64(sz)
		if ratio > tc.maxPtrRatio {
			t.Errorf("%s ptrdata=%d / size=%d (%.1f%%) exceeds budget %.1f%%",
				tc.name, pd, sz, ratio*100, tc.maxPtrRatio*100)
		}
		if tc.maxPtrCount > 0 && pc > tc.maxPtrCount {
			t.Errorf("%s pointer count=%d exceeds budget %d", tc.name, pc, tc.maxPtrCount)
		}
	}
}
