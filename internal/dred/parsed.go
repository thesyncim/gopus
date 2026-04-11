package dred

// Parsed is the low-cost libopus-shaped DRED parse result retained before any
// model-backed processing stage.
type Parsed struct {
	Header Header
}

// ParsePayload decodes the lightweight DRED metadata from a payload body with
// the temporary experimental prefix already stripped.
func ParsePayload(payload []byte, dredFrameOffset int) (Parsed, error) {
	header, err := ParseHeader(payload, dredFrameOffset)
	if err != nil {
		return Parsed{}, err
	}
	return Parsed{Header: header}, nil
}

// Availability reports the request-bounded DRED coverage derived from the
// parsed payload metadata and opus_dred_parse() request parameters.
func (p Parsed) Availability(maxDredSamples, sampleRate int) Availability {
	return p.Header.Availability(maxDredSamples, sampleRate)
}

// FillQuantizerLevels writes the request-bounded libopus quantizer schedule
// into dst and returns the number of entries written.
func (p Parsed) FillQuantizerLevels(dst []int, maxDredSamples, sampleRate int) int {
	return p.ForRequest(Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     sampleRate,
	}).FillQuantizerLevels(dst)
}
