//go:build gopus_libopus_oracle

package gopus

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	encpkg "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func fecParityBitrateForFrameSize(frameSize int) int {
	bitrate := 40000
	if frameSize > 0 && frameSize < 960 {
		bitrate = (40000 * 960) / frameSize
	}
	if bitrate > 320000 {
		bitrate = 320000
	}
	return bitrate
}

func fecParityVoicedSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func fecParityFrame(frameIdx, frameSize, sampleRate, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := fecParityVoicedSample(frameIdx, i, frameSize, sampleRate)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = s
		}
	}
	return pcm
}

func fecParityPCMSequence(frameSize, channels, frameCount int) []float32 {
	pcm := make([]float32, frameSize*channels*frameCount)
	for frameIndex := 0; frameIndex < frameCount; frameIndex++ {
		frame := fecParityFrame(frameIndex, frameSize, 48000, channels)
		copy(pcm[frameIndex*len(frame):], frame)
	}
	return pcm
}

const libopusFECPacketOutputMagic = "GOFC"

var libopusFECEmitPacketsHelper libopustest.HelperCache

func getLibopusFECEmitPacketsHelperPath() (string, error) {
	return libopusFECEmitPacketsHelper.Path(func() (string, error) {
		repoRoot, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		return libopustest.BuildDREDHelper(repoRoot, "libopus_fec_emit_packets.c", "gopus_libopus_fec_emit_packets", true)
	})
}

type libopusFECPacketConfig struct {
	FrameSize   int
	Channels    int
	Bitrate     int
	Application string // "audio" or "voip"
	Signal      string // "music" or "voice"
	InBandFEC   bool
}

func emitLibopusFECPackets(cfg libopusFECPacketConfig, pcm []float32) ([][]byte, error) {
	binPath, err := getLibopusFECEmitPacketsHelperPath()
	if err != nil {
		return nil, err
	}
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = fecParityBitrateForFrameSize(cfg.FrameSize)
	}
	if cfg.Application == "" {
		cfg.Application = "audio"
	}
	if cfg.Signal == "" {
		cfg.Signal = "music"
	}
	env := []string{
		fmt.Sprintf("GOPUS_FEC_FRAME_SIZE=%d", cfg.FrameSize),
		fmt.Sprintf("GOPUS_FEC_CHANNELS=%d", cfg.Channels),
		fmt.Sprintf("GOPUS_FEC_BITRATE=%d", cfg.Bitrate),
		fmt.Sprintf("GOPUS_FEC_APPLICATION=%s", cfg.Application),
		fmt.Sprintf("GOPUS_FEC_SIGNAL=%s", cfg.Signal),
		"GOPUS_FEC_PCM_STDIN=1",
		"GOPUS_FEC_BANDWIDTH=wb",
		fmt.Sprintf("GOPUS_FEC_MAX_FRAMES=%d", len(pcm)/(cfg.FrameSize*cfg.Channels)),
	}
	if !cfg.InBandFEC {
		env = append(env, "GOPUS_FEC_INBAND=0")
	}
	out, err := libopustest.RunHelperEnv(binPath, fecPCMInputLE(pcm), env)
	if err != nil {
		return nil, fmt.Errorf("run fec emit helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderVersion("fec emit", libopusFECPacketOutputMagic, out)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("fec emit helper version=%d want 1", version)
	}
	frameSize := int(reader.U32())
	channels := int(reader.U32())
	count := int(reader.U32())
	if frameSize != cfg.FrameSize || channels != cfg.Channels {
		return nil, fmt.Errorf("fec emit header frameSize=%d channels=%d want %d/%d", frameSize, channels, cfg.FrameSize, cfg.Channels)
	}
	packets := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		packetLen := int(reader.U32())
		packet := reader.Bytes(packetLen)
		packets = append(packets, append([]byte(nil), packet...))
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return packets, nil
}

func fecPCMInputLE(pcm []float32) []byte {
	out := make([]byte, len(pcm)*4)
	var tmp [4]byte
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(tmp[:], math.Float32bits(v))
		copy(out[i*4:(i+1)*4], tmp[:])
	}
	return out
}

func encodeGopusFECPackets(cfg libopusFECPacketConfig, frameCount int) ([][]byte, error) {
	return encodeGopusFECPacketsWithComplexity(cfg, frameCount, 10)
}

func encodeGopusFECPacketsWithComplexity(cfg libopusFECPacketConfig, frameCount, complexity int) ([][]byte, error) {
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = fecParityBitrateForFrameSize(cfg.FrameSize)
	}
	app := ApplicationAudio
	if cfg.Application == "voip" {
		app = ApplicationVoIP
	}
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    cfg.Channels,
		Application: app,
	})
	if err != nil {
		return nil, err
	}
	if err := enc.SetFrameSize(cfg.FrameSize); err != nil {
		return nil, err
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		return nil, err
	}
	if err := enc.SetBitrate(cfg.Bitrate); err != nil {
		return nil, err
	}
	sig := SignalMusic
	if cfg.Signal == "voice" {
		sig = SignalVoice
	}
	if err := enc.SetSignal(sig); err != nil {
		return nil, err
	}
	if cfg.InBandFEC {
		enc.SetFEC(true)
	}
	if err := enc.SetPacketLoss(20); err != nil {
		return nil, err
	}
	if err := enc.SetComplexity(complexity); err != nil {
		return nil, err
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	if cfg.Channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			return nil, err
		}
	}
	enc.enc.SetMode(encpkg.ModeSILK)

	packets := make([][]byte, 0, frameCount)
	for frameIndex := 0; frameIndex < frameCount; frameIndex++ {
		pcm := fecParityFrame(frameIndex, cfg.FrameSize, 48000, cfg.Channels)
		packet := make([]byte, maxPacketBytesPerStream)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			return nil, fmt.Errorf("encode frame %d: %w", frameIndex, err)
		}
		if n == 0 {
			return nil, fmt.Errorf("encode frame %d produced no packet", frameIndex)
		}
		gotPacket := append([]byte(nil), packet[:n]...)
		toc := ParseTOC(gotPacket[0])
		if toc.Mode != ModeSILK {
			return nil, fmt.Errorf("encode frame %d mode=%v want silk", frameIndex, toc.Mode)
		}
		packets = append(packets, gotPacket)
	}
	return packets, nil
}
