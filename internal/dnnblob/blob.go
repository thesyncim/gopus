package dnnblob

import (
	"encoding/binary"
	"sort"
)

const (
	headerSize = 64

	weightTypeFloat = 0
	weightTypeInt8  = 3
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

var (
	requiredDecoderControlRecordNames = sortedRecordNames(
		pitchDNNRequiredRecordNames,
		plcRequiredRecordNames,
		farganRequiredRecordNames,
		osceLACERequiredRecordNames,
		osceNoLACERequiredRecordNames,
	)
	requiredDecoderControlWithBWERecordNames = sortedRecordNames(
		pitchDNNRequiredRecordNames,
		plcRequiredRecordNames,
		farganRequiredRecordNames,
		osceLACERequiredRecordNames,
		osceNoLACERequiredRecordNames,
		osceBWERequiredRecordNames,
	)
	requiredEncoderControlRecordNames = sortedRecordNames(
		pitchDNNRequiredRecordNames,
		dredEncoderRequiredRecordNames,
	)
	requiredStandaloneDREDDecoderRecordNames = sortedRecordNames(dredDecoderRequiredRecordNames)
)

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

func sortedRecordNames(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	out := make([]string, 0, total)
	for _, group := range groups {
		out = append(out, group...)
	}
	sort.Strings(out)
	return out
}

func (b *Blob) validateRecordNames(required []string) error {
	for _, want := range required {
		if !b.HasRecord(want) {
			return errInvalidBlob
		}
	}
	return nil
}

// SupportsPitchDNN reports whether the blob contains the pitch model family
// libopus uses from both DRED and PLC/FARGAN control loaders.
func (b *Blob) SupportsPitchDNN() bool {
	return b.validateRecordNames(pitchDNNRequiredRecordNames) == nil
}

// SupportsPLC reports whether the blob contains the PLC model family.
func (b *Blob) SupportsPLC() bool {
	return b.validateRecordNames(plcRequiredRecordNames) == nil
}

// SupportsFARGAN reports whether the blob contains the FARGAN model family.
func (b *Blob) SupportsFARGAN() bool {
	return b.validateRecordNames(farganRequiredRecordNames) == nil
}

// SupportsDREDEncoder reports whether the blob contains the DRED encoder model family.
func (b *Blob) SupportsDREDEncoder() bool {
	return b.validateRecordNames(dredEncoderRequiredRecordNames) == nil
}

// SupportsDREDDecoder reports whether the blob contains the DRED decoder model family.
func (b *Blob) SupportsDREDDecoder() bool {
	return b.validateRecordNames(dredDecoderRequiredRecordNames) == nil
}

// SupportsOSCELACE reports whether the blob contains the LACE OSCE model family.
func (b *Blob) SupportsOSCELACE() bool {
	return b.validateRecordNames(osceLACERequiredRecordNames) == nil
}

// SupportsOSCENoLACE reports whether the blob contains the NoLACE OSCE model family.
func (b *Blob) SupportsOSCENoLACE() bool {
	return b.validateRecordNames(osceNoLACERequiredRecordNames) == nil
}

// SupportsOSCE reports whether the blob contains the core OSCE model families.
func (b *Blob) SupportsOSCE() bool {
	return b.SupportsOSCELACE() && b.SupportsOSCENoLACE()
}

// SupportsOSCEBWE reports whether the blob contains the OSCE_BWE model family.
func (b *Blob) SupportsOSCEBWE() bool {
	return b.validateRecordNames(osceBWERequiredRecordNames) == nil
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

// ValidateDREDDecoderControl mirrors the standalone libopus DRED decoder
// model-loading path, which only requires the RDOVAE decoder family.
func (b *Blob) ValidateDREDDecoderControl() error {
	if !b.SupportsDREDDecoder() {
		return errInvalidBlob
	}
	return nil
}

// RequiredDecoderControlRecordNames returns a read-only view of the
// loader-derived record names the libopus main decoder path expects from
// OPUS_SET_DNN_BLOB.
func RequiredDecoderControlRecordNames(requireOSCEBWE bool) []string {
	if requireOSCEBWE {
		return requiredDecoderControlWithBWERecordNames
	}
	return requiredDecoderControlRecordNames
}

// RequiredEncoderControlRecordNames returns a read-only view of the
// loader-derived record names the libopus encoder path expects from
// OPUS_SET_DNN_BLOB.
func RequiredEncoderControlRecordNames() []string {
	return requiredEncoderControlRecordNames
}

// RequiredDREDDecoderRecordNames returns a read-only view of the loader-derived
// record names for the standalone libopus DRED decoder model family.
func RequiredDREDDecoderRecordNames() []string {
	return requiredStandaloneDREDDecoderRecordNames
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
