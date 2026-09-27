//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type FixedQEXTAutoChannelFrame struct {
	Bitrate int
	PCM     []int16
}

type FixedQEXTAutoChannelRecord struct {
	Status             int32
	Packet             []byte
	FinalRange         uint32
	CELTCalls          int
	CELTFrameSize      int
	CELTStreamChannels int32
	CELTBitrate        int32
	CELTLSBDepth       int32
	CELTMaxBytes       int
	CELTInputQ8        []int32
}

var opusEncodeFixedQEXTAutoChannelsHelper HelperCache

func buildOpusEncodeFixedQEXTAutoChannelsHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT 96 kHz auto-channel sequence",
		OutputBase:   "gopus_libopus_fixed_qext_96k_auto_channels",
		SourceFile:   "libopus_opus_encode_fixed_qext_auto_channels_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeOpusEncodeFixedQEXTAutoChannelRecords runs one selected FIXED_POINT +
// ENABLE_QEXT encoder through a per-frame bitrate sequence. ForceChannels is
// left at libopus's auto default so the packet TOC exposes each channel choice.
func ProbeOpusEncodeFixedQEXTAutoChannelRecords(frameSize, maxPacketBytes, complexity, lsbDepth int, qext bool, frames []FixedQEXTAutoChannelFrame) ([]FixedQEXTAutoChannelRecord, error) {
	if frameSize != 240 && frameSize != 480 && frameSize != 960 && frameSize != 1920 {
		return nil, fmt.Errorf("fixed-QEXT auto-channel oracle: invalid 96 kHz frame size %d", frameSize)
	}
	if len(frames) == 0 || maxPacketBytes < 1 || maxPacketBytes > 4000 || complexity < 0 || complexity > 10 || lsbDepth < 8 || lsbDepth > 24 {
		return nil, fmt.Errorf("fixed-QEXT auto-channel oracle: invalid dimensions or controls")
	}
	perFrame := frameSize * 2
	payload := NewOraclePayloadVersion("GQAI", 1,
		uint32(frameSize), uint32(len(frames)), uint32(maxPacketBytes),
		uint32(complexity), uint32(lsbDepth), boolToU32(qext))
	for i, frame := range frames {
		if frame.Bitrate <= 0 || frame.Bitrate > 1500000 || len(frame.PCM) != perFrame {
			return nil, fmt.Errorf("fixed-QEXT auto-channel oracle: invalid frame %d bitrate/PCM", i)
		}
		payload.U32(uint32(frame.Bitrate))
		for _, sample := range frame.PCM {
			payload.I16(sample)
		}
	}

	binPath, err := opusEncodeFixedQEXTAutoChannelsHelper.Path(buildOpusEncodeFixedQEXTAutoChannelsHelper)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracleVersion(binPath, payload.Bytes(), "fixed-QEXT 96 kHz auto-channel sequence", "GQAO", 2)
	if err != nil {
		return nil, err
	}
	if got := reader.Count(len(frames)); got != len(frames) {
		return nil, fmt.Errorf("fixed-QEXT auto-channel oracle: record count=%d want %d", got, len(frames))
	}
	records := make([]FixedQEXTAutoChannelRecord, len(frames))
	for i := range records {
		records[i].Status = reader.I32()
		packetLen := int(reader.U32())
		records[i].FinalRange = reader.U32()
		records[i].Packet = append([]byte(nil), reader.Bytes(packetLen)...)
		padding := (4 - (packetLen & 3)) & 3
		reader.Bytes(padding)
		records[i].CELTCalls = int(reader.U32())
		records[i].CELTFrameSize = int(reader.U32())
		sampleCount := int(reader.U32())
		records[i].CELTStreamChannels = int32(reader.U32())
		records[i].CELTBitrate = reader.I32()
		records[i].CELTLSBDepth = reader.I32()
		records[i].CELTMaxBytes = int(reader.U32())
		if sampleCount < 0 || sampleCount > frameSize*2 {
			return nil, fmt.Errorf("fixed-QEXT auto-channel oracle: invalid CELT input count in frame %d: %d", i, sampleCount)
		}
		records[i].CELTInputQ8 = make([]int32, sampleCount)
		for j := range records[i].CELTInputQ8 {
			records[i].CELTInputQ8[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return records, nil
}

func boolToU32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}
