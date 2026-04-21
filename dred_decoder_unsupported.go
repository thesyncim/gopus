//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
)

// ErrDREDModelNotLoaded reports that the tag-gated standalone DRED decoder
// metadata path has not been armed with a DRED decoder model blob yet.
var ErrDREDModelNotLoaded = errors.New("gopus: DRED decoder model not loaded")

// DREDRequest mirrors the low-cost opus_dred_parse() request parameters that
// affect how much cached redundancy is usable.
type DREDRequest = internaldred.Request

// DREDAvailability summarizes the request-bounded DRED coverage available from
// a parsed payload.
type DREDAvailability = internaldred.Availability

// DREDFeatureWindow mirrors the feature-offset window libopus derives from a
// parsed DRED result and a concealment request.
type DREDFeatureWindow = internaldred.FeatureWindow

// DREDParsed is the retained low-cost DRED metadata parsed from an Opus packet.
type DREDParsed = internaldred.Parsed

// DREDResult bundles parsed DRED metadata with request-bounded availability.
type DREDResult = internaldred.Result

// DREDDecoder mirrors libopus's standalone OpusDREDDecoder control lifetime for
// the tag-gated DRED metadata surface.
type DREDDecoder struct {
	dnnBlob     *dnnblob.Blob
	modelLoaded bool
}

// NewDREDDecoder constructs a tag-gated standalone DRED decoder wrapper.
func NewDREDDecoder() *DREDDecoder {
	return &DREDDecoder{}
}

// SetDNNBlob loads and validates a standalone DRED decoder model blob, matching
// the quarantined libopus OpusDREDDecoder DNN-blob control lifetime.
func (d *DREDDecoder) SetDNNBlob(data []byte) error {
	if d == nil {
		return ErrInvalidArgument
	}
	if data == nil {
		d.dnnBlob = nil
		d.modelLoaded = false
		return nil
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		return ErrInvalidArgument
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		return ErrInvalidArgument
	}
	d.dnnBlob = blob
	d.modelLoaded = true
	return nil
}

// ModelLoaded reports whether a DRED decoder model blob is currently retained.
func (d *DREDDecoder) ModelLoaded() bool {
	return d != nil && d.modelLoaded
}

// DRED mirrors the retained standalone OpusDRED packet state used by the
// experimental DRED metadata wrapper.
type DRED struct {
	data         [internaldred.MaxDataSize]byte
	cache        internaldred.Cache
	processStage int
}

// NewDRED constructs an empty standalone DRED state wrapper.
func NewDRED() *DRED {
	return &DRED{}
}

// Clear resets the retained DRED payload and process stage.
func (d *DRED) Clear() {
	if d == nil {
		return
	}
	d.cache.Clear()
	d.processStage = 0
}

// Empty reports whether any DRED payload is currently retained.
func (d *DRED) Empty() bool {
	return d == nil || d.cache.Empty()
}

// Len reports the retained payload size in bytes.
func (d *DRED) Len() int {
	if d == nil {
		return 0
	}
	return d.cache.Len
}

// Parsed returns the retained low-cost DRED metadata.
func (d *DRED) Parsed() DREDParsed {
	if d == nil {
		return DREDParsed{}
	}
	return d.cache.Parsed
}

// Result evaluates the retained DRED payload against an opus_dred_parse()-style
// request.
func (d *DRED) Result(maxDredSamples, sampleRate int) DREDResult {
	if d == nil {
		return DREDResult{}
	}
	return d.cache.Result(DREDRequest{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     sampleRate,
	})
}

// Parse finds and retains the temporary DRED packet extension from packet and
// returns the request-bounded available and trailing-silence sample counts.
//
// This tag-gated wrapper currently covers payload discovery plus low-cost
// metadata retention. It does not yet expose full model-backed DRED audio
// decode, but it matches libopus's standalone DRED control lifetime.
func (d *DREDDecoder) Parse(dst *DRED, packet []byte, maxDredSamples, sampleRate int, deferProcessing bool) (availableSamples, dredEnd int, err error) {
	if d == nil || dst == nil || sampleRate <= 0 || maxDredSamples < 0 {
		return 0, 0, ErrInvalidArgument
	}
	if !d.modelLoaded {
		return 0, 0, ErrDREDModelNotLoaded
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		dst.Clear()
		return 0, 0, nil
	}
	if err := dst.cache.Store(dst.data[:], payload, frameOffset); err != nil {
		return 0, 0, ErrInvalidPacket
	}
	dst.processStage = 1
	if !deferProcessing {
		if err := d.Process(dst, dst); err != nil {
			return 0, 0, err
		}
	}
	result := dst.Result(maxDredSamples, sampleRate)
	return result.Availability.AvailableSamples, result.Availability.EndSamples, nil
}

// Process finalizes a deferred standalone DRED metadata state. The current
// experimental wrapper has no extra model-backed stage yet, so processing
// primarily preserves the libopus-shaped deferred/processed control flow.
func (d *DREDDecoder) Process(src, dst *DRED) error {
	if d == nil || src == nil || dst == nil {
		return ErrInvalidArgument
	}
	if !d.modelLoaded {
		return ErrDREDModelNotLoaded
	}
	if src.processStage != 1 && src.processStage != 2 {
		return ErrInvalidArgument
	}
	if src != dst {
		*dst = *src
	}
	dst.processStage = 2
	return nil
}
