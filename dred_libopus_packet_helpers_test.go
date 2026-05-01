//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"sync"
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

var (
	libopusDREDEmitPacketHelperOnce sync.Once
	libopusDREDEmitPacketHelperPath string
	libopusDREDEmitPacketHelperErr  error
)

func getLibopusDREDEmitPacketHelperPath() (string, error) {
	libopusDREDEmitPacketHelperOnce.Do(func() {
		libopusDREDEmitPacketHelperPath, libopusDREDEmitPacketHelperErr = buildLibopusDREDHelper("libopus_dred_emit_packet.c", "gopus_libopus_dred_emit_packet", true)
	})
	if libopusDREDEmitPacketHelperErr != nil {
		return "", libopusDREDEmitPacketHelperErr
	}
	return libopusDREDEmitPacketHelperPath, nil
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
