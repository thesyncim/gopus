//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sync"

	internaldred "github.com/thesyncim/gopus/internal/dred"
)

const libopusDREDPacketOutputMagic = "GODP"

type libopusDREDPacket struct {
	sampleRate     int
	maxDREDSamples int
	packet         []byte
}

func scaleDREDSampleCount(samples, fromRate, toRate int) int {
	if samples <= 0 || fromRate <= 0 || toRate <= 0 || fromRate == toRate {
		return samples
	}
	return samples * toRate / fromRate
}

func libopusDREDRequestForDecoder(packetInfo libopusDREDPacket, decoderSampleRate int) (maxDREDSamples, sampleRate int) {
	sampleRate = decoderSampleRate
	if sampleRate <= 0 {
		sampleRate = packetInfo.sampleRate
	}
	maxDREDSamples = packetInfo.maxDREDSamples
	if packetInfo.sampleRate > 0 && sampleRate > 0 {
		maxDREDSamples = scaleDREDSampleCount(maxDREDSamples, packetInfo.sampleRate, sampleRate)
	}
	return maxDREDSamples, sampleRate
}

type libopusDREDPacketConfig struct {
	FrameSize int
	ForceMode Mode
	Bandwidth Bandwidth
	Channels  int
}

type libopusDREDProcessInfo struct {
	availableSamples  int
	dredEndSamples    int
	processRet        int
	processStage      int
	nbLatents         int
	dredOffset        int
	secondProcessRet  int
	secondStage       int
	cloneProcessRet   int
	cloneStage        int
	secondStateHash   uint32
	secondLatentHash  uint32
	secondFeatureHash uint32
	cloneStateHash    uint32
	cloneLatentHash   uint32
	cloneFeatureHash  uint32
	state             [internaldred.StateDim]float32
	latents           []float32
	features          []float32
}

type libopusDREDRecoveryWindowInfo struct {
	availableSamples         int
	dredEndSamples           int
	processRet               int
	processStage             int
	nbLatents                int
	dredOffset               int
	featuresPerFrame         int
	neededFeatureFrames      int
	featureOffsetBase        int
	maxFeatureIndex          int
	recoverableFeatureFrames int
	missingPositiveFrames    int
	featureOffsets           []int
}

var (
	libopusDREDModelBlobHelperOnce sync.Once
	libopusDREDModelBlobHelperPath string
	libopusDREDModelBlobHelperErr  error

	libopusDREDEmitPacketHelperOnce sync.Once
	libopusDREDEmitPacketHelperPath string
	libopusDREDEmitPacketHelperErr  error

	libopusDREDProcessHelperOnce sync.Once
	libopusDREDProcessHelperPath string
	libopusDREDProcessHelperErr  error

	libopusDREDRecoveryWindowHelperOnce sync.Once
	libopusDREDRecoveryWindowHelperPath string
	libopusDREDRecoveryWindowHelperErr  error
)

func getLibopusDREDModelBlobHelperPath() (string, error) {
	libopusDREDModelBlobHelperOnce.Do(func() {
		libopusDREDModelBlobHelperPath, libopusDREDModelBlobHelperErr = buildLibopusDREDHelper("libopus_dred_model_blob.c", "gopus_libopus_dred_model_blob", true)
	})
	if libopusDREDModelBlobHelperErr != nil {
		return "", libopusDREDModelBlobHelperErr
	}
	return libopusDREDModelBlobHelperPath, nil
}

func getLibopusDREDEmitPacketHelperPath() (string, error) {
	libopusDREDEmitPacketHelperOnce.Do(func() {
		libopusDREDEmitPacketHelperPath, libopusDREDEmitPacketHelperErr = buildLibopusDREDHelper("libopus_dred_emit_packet.c", "gopus_libopus_dred_emit_packet", true)
	})
	if libopusDREDEmitPacketHelperErr != nil {
		return "", libopusDREDEmitPacketHelperErr
	}
	return libopusDREDEmitPacketHelperPath, nil
}

func getLibopusDREDProcessHelperPath() (string, error) {
	libopusDREDProcessHelperOnce.Do(func() {
		libopusDREDProcessHelperPath, libopusDREDProcessHelperErr = buildLibopusDREDHelper("libopus_dred_process_info.c", "gopus_libopus_dred_process", true)
	})
	if libopusDREDProcessHelperErr != nil {
		return "", libopusDREDProcessHelperErr
	}
	return libopusDREDProcessHelperPath, nil
}

