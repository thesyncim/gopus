//go:build gopus_qext

package libopustest

import (
	"fmt"
)

const (
	multistreamInt24_96kInputMagic  = "GMI4"
	multistreamInt24_96kOutputMagic = "GMO4"
)

var multistreamInt24_96kHelper HelperCache

func buildMultistreamInt24_96kHelper() (string, error) {
	return BuildPublicAPIHelper(CHelperConfig{
		Label:       "multistream int24 96 kHz long encode",
		OutputBase:  "gopus_libopus_multistream_int24_96k_long",
		SourceFile:  "libopus_multistream_int24_96k_long_oracle.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{"-lm"},
		DeadStrip:   true,
	})
}

type MultistreamInt24Encode96kParams struct {
	Channels       int
	Streams        int
	CoupledStreams int
	Mapping        []byte
	FrameSize      int
	PacketCapacity int
	Bitrate        int
	Complexity     int
	PCM            []int32
}

type MultistreamInt24Encode96kResult struct {
	Packet     []byte
	Samples    int
	FinalRange uint32
}

// ProbeMultistreamInt24Encode96k drives the selected 96 kHz QEXT-enabled
// libopus opus_multistream_encode24() entry point and returns its packet and
// XOR-combined per-stream final range.
func ProbeMultistreamInt24Encode96k(p MultistreamInt24Encode96kParams) (MultistreamInt24Encode96kResult, error) {
	if p.Channels < 1 || p.Channels > 2 || p.Streams < 1 || p.Streams > p.Channels ||
		p.CoupledStreams < 0 || p.CoupledStreams > p.Streams ||
		p.CoupledStreams*2+p.Streams-p.CoupledStreams != p.Channels ||
		len(p.Mapping) != p.Channels || p.FrameSize < 1 || p.FrameSize > 11520 ||
		p.PacketCapacity < 2*p.Streams-1 || p.PacketCapacity > 4000 ||
		p.Bitrate < 1 || p.Complexity < 0 || p.Complexity > 10 ||
		len(p.PCM) != p.FrameSize*p.Channels {
		return MultistreamInt24Encode96kResult{}, fmt.Errorf("multistream int24 96 kHz: invalid dimensions or controls")
	}

	binPath, err := multistreamInt24_96kHelper.Path(buildMultistreamInt24_96kHelper)
	if err != nil {
		return MultistreamInt24Encode96kResult{}, err
	}
	payload := NewOraclePayloadVersion(multistreamInt24_96kInputMagic, 1)
	payload.U32s(
		uint32(p.FrameSize), uint32(p.Channels), uint32(p.Streams),
		uint32(p.CoupledStreams), uint32(p.PacketCapacity), uint32(p.Bitrate),
		uint32(p.Complexity),
	)
	payload.Raw(p.Mapping)
	payload.I32s(p.PCM...)
	reader, err := RunOracle(binPath, payload.Bytes(), "multistream int24 96 kHz encode", multistreamInt24_96kOutputMagic)
	if err != nil {
		return MultistreamInt24Encode96kResult{}, err
	}
	packetLen := int(reader.U32())
	result := MultistreamInt24Encode96kResult{
		Samples:    int(reader.U32()),
		FinalRange: reader.U32(),
		Packet:     append([]byte(nil), reader.Bytes(packetLen)...),
	}
	if err := reader.ExpectConsumed(); err != nil {
		return MultistreamInt24Encode96kResult{}, fmt.Errorf("multistream int24 96 kHz oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return MultistreamInt24Encode96kResult{}, err
	}
	return result, nil
}
