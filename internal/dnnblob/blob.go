package dnnblob

import "encoding/binary"

const (
	headerSize = 64

	weightTypeFloat   = 0
	weightTypeInt     = 1
	weightTypeQWeight = 2
	weightTypeInt8    = 3
)

// Record mirrors one libopus WeightArray entry parsed from a weights blob.
type Record struct {
	Name string
	Type int32
	Size int32
	Data []byte
}

// Blob stores a validated copy of a libopus-style weights blob and its records.
type Blob struct {
	Raw     []byte
	Records []Record
}

// DecoderModelState summarizes which decoder-side model families are present in
// a validated weights blob.
type DecoderModelState struct {
	PitchDNN bool
	PLC      bool
	FARGAN   bool
	DRED     bool
	OSCE     bool
	OSCEBWE  bool
}

// Clone validates data using libopus parse_weights-style framing rules and
// returns a persistent copy whose record slices point into the copied buffer.
func Clone(data []byte) (*Blob, error) {
	raw := append([]byte(nil), data...)
	records, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return &Blob{Raw: raw, Records: records}, nil
}

// HasRecord reports whether the parsed blob contains a record with the given name.
func (b *Blob) HasRecord(name string) bool {
	if b == nil {
		return false
	}
	for _, rec := range b.Records {
		if rec.Name == name {
			return true
		}
	}
	return false
}

func (b *Blob) hasRecordExact(name string, typ int32, size int32) bool {
	if b == nil {
		return false
	}
	for _, rec := range b.Records {
		if rec.Name == name && rec.Type == typ && rec.Size == size {
			return true
		}
	}
	return false
}

type requiredRecord struct {
	name string
	typ  int32
	size int32
}

// These source-derived sentinels mirror the model families loaded by libopus
// 1.6.1's OPUS_SET_DNN_BLOB control handlers.
var (
	pitchDNNRequiredRecords = []requiredRecord{
		{name: "dense_if_upsampler_1_bias", typ: weightTypeFloat, size: 64 * 4},
	}
	plcRequiredRecords = []requiredRecord{
		{name: "plc_dense_in_bias", typ: weightTypeFloat, size: 128 * 4},
	}
	farganRequiredRecords = []requiredRecord{
		{name: "cond_net_pembed_bias", typ: weightTypeFloat, size: 12 * 4},
	}
	dredEncoderRequiredRecords = []requiredRecord{
		{name: "enc_dense1_bias", typ: weightTypeFloat, size: 64 * 4},
	}
	dredDecoderRequiredRecords = []requiredRecord{
		{name: "dec_dense1_bias", typ: weightTypeFloat, size: 64 * 4},
	}
	osceLACERequiredRecords = []requiredRecord{
		{name: "lace_pitch_embedding_bias", typ: weightTypeFloat, size: 64 * 4},
	}
	osceNoLACERequiredRecords = []requiredRecord{
		{name: "nolace_pitch_embedding_bias", typ: weightTypeFloat, size: 64 * 4},
	}
	osceBWERequiredRecords = []requiredRecord{
		{name: "bbwenet_fnet_conv1_bias", typ: weightTypeFloat, size: 128 * 4},
	}
)

func (b *Blob) validateRecords(required []requiredRecord) error {
	for _, want := range required {
		if !b.hasRecordExact(want.name, want.typ, want.size) {
			return errInvalidBlob
		}
	}
	return nil
}

// SupportsPitchDNN reports whether the blob contains the pitch model family
// libopus uses from both DRED and PLC/FARGAN control loaders.
func (b *Blob) SupportsPitchDNN() bool {
	return b.validateRecords(pitchDNNRequiredRecords) == nil
}

// SupportsPLC reports whether the blob contains the PLC model family.
func (b *Blob) SupportsPLC() bool {
	return b.validateRecords(plcRequiredRecords) == nil
}

// SupportsFARGAN reports whether the blob contains the FARGAN model family.
func (b *Blob) SupportsFARGAN() bool {
	return b.validateRecords(farganRequiredRecords) == nil
}

