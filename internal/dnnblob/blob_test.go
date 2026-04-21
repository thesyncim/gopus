package dnnblob

import (
	"encoding/binary"
	"testing"
)

func makeTestBlobRecord(name string, typ int32, payload []byte) []byte {
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[4:8], 0)
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	copy(out[headerSize:], payload)
	return out
}

func TestCloneParsesRecords(t *testing.T) {
	payload := append(makeTestBlobRecord("alpha", weightTypeFloat, []byte{1, 2, 3, 4}),
		makeTestBlobRecord("beta", weightTypeInt8, []byte{9, 8, 7})...)

	blob, err := Clone(payload)
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if len(blob.Records) != 2 {
		t.Fatalf("record count=%d want 2", len(blob.Records))
	}
	if blob.Records[0].Name != "alpha" || blob.Records[0].Type != weightTypeFloat || blob.Records[0].Size != 4 {
		t.Fatalf("record[0]=%+v", blob.Records[0])
	}
	if string(blob.Records[0].Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("record[0].Data=%v", blob.Records[0].Data)
	}
	if blob.Records[1].Name != "beta" || blob.Records[1].Type != weightTypeInt8 || blob.Records[1].Size != 3 {
		t.Fatalf("record[1]=%+v", blob.Records[1])
	}
	if !blob.HasRecord("alpha") || !blob.HasRecord("beta") || blob.HasRecord("gamma") {
		t.Fatal("HasRecord mismatch")
	}
}

func TestCloneRejectsMalformedBlob(t *testing.T) {
	tests := []struct {
		name string
		blob []byte
	}{
		{name: "short", blob: []byte{1, 2, 3}},
		{name: "block smaller than size", blob: func() []byte {
			out := makeTestBlobRecord("bad", weightTypeFloat, []byte{1, 2, 3, 4})
			binary.LittleEndian.PutUint32(out[16:20], 2)
			return out
		}()},
		{name: "truncated payload", blob: func() []byte {
			out := makeTestBlobRecord("bad", weightTypeFloat, []byte{1, 2, 3, 4})
			return out[:len(out)-1]
		}()},
		{name: "missing nul terminator", blob: func() []byte {
			out := makeTestBlobRecord("bad", weightTypeFloat, []byte{1})
			out[63] = 'x'
			return out
		}()},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Clone(tc.blob); err == nil {
				t.Fatal("Clone error=nil want non-nil")
			}
		})
	}
}

func TestValidateEncoderControl(t *testing.T) {
	blob, err := Clone(append(
		makeTestBlobRecord("enc_dense1_bias", weightTypeFloat, make([]byte, 64*4)),
		makeTestBlobRecord("dense_if_upsampler_1_bias", weightTypeFloat, make([]byte, 64*4))...,
	))
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if !blob.SupportsDREDEncoder() || !blob.SupportsPitchDNN() {
		t.Fatal("encoder blob missing expected capabilities")
	}
	if blob.SupportsPLC() || blob.SupportsFARGAN() || blob.SupportsOSCELACE() || blob.SupportsOSCENoLACE() || blob.SupportsOSCEBWE() {
		t.Fatal("encoder blob unexpectedly advertises decoder capabilities")
	}
	if err := blob.ValidateEncoderControl(); err != nil {
		t.Fatalf("ValidateEncoderControl error: %v", err)
	}

	missingPitch, err := Clone(makeTestBlobRecord("enc_dense1_bias", weightTypeFloat, make([]byte, 64*4)))
	if err != nil {
		t.Fatalf("Clone missingPitch error: %v", err)
	}
	if err := missingPitch.ValidateEncoderControl(); err == nil {
		t.Fatal("ValidateEncoderControl error=nil want non-nil")
	}
}

func TestValidateDecoderControl(t *testing.T) {
	build := func(names []string) []byte {
		var blob []byte
		for _, name := range names {
			payloadSize := 4
			switch name {
			case "dense_if_upsampler_1_bias", "dec_dense1_bias":
				payloadSize = 64 * 4
			}
			blob = append(blob, makeTestBlobRecord(name, weightTypeFloat, make([]byte, payloadSize))...)
		}
		return blob
	}

	blob, err := Clone(build(RequiredDecoderControlRecordNames(true)))
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if !blob.SupportsPLC() || !blob.SupportsPitchDNN() || !blob.SupportsFARGAN() || !blob.SupportsOSCELACE() || !blob.SupportsOSCENoLACE() || !blob.SupportsOSCEBWE() {
		t.Fatal("decoder blob missing expected capabilities")
	}
	if blob.SupportsDREDEncoder() || blob.SupportsDREDDecoder() {
		t.Fatal("decoder blob unexpectedly advertises DRED capabilities")
	}
	if err := blob.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(false) error: %v", err)
	}
	if err := blob.ValidateDecoderControl(true); err != nil {
		t.Fatalf("ValidateDecoderControl(true) error: %v", err)
	}

	missingBWE, err := Clone(build(RequiredDecoderControlRecordNames(false)))
	if err != nil {
		t.Fatalf("Clone missingBWE error: %v", err)
	}
	if missingBWE.SupportsOSCEBWE() {
		t.Fatal("missingBWE unexpectedly advertises OSCE_BWE capability")
	}
	if err := missingBWE.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(false) with no BWE error: %v", err)
	}
	if err := missingBWE.ValidateDecoderControl(true); err == nil {
		t.Fatal("ValidateDecoderControl(true) error=nil want non-nil")
	}
}

func TestValidateDecoderControlWithGeneratedManifestNames(t *testing.T) {
	build := func(names []string) []byte {
		var blob []byte
		for _, name := range names {
			payloadSize := 4
			if name == "dense_if_upsampler_1_bias" {
				payloadSize = 64 * 4
			}
			blob = append(blob, makeTestBlobRecord(name, weightTypeFloat, make([]byte, payloadSize))...)
		}
		return blob
	}

	blob, err := Clone(build(RequiredDecoderControlRecordNames(false)))
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if err := blob.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(false) error: %v", err)
	}

	names := RequiredDecoderControlRecordNames(false)
	missingOne, err := Clone(build(names[:len(names)-1]))
	if err != nil {
		t.Fatalf("Clone missingOne error: %v", err)
	}
	if err := missingOne.ValidateDecoderControl(false); err == nil {
		t.Fatal("ValidateDecoderControl(false) error=nil want non-nil for incomplete manifest")
	}
}

func TestDecoderModels(t *testing.T) {
	var blobData []byte
	for _, name := range append(RequiredDecoderControlRecordNames(true), "dec_dense1_bias") {
		payloadSize := 4
		switch name {
		case "dense_if_upsampler_1_bias", "dec_dense1_bias":
			payloadSize = 64 * 4
		}
		blobData = append(blobData, makeTestBlobRecord(name, weightTypeFloat, make([]byte, payloadSize))...)
	}

	blob, err := Clone(blobData)
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}

	models := blob.DecoderModels()
	if !models.PitchDNN || !models.PLC || !models.FARGAN || !models.DRED || !models.OSCE || !models.OSCEBWE {
		t.Fatalf("DecoderModels()=%+v want all capabilities", models)
	}
	if !blob.SupportsOSCE() {
		t.Fatal("SupportsOSCE()=false want true")
	}

	var nilBlob *Blob
	if got := nilBlob.DecoderModels(); got != (DecoderModelState{}) {
		t.Fatalf("nil DecoderModels()=%+v want zero value", got)
	}
	if nilBlob.SupportsOSCE() {
		t.Fatal("nil SupportsOSCE()=true want false")
	}
}
