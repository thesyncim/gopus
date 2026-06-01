package libopustest

import "fmt"

// Decode-side differential oracle.
//
// This wraps the version-2 wire protocol of libopus_decode_error_probe.c, which
// decodes each packet through a FRESH libopus decoder and returns, per case,
// the raw opus_decode* return code AND (on success) the decoded PCM bytes.
//
// It is the C side of the differential decode fuzzer: gopus decodes the same
// packets independently and the two results are asserted identical (or both
// rejected with the same error class). Per-case isolation (fresh decoder) makes
// every probe reproducible from the packet bytes alone, which is what lets the
// fuzzer minimise a failing case down to a single packet.

const (
	decodeDiffInputMagic  = "GDEI"
	decodeDiffOutputMagic = "GDEO"

	// DecodeDiffFormatFloat32 selects opus_decode_float (PCM is float32 LE).
	DecodeDiffFormatFloat32 = uint32(0)
	// DecodeDiffFormatInt16 selects opus_decode (PCM is int16 LE).
	DecodeDiffFormatInt16 = uint32(1)
	// DecodeDiffFormatInt24 selects opus_decode24 (PCM is int32 LE, 24-bit).
	DecodeDiffFormatInt24 = uint32(2)
)

// DecodeDiffCase is one packet to decode through the libopus oracle.
type DecodeDiffCase struct {
	Packet    []byte // nil/empty → NULL packet (PLC path)
	Format    uint32 // DecodeDiffFormat*
	FrameSize uint32 // PCM buffer capacity in samples/channel
	DecodeFEC bool
}

// DecodeDiffResult is the libopus oracle output for one DecodeDiffCase.
type DecodeDiffResult struct {
	// Code is the raw opus_decode* return value: negative libopus error, or the
	// positive decoded sample count per channel on success.
	Code int32
	// PCM holds the raw decoded sample bytes on success (Code > 0), little-endian
	// in the requested Format. Empty when Code <= 0.
	PCM []byte
}

// Float32 decodes r.PCM as little-endian float32 samples.
func (r DecodeDiffResult) Float32() []float32 {
	n := len(r.PCM) / 4
	out := make([]float32, n)
	rd := &OracleReader{data: r.PCM, label: "decode diff pcm"}
	for i := range out {
		out[i] = rd.Float32()
	}
	return out
}

// Int16 decodes r.PCM as little-endian int16 samples.
func (r DecodeDiffResult) Int16() []int16 {
	n := len(r.PCM) / 2
	out := make([]int16, n)
	rd := &OracleReader{data: r.PCM, label: "decode diff pcm"}
	for i := range out {
		out[i] = rd.I16()
	}
	return out
}

// Int24 decodes r.PCM as little-endian int32 (right-justified 24-bit) samples.
func (r DecodeDiffResult) Int24() []int32 {
	n := len(r.PCM) / 4
	out := make([]int32, n)
	rd := &OracleReader{data: r.PCM, label: "decode diff pcm"}
	for i := range out {
		out[i] = rd.I32()
	}
	return out
}

var decodeDiffHelper HelperCache

func buildDecodeDiffHelper() (string, error) {
	return decodeDiffHelper.CHelperPath(CHelperConfig{
		Label:      "decode diff probe",
		OutputBase: "gopus_decode_diff_probe",
		SourceFile: "libopus_decode_error_probe.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

// DecodeDiffHelperPath builds (once) and returns the differential decode oracle
// binary path.
func DecodeDiffHelperPath() (string, error) {
	return buildDecodeDiffHelper()
}

// ProbeDecodeDiff decodes every case through a fresh libopus decoder at the given
// sampleRate/channels and returns the per-case return code and decoded PCM.
//
// All cases share sampleRate/channels (one decoder session). Call separately for
// different stream configurations.
func ProbeDecodeDiff(sampleRate, channels int, cases []DecodeDiffCase) ([]DecodeDiffResult, error) {
	binPath, err := buildDecodeDiffHelper()
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, nil
	}

	payload := NewOraclePayloadVersion(decodeDiffInputMagic, 2,
		uint32(channels),
		uint32(sampleRate),
		uint32(len(cases)),
	)
	for _, c := range cases {
		fs := c.FrameSize
		if fs == 0 {
			fs = 5760
		}
		fec := uint32(0)
		if c.DecodeFEC {
			fec = 1
		}
		payload.U32(c.Format)
		payload.U32(fs)
		payload.U32(fec)
		payload.U32(uint32(len(c.Packet)))
		payload.Raw(c.Packet)
	}

	reader, err := RunOracleVersion(binPath, payload.Bytes(), "decode diff probe", decodeDiffOutputMagic, 2)
	if err != nil {
		return nil, err
	}
	n := reader.Count(len(cases))
	out := make([]DecodeDiffResult, n)
	for i := range out {
		out[i].Code = reader.I32()
		pcmBytes := int(reader.U32())
		if pcmBytes > 0 {
			b := reader.Bytes(pcmBytes)
			// Copy out of the reader's backing buffer.
			out[i].PCM = append([]byte(nil), b...)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("decode diff probe: %w", err)
	}
	return out, nil
}
