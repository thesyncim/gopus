package rangecoding

import (
	"reflect"
	"testing"
)

func TestRangeCoderStateFieldWidthsMatchLibopusECCTX(t *testing.T) {
	// libopus celt/entcode.h: ec_ctx keeps storage/end_offs/offs/rng/val/ext
	// as opus_uint32 and nend_bits/nbits_total/rem/error as C int.
	uint32Type := reflect.TypeOf(uint32(0))
	int32Type := reflect.TypeOf(int32(0))
	for _, tc := range []struct {
		name   string
		target any
		fields map[string]reflect.Type
	}{
		{
			name:   "Encoder",
			target: Encoder{},
			fields: map[string]reflect.Type{
				"storage": uint32Type, "endOffs": uint32Type, "offs": uint32Type,
				"endWindow": uint32Type, "rng": uint32Type, "val": uint32Type, "ext": uint32Type,
				"nendBits": int32Type, "nbitsTotal": int32Type,
				"rem": int32Type, "err": int32Type,
			},
		},
		{
			name:   "EncoderState",
			target: EncoderState{},
			fields: map[string]reflect.Type{
				"storage": uint32Type, "endOffs": uint32Type, "offs": uint32Type,
				"endWindow": uint32Type, "rng": uint32Type, "val": uint32Type, "ext": uint32Type,
				"nendBits": int32Type, "nbitsTotal": int32Type,
				"rem": int32Type, "err": int32Type,
			},
		},
		{
			name:   "Decoder",
			target: Decoder{},
			fields: map[string]reflect.Type{
				"storage": uint32Type, "endOffs": uint32Type, "offs": uint32Type,
				"endWindow": uint32Type, "rng": uint32Type, "val": uint32Type, "ext": uint32Type,
				"nendBits": int32Type, "nbitsTotal": int32Type,
				"rem": int32Type, "err": int32Type,
			},
		},
	} {
		typ := reflect.TypeOf(tc.target)
		for field, want := range tc.fields {
			got, ok := typ.FieldByName(field)
			if !ok {
				t.Fatalf("%s.%s missing", tc.name, field)
			}
			if got.Type != want {
				t.Fatalf("%s.%s type=%s want %s", tc.name, field, got.Type, want)
			}
		}
	}
}
