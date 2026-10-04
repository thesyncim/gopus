package libopustest

import "fmt"

const (
	MultistreamDecodeFormatFloat32 uint32 = iota
	MultistreamDecodeFormatInt16
	MultistreamDecodeFormatInt24
)

// MultistreamDecodeCase is one independent public multistream or projection
// decode request. ProbeMultistreamDecodeFresh creates a new C decoder for each.
type MultistreamDecodeCase struct {
	SampleRate uint32
	GainQ8     int32
	Format     uint32
	Family     uint32
	Channels   uint32
	Streams    uint32
	Coupled    uint32
	FrameSize  uint32
	Mapping    []byte
	Demixing   []byte
	Packet     []byte
}

// MultistreamDecodeResult contains the raw decoder return code, final range,
// and PCM bytes. PCM is the public API's little-endian float32, int16, or
// int24-in-int32 output.
type MultistreamDecodeResult struct {
	Code       int32
	FinalRange uint32
	PCM        []byte
}

var multistreamDecodeFreshHelper HelperCache

func buildMultistreamDecodeFreshHelper() (string, error) {
	return multistreamDecodeFreshHelper.Path(func() (string, error) {
		return BuildPublicAPIHelper(CHelperConfig{
			Label:      "fresh multistream decode",
			OutputBase: "gopus_libopus_refdecode",
			SourceFile: "libopus_refdecode_multistream.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{"-lm"},
		})
	})
}

// ProbeMultistreamDecodeFresh batches independent public decoder requests into
// one helper process while creating and destroying a C decoder for every case.
// Decode failures are returned as negative Code values; framing/build/run
// failures remain errors. Results preserve request order, and PCM slices retain
// the shared helper-output backing buffer.
func ProbeMultistreamDecodeFresh(cases []MultistreamDecodeCase) ([]MultistreamDecodeResult, error) {
	if len(cases) == 0 {
		return nil, nil
	}
	if len(cases) > 256 {
		return nil, fmt.Errorf("fresh multistream decode: batch has %d cases, maximum is 256", len(cases))
	}
	for i, c := range cases {
		if uint64(len(c.Mapping)) > uint64(^uint32(0)) ||
			uint64(len(c.Demixing)) > uint64(^uint32(0)) ||
			uint64(len(c.Packet)) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("fresh multistream decode case %d input is too large", i)
		}
	}

	binPath, err := buildMultistreamDecodeFreshHelper()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayloadVersion("GMSI", 7, uint32(len(cases)))
	for _, c := range cases {
		payload.U32(c.SampleRate)
		payload.I32(c.GainQ8)
		payload.U32(c.Format)
		payload.U32(c.Family)
		payload.U32(c.Channels)
		payload.U32(c.Streams)
		payload.U32(c.Coupled)
		payload.U32(c.FrameSize)
		payload.U32(uint32(len(c.Mapping)))
		payload.U32(uint32(len(c.Demixing)))
		payload.U32(uint32(len(c.Packet)))
		payload.Raw(c.Mapping)
		payload.Raw(c.Demixing)
		payload.Raw(c.Packet)
	}

	reader, err := RunOracleVersion(binPath, payload.Bytes(), "fresh multistream decode", "GMSO", 7)
	if err != nil {
		return nil, err
	}
	n := reader.Count(len(cases))
	out := make([]MultistreamDecodeResult, n)
	for i := range out {
		out[i].Code = reader.I32()
		out[i].FinalRange = reader.U32()
		pcmBytes := reader.U32()
		if uint64(pcmBytes) > uint64(reader.Remaining()) {
			return nil, fmt.Errorf("fresh multistream decode case %d PCM length=%d exceeds remaining output=%d", i, pcmBytes, reader.Remaining())
		}
		if out[i].Code <= 0 && pcmBytes != 0 {
			return nil, fmt.Errorf("fresh multistream decode case %d code=%d has %d PCM bytes", i, out[i].Code, pcmBytes)
		}
		if out[i].Code > 0 {
			itemSize := uint64(4)
			if cases[i].Format == MultistreamDecodeFormatInt16 {
				itemSize = 2
			}
			want := uint64(out[i].Code) * uint64(cases[i].Channels) * itemSize
			if want != uint64(pcmBytes) {
				return nil, fmt.Errorf("fresh multistream decode case %d PCM length=%d want %d", i, pcmBytes, want)
			}
		}
		out[i].PCM = reader.Bytes(int(pcmBytes))
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("fresh multistream decode: %w", err)
	}
	return out, nil
}
