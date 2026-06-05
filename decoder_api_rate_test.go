package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusRefdecodeSingleFormatFloat32 = uint32(0)
	libopusRefdecodeSingleFormatInt16   = uint32(1)
)

var libopusAPIRateRefdecodeHelper libopustest.HelperCache

type apiRatePacketParityCase struct {
	name      string
	packet    func(t *testing.T, channels int) []byte
	tolerance float64
}

func apiRatePLCDurationCases() []apiRatePacketParityCase {
	return []apiRatePacketParityCase{
		{name: "silk_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 480) }, tolerance: 8e-3},
		{name: "silk_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 1920) }, tolerance: 8e-3},
		{name: "silk_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 2880) }, tolerance: 8e-3},
		{name: "celt_2p5ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 120) }, tolerance: 3e-3},
		{name: "celt_5ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 240) }, tolerance: 3e-3},
		{name: "celt_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 480) }, tolerance: 3e-3},
		{name: "celt_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 1920) }, tolerance: 3e-3},
		{name: "celt_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 2880) }, tolerance: 3e-3},
		{name: "hybrid_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 480) }, tolerance: 1e-2},
		{name: "hybrid_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 1920) }, tolerance: 1e-2},
		{name: "hybrid_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 2880) }, tolerance: 1e-2},
	}
}

func invalidAPIRateRequestedFrameSizes(sampleRate int) []int {
	quantum := sampleRate / 400
	return []int{quantum + 1, sampleRate/50 - 1, sampleRate/50 + 1}
}

func overlongAPIRateRequestedFrameSize(sampleRate int) int {
	return sampleRate / 5 // 200 ms, larger than one 120 ms internal PLC chunk.
}

func encodeAPIRateSILKPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateSILKPacketFrameSize(t, channels, 960)
}

func encodeAPIRateSILKPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		left := 0.22 * float32(math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.18 * float32(math.Sin(2*math.Pi*660*float64(i)/sampleRate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateCELTPacketFrameSize(t, channels, 960)
}

func encodeAPIRateCELTPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		left := 0.28 * float32(math.Sin(2*math.Pi*1200*float64(i)/sampleRate))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.19 * float32(math.Sin(2*math.Pi*1900*float64(i)/sampleRate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacketFrameSizeVariant(t *testing.T, channels, frameSize, bitrate, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeVBR); err != nil {
		t.Fatalf("SetBitrateMode(VBR): %v", err)
	}
	enc.SetVBRConstraint(false)
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		tm := float64(variant*frameSize+i) / sampleRate
		left := 0.28*float32(math.Sin(2*math.Pi*(1200+float64(variant)*137)*tm+float64(variant)*0.11)) +
			0.05*float32(math.Sin(2*math.Pi*(2300+float64(variant)*91)*tm+0.23))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.19*float32(math.Sin(2*math.Pi*(1900+float64(variant)*151)*tm+0.31)) +
				0.04*float32(math.Sin(2*math.Pi*(3100+float64(variant)*73)*tm+0.07))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacketVariants(t *testing.T, channels, frameSize int, bitrates []int, count int) [][]byte {
	t.Helper()
	packets := make([][]byte, 0, 16)
	for variant := range 16 {
		bitrate := bitrates[variant%len(bitrates)]
		packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, bitrate, variant)
		if len(packets) > 0 && packet[0]&0xFC != packets[0][0]&0xFC {
			t.Fatalf("variant TOC base=0x%02x want 0x%02x", packet[0]&0xFC, packets[0][0]&0xFC)
		}
		packets = append(packets, packet)
		for start := 0; start+count <= len(packets); start++ {
			window := packets[start : start+count]
			if apiRatePacketsHaveUnequalPayloadSizes(window) {
				return append([][]byte(nil), window...)
			}
		}
	}
	t.Fatalf("failed to generate %d CELT VBR frames with unequal payload sizes", count)
	return nil
}

func encodeAPIRateHybridPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateHybridPacketFrameSize(t, channels, 960)
}

func encodeAPIRateHybridPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeHybrid); err != nil {
		t.Fatalf("SetMode(Hybrid): %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(64000 * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		tm := float64(i) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
			0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateFECSequence(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate, channels, frameSize int) ([]byte, []byte) {
	t.Helper()
	const sampleRate = 48000
	app := ApplicationVoIP
	if mode == EncoderModeSILK {
		app = ApplicationRestrictedSilk
	}
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: app,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, 12)
	for frameIndex := range 12 {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			pcm[i*channels] = 0.38*float32(math.Sin(2*math.Pi*220*tm)) +
				0.14*float32(math.Sin(2*math.Pi*440*tm+0.11))
			if channels == 2 {
				pcm[i*channels+1] = 0.33*float32(math.Sin(2*math.Pi*330*tm+0.07)) +
					0.12*float32(math.Sin(2*math.Pi*660*tm+0.19))
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != wantMode {
			t.Fatalf("Encode frame %d mode=%v want %v", frameIndex, toc.Mode, wantMode)
		}
		packets = append(packets, append([]byte(nil), packet...))
		if len(packets) >= 3 && packetHasInBandFEC(t, packet) {
			return packets[len(packets)-3], packet
		}
	}
	t.Fatalf("failed to generate %v packet carrying LBRR", wantMode)
	return nil, nil
}

func firstAPIRateFramePayload(t *testing.T, packet []byte) []byte {
	t.Helper()
	_, frames, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parsePacketFrames: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("single-frame packet has %d frames", len(frames))
	}
	return append([]byte(nil), frames[0]...)
}

func firstAPIRateFramePayloads(t *testing.T, packets [][]byte) [][]byte {
	t.Helper()
	frames := make([][]byte, len(packets))
	for i, packet := range packets {
		frames[i] = firstAPIRateFramePayload(t, packet)
	}
	return frames
}

func buildAPIRateMultiFramePacket(t *testing.T, basePacket []byte, frames [][]byte, wantFrameCode byte) []byte {
	t.Helper()
	data := make([]byte, maxPacketBytesPerStream)
	n, err := buildRepacketizedPacketWithOptions(basePacket[0]&0xFC, frames, data, 0, false, nil)
	if err != nil {
		t.Fatalf("buildRepacketizedPacketWithOptions: %v", err)
	}
	packet := append([]byte(nil), data[:n]...)
	info, parsed, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parse repacketized packet: %v", err)
	}
	if info.TOC.FrameCode != wantFrameCode {
		t.Fatalf("frame code=%d want %d", info.TOC.FrameCode, wantFrameCode)
	}
	if len(parsed) != len(frames) {
		t.Fatalf("frame count=%d want %d", len(parsed), len(frames))
	}
	return packet
}

func apiRatePacketsHaveUnequalPayloadSizes(packets [][]byte) bool {
	if len(packets) < 2 {
		return false
	}
	firstLen := len(firstPacketPayloadForSizeCheck(packets[0]))
	for _, packet := range packets[1:] {
		if len(firstPacketPayloadForSizeCheck(packet)) != firstLen {
			return true
		}
	}
	return false
}

func firstPacketPayloadForSizeCheck(packet []byte) []byte {
	if len(packet) == 0 {
		return nil
	}
	if packet[0]&0x03 == 0 {
		return packet[1:]
	}
	_, frames, err := parsePacketFrames(packet)
	if err != nil || len(frames) == 0 {
		return nil
	}
	return frames[0]
}

type libopusAPIRateDecodeStep struct {
	packet    []byte
	frameSize int
	fec       bool
}

func getLibopusAPIRateRefdecodeHelperPath() (string, error) {
	return libopusAPIRateRefdecodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "api-rate reference decode",
		OutputBase: "gopus_libopus_refdecode_api_rate",
		SourceFile: "libopus_refdecode_single.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

func requireLibopusAPIRateRefdecodeHelper(t *testing.T) {
	t.Helper()
	if _, err := getLibopusAPIRateRefdecodeHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "api-rate reference decode", err)
	}
}

func decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize int, packets [][]byte) ([]float32, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, packet := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: packet}
	}
	return decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]float32, error) {
	return decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]float32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5, libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]float32, []uint32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 6, libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), 0, uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, nil, err
	}
	reader, version, err := libopustest.NewOracleReaderVersion("api-rate reference decode ranges", "GOSO", data)
	if err != nil {
		return nil, nil, err
	}
	if version != 2 {
		return nil, nil, fmt.Errorf("api-rate reference decode ranges helper version=%d want 2", version)
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples*4 + 4 + len(steps)*4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	rangeCount := reader.Count(len(steps))
	ranges := make([]uint32, rangeCount)
	for i := range ranges {
		ranges[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return decoded, ranges, nil
}

func decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize int, packets [][]byte) ([]int16, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, packet := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: packet}
	}
	return decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	return decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5, libopusRefdecodeSingleFormatInt16, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, channels, maxFrameSize int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 7, libopusRefdecodeSingleFormatInt16, uint32(sampleRate), 0, uint32(channels), uint32(maxFrameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		frameSize := step.frameSize
		if frameSize == 0 {
			frameSize = maxFrameSize
		}
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}
