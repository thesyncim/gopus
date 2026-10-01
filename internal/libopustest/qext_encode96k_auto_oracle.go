package libopustest

import "fmt"

const (
	qextAuto96kInputMagic  = "GQAI"
	qextAuto96kOutputMagic = "GQAO"
)

type QEXT96kAutoFrame struct {
	Bitrate int
	PCM     []float32
}

type QEXT96kAutoRecord struct {
	Status        int32
	Packet        []byte
	FinalRange    uint32
	CELTCalls     uint32
	CELTFrameSize uint32
	CELTStream    uint32
	CELTBitrate   int32
	CELTLSBDepth  int32
	CELTMaxBytes  uint32
	CELTInput     []float32
	CELTState     []uint32
	PitchBuffer   []float32
	PitchSearch   int32
	RemoveInput   int32
	RemoveOutput  int32
	RemoveGain    uint32
}

var qextAuto96kHelper HelperCache
var qextAuto96kStateHelper HelperCache

func buildQEXTAuto96kHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "qext native 96 kHz auto-channel sequence",
		OutputBase:  "gopus_libopus_qext_96k_auto_channels",
		SourceFile:  "libopus_qext_96k_auto_channels_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-fno-tree-vectorize", "-fno-tree-slp-vectorize"},
		RefIncludes: []string{"celt", "silk", "src"},
		QEXTRef:     true,
		Libs:        []string{QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func buildQEXTAuto96kStateHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "qext native 96 kHz auto-channel state sequence",
		OutputBase:  "gopus_libopus_qext_96k_auto_channels_state",
		SourceFile:  "libopus_qext_96k_auto_channels_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-DGOPUS_STATE_TRACE=1", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		QEXTRef:     true,
		Libs:        []string{QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeQEXT96kAutoChannels runs the selected float + ENABLE_QEXT public
// encoder with OPUS_APPLICATION_AUDIO and per-frame bitrate updates.
func ProbeQEXT96kAutoChannels(frameSize, maxPacketBytes, complexity, lsbDepth int, qext bool, frames []QEXT96kAutoFrame) ([]QEXT96kAutoRecord, error) {
	return probeQEXT96kAutoChannels(frameSize, maxPacketBytes, complexity, lsbDepth, qext, frames, false)
}

func ProbeQEXT96kAutoChannelsState(frameSize, maxPacketBytes, complexity, lsbDepth int, qext bool, frames []QEXT96kAutoFrame) ([]QEXT96kAutoRecord, error) {
	return probeQEXT96kAutoChannels(frameSize, maxPacketBytes, complexity, lsbDepth, qext, frames, true)
}

func probeQEXT96kAutoChannels(frameSize, maxPacketBytes, complexity, lsbDepth int, qext bool, frames []QEXT96kAutoFrame, withState bool) ([]QEXT96kAutoRecord, error) {
	if frameSize != 240 && frameSize != 480 && frameSize != 960 && frameSize != 1920 {
		return nil, fmt.Errorf("qext 96 kHz auto: invalid frame size %d", frameSize)
	}
	if maxPacketBytes < 1 || maxPacketBytes > 4000 || complexity < 0 || complexity > 10 ||
		lsbDepth < 8 || lsbDepth > 24 || len(frames) < 1 || len(frames) > 32 {
		return nil, fmt.Errorf("qext 96 kHz auto: invalid controls")
	}
	payload := NewOraclePayloadVersion(qextAuto96kInputMagic, 1)
	payload.U32(uint32(frameSize))
	payload.U32(uint32(len(frames)))
	payload.U32(uint32(maxPacketBytes))
	payload.U32(uint32(complexity))
	payload.U32(uint32(lsbDepth))
	if qext {
		payload.U32(1)
	} else {
		payload.U32(0)
	}
	for i, frame := range frames {
		if frame.Bitrate <= 0 || frame.Bitrate > 1500000 || len(frame.PCM) != frameSize*2 {
			return nil, fmt.Errorf("qext 96 kHz auto: invalid frame %d", i)
		}
		payload.U32(uint32(frame.Bitrate))
		payload.Float32s(frame.PCM...)
	}
	helper := &qextAuto96kHelper
	build := buildQEXTAuto96kHelper
	version := uint32(2)
	if withState {
		helper = &qextAuto96kStateHelper
		build = buildQEXTAuto96kStateHelper
		version = 4
	}
	bin, err := helper.Path(build)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracleVersion(bin, payload.Bytes(), "qext native 96 kHz auto-channel sequence", qextAuto96kOutputMagic, version)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(frames))
	if count != len(frames) {
		return nil, fmt.Errorf("qext 96 kHz auto: C frame count %d want %d: %v", count, len(frames), reader.Err())
	}
	out := make([]QEXT96kAutoRecord, count)
	for i := range out {
		out[i].Status = reader.I32()
		n := int(reader.U32())
		out[i].FinalRange = reader.U32()
		if n < 0 || n > maxPacketBytes {
			return nil, fmt.Errorf("qext 96 kHz auto: packet length %d in frame %d", n, i)
		}
		out[i].Packet = append([]byte(nil), reader.Bytes(n)...)
		if padding := (4 - n%4) % 4; padding > 0 {
			reader.Bytes(padding)
		}
		out[i].CELTCalls = reader.U32()
		out[i].CELTFrameSize = reader.U32()
		count := int(reader.U32())
		out[i].CELTStream = reader.U32()
		out[i].CELTBitrate = reader.I32()
		out[i].CELTLSBDepth = reader.I32()
		out[i].CELTMaxBytes = reader.U32()
		if count < 0 || count > 3840 {
			return nil, fmt.Errorf("qext 96 kHz auto: CELT input length %d in frame %d", count, i)
		}
		out[i].CELTInput = make([]float32, count)
		for j := range out[i].CELTInput {
			out[i].CELTInput[j] = reader.Float32()
		}
		if withState {
			out[i].CELTState = readQEXTCELTFloatState(reader)
			pitchBufferCount := int(reader.U32())
			if pitchBufferCount < 0 || pitchBufferCount > 2048 {
				return nil, fmt.Errorf("qext 96 kHz auto: pitch buffer length %d in frame %d", pitchBufferCount, i)
			}
			out[i].PitchBuffer = make([]float32, pitchBufferCount)
			for j := range out[i].PitchBuffer {
				out[i].PitchBuffer[j] = reader.Float32()
			}
			out[i].PitchSearch = reader.I32()
			out[i].RemoveInput = reader.I32()
			out[i].RemoveOutput = reader.I32()
			out[i].RemoveGain = reader.U32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("qext 96 kHz auto: %w", err)
	}
	return out, nil
}

func readQEXTCELTFloatState(reader *OracleReader) []uint32 {
	const scalarCount = 35
	state := make([]uint32, 0, scalarCount+8192)
	if reader.Count(scalarCount) == 0 {
		return state
	}
	for i := 0; i < scalarCount; i++ {
		state = append(state, reader.U32())
	}
	for array := 0; array < 8; array++ {
		count := int(reader.U32())
		if count < 0 || count > 8192 {
			return state
		}
		state = append(state, uint32(count))
		for i := 0; i < count; i++ {
			state = append(state, reader.U32())
		}
	}
	return state
}