func getLibopusDREDRecoveryWindowHelperPath() (string, error) {
	libopusDREDRecoveryWindowHelperOnce.Do(func() {
		libopusDREDRecoveryWindowHelperPath, libopusDREDRecoveryWindowHelperErr = buildLibopusDREDHelper("libopus_dred_recovery_window_info.c", "gopus_libopus_dred_recovery_window", true)
	})
	if libopusDREDRecoveryWindowHelperErr != nil {
		return "", libopusDREDRecoveryWindowHelperErr
	}
	return libopusDREDRecoveryWindowHelperPath, nil
}

func probeLibopusDREDModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run dred model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func emitLibopusDREDPacket() (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func emitLibopusDREDPacketWithFrameSize(frameSize int) (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func emitLibopusDREDPacketWithConfig(cfg libopusDREDPacketConfig) (libopusDREDPacket, error) {
	binPath, err := getLibopusDREDEmitPacketHelperPath()
	if err != nil {
		return libopusDREDPacket{}, err
	}
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	forceModeEnv, err := libopusDREDForceModeEnv(cfg.ForceMode)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	bandwidthEnv, err := libopusDREDBandwidthEnv(cfg.Bandwidth)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GOPUS_DRED_FRAME_SIZE=%d", cfg.FrameSize),
		fmt.Sprintf("GOPUS_DRED_CHANNELS=%d", cfg.Channels),
		fmt.Sprintf("GOPUS_DRED_FORCE_MODE=%s", forceModeEnv),
		fmt.Sprintf("GOPUS_DRED_BANDWIDTH=%s", bandwidthEnv),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDPacket{}, fmt.Errorf("run dred emit helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	if len(out) < 20 || string(out[:4]) != libopusDREDPacketOutputMagic {
		return libopusDREDPacket{}, fmt.Errorf("unexpected dred emit helper output")
	}
	packetLen := int(binary.LittleEndian.Uint32(out[16:20]))
	if len(out) != 20+packetLen {
		return libopusDREDPacket{}, fmt.Errorf("truncated dred packet output")
	}
	info := libopusDREDPacket{
		sampleRate:     int(binary.LittleEndian.Uint32(out[8:12])),
		maxDREDSamples: int(binary.LittleEndian.Uint32(out[12:16])),
		packet:         append([]byte(nil), out[20:]...),
	}
	toc := ParseTOC(info.packet[0])
	if toc.Mode != cfg.ForceMode {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper mode=%v want %v", toc.Mode, cfg.ForceMode)
	}
	if toc.Bandwidth != cfg.Bandwidth {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper bandwidth=%v want %v", toc.Bandwidth, cfg.Bandwidth)
	}
	packetDuration, err := opusPacketDurationSamples(info.packet)
	if err != nil {
		return libopusDREDPacket{}, fmt.Errorf("parse dred emit helper packet duration: %w", err)
	}
	if packetDuration != cfg.FrameSize {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper packet duration=%d want %d", packetDuration, cfg.FrameSize)
	}
	return info, nil
}

func opusPacketDurationSamples(packet []byte) (int, error) {
	info, err := ParsePacket(packet)
	if err != nil {
		return 0, err
	}
	return info.TOC.FrameSize * info.FrameCount, nil
}

func libopusDREDForceModeEnv(mode Mode) (string, error) {
	switch mode {
	case ModeCELT:
		return "celt", nil
	case ModeHybrid:
		return "hybrid", nil
	case ModeSILK:
		return "silk", nil
	default:
		return "", fmt.Errorf("unsupported libopus dred packet force mode %v", mode)
	}
}

func libopusDREDBandwidthEnv(bandwidth Bandwidth) (string, error) {
	switch bandwidth {
	case BandwidthWideband:
		return "wb", nil
	case BandwidthSuperwideband:
		return "swb", nil
	case BandwidthFullband:
		return "fb", nil
	default:
		return "", fmt.Errorf("unsupported libopus dred packet bandwidth %v", bandwidth)
	}
}

func probeLibopusDREDProcess(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDProcessInfo, error) {
	binPath, err := getLibopusDREDProcessHelperPath()
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDREDParseInputMagic)
	for _, v := range []uint32{1, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDREDProcessInfo{}, fmt.Errorf("encode dred process helper header: %w", err)
		}
	}
	if _, err := payload.Write(packet); err != nil {
		return libopusDREDProcessInfo{}, fmt.Errorf("encode dred process helper packet: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDProcessInfo{}, fmt.Errorf("run dred process helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	headerBytes := 4 + 4 + 16*4
	if len(out) < headerBytes || string(out[:4]) != libopusDREDParseOutputMagic {
		return libopusDREDProcessInfo{}, fmt.Errorf("unexpected dred process helper output")
	}

	info := libopusDREDProcessInfo{
		availableSamples:  int(int32(binary.LittleEndian.Uint32(out[8:12]))),
		dredEndSamples:    int(int32(binary.LittleEndian.Uint32(out[12:16]))),
		processRet:        int(int32(binary.LittleEndian.Uint32(out[16:20]))),
		processStage:      int(int32(binary.LittleEndian.Uint32(out[20:24]))),
		nbLatents:         int(int32(binary.LittleEndian.Uint32(out[24:28]))),
		dredOffset:        int(int32(binary.LittleEndian.Uint32(out[28:32]))),
		secondProcessRet:  int(int32(binary.LittleEndian.Uint32(out[32:36]))),
		secondStage:       int(int32(binary.LittleEndian.Uint32(out[36:40]))),
		cloneProcessRet:   int(int32(binary.LittleEndian.Uint32(out[40:44]))),
		cloneStage:        int(int32(binary.LittleEndian.Uint32(out[44:48]))),
		secondStateHash:   binary.LittleEndian.Uint32(out[48:52]),
		secondLatentHash:  binary.LittleEndian.Uint32(out[52:56]),
		secondFeatureHash: binary.LittleEndian.Uint32(out[56:60]),
		cloneStateHash:    binary.LittleEndian.Uint32(out[60:64]),
		cloneLatentHash:   binary.LittleEndian.Uint32(out[64:68]),
		cloneFeatureHash:  binary.LittleEndian.Uint32(out[68:72]),
	}

	offset := 72
	for i := range info.state {
		if len(out) < offset+4 {
			return libopusDREDProcessInfo{}, fmt.Errorf("truncated dred process state")
		}
		info.state[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		if len(out) < offset+4 {
			return libopusDREDProcessInfo{}, fmt.Errorf("truncated dred process latents")
		}
		info.latents[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
	}

	featureValues := info.nbLatents * 4 * internaldred.NumFeatures
	info.features = make([]float32, featureValues)
	for i := 0; i < featureValues; i++ {
		if len(out) < offset+4 {
			return libopusDREDProcessInfo{}, fmt.Errorf("truncated dred process features")
		}
		info.features[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
	}
	return info, nil
}

func probeLibopusDREDRecoveryWindow(packet []byte, maxDREDSamples, sampleRate, frameSizeSamples, decodeOffsetSamples int, blend bool) (libopusDREDRecoveryWindowInfo, error) {
	binPath, err := getLibopusDREDRecoveryWindowHelperPath()
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDREDParseInputMagic)
	var blendFlag uint32
	if blend {
		blendFlag = 1
	}
	for _, v := range []uint32{
		1,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(int32(decodeOffsetSamples)),
		blendFlag,
		uint32(len(packet)),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("encode dred recovery helper header: %w", err)
		}
	}
	if _, err := payload.Write(packet); err != nil {
		return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("encode dred recovery helper packet: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("run dred recovery helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	headerBytes := 4 + 4 + 12*4
	if len(out) < headerBytes || string(out[:4]) != libopusDREDParseOutputMagic {
		return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("unexpected dred recovery helper output")
	}

	info := libopusDREDRecoveryWindowInfo{
		availableSamples:         int(int32(binary.LittleEndian.Uint32(out[8:12]))),
		dredEndSamples:           int(int32(binary.LittleEndian.Uint32(out[12:16]))),
		processRet:               int(int32(binary.LittleEndian.Uint32(out[16:20]))),
		processStage:             int(int32(binary.LittleEndian.Uint32(out[20:24]))),
		nbLatents:                int(int32(binary.LittleEndian.Uint32(out[24:28]))),
		dredOffset:               int(int32(binary.LittleEndian.Uint32(out[28:32]))),
		featuresPerFrame:         int(int32(binary.LittleEndian.Uint32(out[32:36]))),
		neededFeatureFrames:      int(int32(binary.LittleEndian.Uint32(out[36:40]))),
		featureOffsetBase:        int(int32(binary.LittleEndian.Uint32(out[40:44]))),
		maxFeatureIndex:          int(int32(binary.LittleEndian.Uint32(out[44:48]))),
		recoverableFeatureFrames: int(int32(binary.LittleEndian.Uint32(out[48:52]))),
		missingPositiveFrames:    int(int32(binary.LittleEndian.Uint32(out[52:56]))),
	}

	offset := 56
	info.featureOffsets = make([]int, info.neededFeatureFrames)
	for i := range info.featureOffsets {
		if len(out) < offset+4 {
			return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("truncated dred recovery helper offsets")
		}
		info.featureOffsets[i] = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
	}
	return info, nil
}
