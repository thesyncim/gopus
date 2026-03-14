package gopus

import (
	"math"
	"testing"
)

func addSafetyDecodeFuzzSeeds(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x03},
		{0x02, 0x01},
		{0xFF},
		minimalHybridTestPacket20ms(),
		minimalHybridTestPacket20msStereo(),
	}
	for _, seed := range seeds {
		f.Add(append([]byte(nil), seed...))
	}

	if packet, err := safetyEncodedSeedPacket(1); err == nil && len(packet) > 0 {
		f.Add(packet)
	}
	if packet, err := safetyEncodedSeedPacket(2); err == nil && len(packet) > 0 {
		f.Add(packet)
	}
}

func safetyEncodedSeedPacket(channels int) ([]byte, error) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		return nil, err
	}
	pcm := generateSineWaveFloat32(48000, 440, enc.FrameSize(), channels)
	return enc.EncodeFloat32(pcm)
}

func requireFinitePCM(t *testing.T, samples []float32) {
	t.Helper()

	for i, sample := range samples {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatalf("sample[%d] is not finite: %v", i, sample)
		}
	}
}

func FuzzDecodeNeverPanics(f *testing.F) {
	addSafetyDecodeFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}

		for _, channels := range []int{1, 2} {
			cfg := DefaultDecoderConfig(48000, channels)
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatalf("NewDecoder(%d) error: %v", channels, err)
			}

			pcm := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
			n, err := dec.Decode(data, pcm)
			if err == nil {
				if n < 0 || n > cfg.MaxPacketSamples {
					t.Fatalf("Decode(%dch) samples=%d outside [0,%d]", channels, n, cfg.MaxPacketSamples)
				}
				requireFinitePCM(t, pcm[:n*channels])
			}

			plcN, plcErr := dec.Decode(nil, pcm)
			if plcErr != nil {
				t.Fatalf("Decode(nil) after fuzz packet failed for %dch: %v", channels, plcErr)
			}
			if plcN < 0 || plcN > cfg.MaxPacketSamples {
				t.Fatalf("Decode(nil) samples=%d outside [0,%d]", plcN, cfg.MaxPacketSamples)
			}
			requireFinitePCM(t, pcm[:plcN*channels])
		}
	})
}
