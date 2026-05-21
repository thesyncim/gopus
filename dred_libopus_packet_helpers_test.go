//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const libopusDREDPacketOutputMagic = "GODP"
const libopusDREDPacketMaxFramesToTry = 640

type libopusDREDPacket struct {
	sampleRate     int
	maxDREDSamples int
	frameIndex     int
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
	FrameSize     int
	ForceMode     Mode
	Bandwidth     Bandwidth
	Channels      int
	ForceChannels int
	Bitrate       int
	CBR           bool
	Multistream   bool
}

var libopusDREDEmitPacketHelper libopustest.HelperCache

func getLibopusDREDEmitPacketHelperPath() (string, error) {
	return libopusDREDEmitPacketHelper.Path(func() (string, error) {
		return buildLibopusDREDHelper("libopus_dred_emit_packet.c", "gopus_libopus_dred_emit_packet", true)
	})
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
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = encoderDREDBitrateForFrameSize(cfg.FrameSize)
	}
	forceModeEnv, err := libopusDREDForceModeEnv(cfg.ForceMode)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	bandwidthEnv, err := libopusDREDBandwidthEnv(cfg.Bandwidth)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	env := []string{
		fmt.Sprintf("GOPUS_DRED_FRAME_SIZE=%d", cfg.FrameSize),
		fmt.Sprintf("GOPUS_DRED_CHANNELS=%d", cfg.Channels),
		fmt.Sprintf("GOPUS_DRED_FORCE_MODE=%s", forceModeEnv),
		fmt.Sprintf("GOPUS_DRED_BANDWIDTH=%s", bandwidthEnv),
		fmt.Sprintf("GOPUS_DRED_BITRATE=%d", cfg.Bitrate),
		"GOPUS_DRED_PCM_STDIN=1",
	}
	if cfg.CBR {
		env = append(env, "GOPUS_DRED_CBR=1")
	}
	if cfg.ForceChannels != 0 {
		env = append(env, fmt.Sprintf("GOPUS_DRED_FORCE_CHANNELS=%d", cfg.ForceChannels))
	}
	if cfg.Multistream {
		env = append(env, "GOPUS_DRED_MULTISTREAM=1")
	}
	out, err := libopustest.RunHelperEnv(binPath, libopusDREDPacketPCMInput(cfg, libopusDREDPacketMaxFramesToTry), env)
	if err != nil {
		return libopusDREDPacket{}, fmt.Errorf("run dred emit helper: %w", err)
	}

	reader, version, err := libopustest.NewOracleReaderVersion("dred emit", libopusDREDPacketOutputMagic, out)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	if version != 1 && version != 2 {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper version=%d want 1 or 2", version)
	}
	info := libopusDREDPacket{
		sampleRate:     int(reader.U32()),
		maxDREDSamples: int(reader.U32()),
	}
	packetLen := int(reader.U32())
	if version >= 2 {
		info.frameIndex = int(reader.U32())
	}
	packet := reader.Bytes(packetLen)
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDPacket{}, err
	}
	info.packet = append([]byte(nil), packet...)
	if len(info.packet) == 0 {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper returned an empty packet")
	}
	toc := ParseTOC(info.packet[0])
	if toc.Mode != cfg.ForceMode {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper mode=%v want %v", toc.Mode, cfg.ForceMode)
	}
	if toc.Bandwidth != cfg.Bandwidth {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper bandwidth=%v want %v", toc.Bandwidth, cfg.Bandwidth)
	}
	if cfg.ForceChannels != 0 && toc.Stereo != (cfg.ForceChannels == 2) {
		gotChannels := 1
		if toc.Stereo {
			gotChannels = 2
		}
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper channels=%d want %d", gotChannels, cfg.ForceChannels)
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

func libopusDREDPacketPCMInput(cfg libopusDREDPacketConfig, maxFrames int) []byte {
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	out := make([]byte, 0, maxFrames*cfg.FrameSize*cfg.Channels*4)
	var tmp [4]byte
	for frameIdx := 0; frameIdx < maxFrames; frameIdx++ {
		for sampleIdx := 0; sampleIdx < cfg.FrameSize; sampleIdx++ {
			bits := math.Float32bits(encoderDREDVoicedSample(frameIdx, sampleIdx, cfg.FrameSize, 48000))
			binary.LittleEndian.PutUint32(tmp[:], bits)
			for ch := 0; ch < cfg.Channels; ch++ {
				out = append(out, tmp[:]...)
			}
		}
	}
	return out
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
		return "", fmt.Errorf("extra libopus dred packet force mode %v", mode)
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
		return "", fmt.Errorf("extra libopus dred packet bandwidth %v", bandwidth)
	}
}