// SupportsDREDEncoder reports whether the blob contains the DRED encoder model family.
func (b *Blob) SupportsDREDEncoder() bool {
	return b.validateRecords(dredEncoderRequiredRecords) == nil
}

// SupportsDREDDecoder reports whether the blob contains the DRED decoder model family.
func (b *Blob) SupportsDREDDecoder() bool {
	return b.validateRecords(dredDecoderRequiredRecords) == nil
}

// SupportsOSCELACE reports whether the blob contains the LACE OSCE model family.
func (b *Blob) SupportsOSCELACE() bool {
	return b.validateRecords(osceLACERequiredRecords) == nil
}

// SupportsOSCENoLACE reports whether the blob contains the NoLACE OSCE model family.
func (b *Blob) SupportsOSCENoLACE() bool {
	return b.validateRecords(osceNoLACERequiredRecords) == nil
}

// SupportsOSCE reports whether the blob contains the core OSCE model families.
func (b *Blob) SupportsOSCE() bool {
	return b.SupportsOSCELACE() && b.SupportsOSCENoLACE()
}

// SupportsOSCEBWE reports whether the blob contains the OSCE_BWE model family.
func (b *Blob) SupportsOSCEBWE() bool {
	return b.validateRecords(osceBWERequiredRecords) == nil
}

// DecoderModels reports which decoder-side model families are available from
// the retained blob.
func (b *Blob) DecoderModels() DecoderModelState {
	return DecoderModelState{
		PitchDNN: b.SupportsPitchDNN(),
		PLC:      b.SupportsPLC(),
		FARGAN:   b.SupportsFARGAN(),
		DRED:     b.SupportsDREDDecoder(),
		OSCE:     b.SupportsOSCE(),
		OSCEBWE:  b.SupportsOSCEBWE(),
	}
}

// ValidateEncoderControl mirrors the libopus encoder DNN-blob surface by
// requiring the model families needed for DRED encoder loading.
func (b *Blob) ValidateEncoderControl() error {
	if !b.SupportsDREDEncoder() || !b.SupportsPitchDNN() {
		return errInvalidBlob
	}
	return nil
}

// ValidateDecoderControl mirrors the libopus decoder DNN-blob surface by
// requiring the model families needed for PLC/FARGAN and OSCE loading.
func (b *Blob) ValidateDecoderControl(requireOSCEBWE bool) error {
	models := b.DecoderModels()
	if !models.PLC || !models.PitchDNN || !models.FARGAN || !models.OSCE {
		return errInvalidBlob
	}
	if requireOSCEBWE && !models.OSCEBWE {
		return errInvalidBlob
	}
	return nil
}

func parse(data []byte) ([]Record, error) {
	records := make([]Record, 0, 4)
	offset := 0
	for offset < len(data) {
		remaining := len(data) - offset
		if remaining < headerSize {
			return nil, errInvalidBlob
		}

		hdr := data[offset : offset+headerSize]
		typ := int32(binary.LittleEndian.Uint32(hdr[8:12]))
		size := int32(binary.LittleEndian.Uint32(hdr[12:16]))
		blockSize := int32(binary.LittleEndian.Uint32(hdr[16:20]))
		if size < 0 || blockSize < size {
			return nil, errInvalidBlob
		}
		if int(blockSize) > remaining-headerSize {
			return nil, errInvalidBlob
		}

		nameBytes := hdr[20:64]
		if nameBytes[len(nameBytes)-1] != 0 {
			return nil, errInvalidBlob
		}
		nameLen := 0
		for nameLen < len(nameBytes) && nameBytes[nameLen] != 0 {
			nameLen++
		}
		dataStart := offset + headerSize
		dataEnd := dataStart + int(size)
		records = append(records, Record{
			Name: string(nameBytes[:nameLen]),
			Type: typ,
			Size: size,
			Data: data[dataStart:dataEnd],
		})
		offset += headerSize + int(blockSize)
	}
	if offset != len(data) {
		return nil, errInvalidBlob
	}
	return records, nil
}
