package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

type decoderFuzzSeed struct {
	packet   []byte
	channels int
}

const maxOpusPacketSamples48k = 5760
const maxOpusFrameBytes = 1275

func packetWithinDifferentialScope(info gopus.PacketInfo) bool {
	if info.TOC.FrameSize*info.FrameCount > maxOpusPacketSamples48k {
		return false
	}
	for _, frameSize := range info.FrameSizes {
		if frameSize <= 0 || frameSize > maxOpusFrameBytes {
			return false
		}
	}
	return true
}

func decoderMatrixFuzzSeeds() []decoderFuzzSeed {
	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		return nil
	}

	seeds := make([]decoderFuzzSeed, 0, len(fixture.Cases)*2)
	for _, c := range fixture.Cases {
		packets, err := decodeLibopusDecoderMatrixPackets(c)
		if err != nil || len(packets) == 0 {
			continue
		}

		seeds = append(seeds, decoderFuzzSeed{
			packet:   append([]byte(nil), packets[0]...),
			channels: c.Channels,
		})

		lastIdx := len(packets) - 1
		if lastIdx > 0 {
			seeds = append(seeds, decoderFuzzSeed{
				packet:   append([]byte(nil), packets[lastIdx]...),
				channels: c.Channels,
			})
		}
	}

	return seeds
}

func normalizeFuzzChannels(channels int) int {
	if channels%2 == 0 {
		return 2
	}
	return 1
}

func decodeSinglePacketWithLibopusReference(channels int, packet []byte) ([]float32, error) {
	return decodeWithLibopusReferencePacketsSingle(channels, maxOpusPacketSamples48k, [][]byte{packet})
}

func requireDecodedWaveformParity(t *testing.T, got, ref []float32, channels int) {
	t.Helper()

	if len(got) == 0 || len(ref) == 0 {
		t.Fatalf("decoded waveform missing: gopus=%d libopus=%d", len(got), len(ref))
	}

	frames := len(ref) / channels
	if len(got) < len(ref) {
		frames = len(got) / channels
	}
	if frames >= 480 {
		q, delay, err := ComputeOpusCompareQualityFloat32WithDelay(ref[:frames*channels], got[:frames*channels], 48000, channels, differentialFuzzPacketMaxDelay)
		if err != nil {
			t.Fatalf("compute fuzz opus_compare parity: %v", err)
		}
		if q < differentialFuzzPacketMinQ {
			t.Fatalf("decoded waveform quality mismatch: Q=%.2f < %.2f delay=%d", q, differentialFuzzPacketMinQ, delay)
		}
		return
	}

	gotPCM := float32ToPCM16(got[:frames*channels])
	refPCM := float32ToPCM16(ref[:frames*channels])
	maxAbsDiff := 0
	sumAbsDiff := 0.0
	for i := range refPCM {
		diff := int(math.Abs(float64(int(gotPCM[i]) - int(refPCM[i]))))
		if diff > maxAbsDiff {
			maxAbsDiff = diff
		}
		sumAbsDiff += float64(diff)
	}
	meanAbsDiff := sumAbsDiff / float64(len(refPCM))
	if maxAbsDiff > differentialFuzzMaxPCM16AbsDiff || meanAbsDiff > differentialFuzzMaxPCM16MeanDiff {
		t.Fatalf(
			"decoded waveform mismatch on short packet: max_abs=%d mean_abs=%.2f samples=%d",
			maxAbsDiff,
			meanAbsDiff,
			len(refPCM),
		)
	}
}

func requireFiniteDecodedSamples(t *testing.T, samples []float32) {
	t.Helper()

	for i, sample := range samples {
		if sample != sample || sample > 1e38 || sample < -1e38 {
			t.Fatalf("decoded sample[%d] is not finite: %v", i, sample)
		}
	}
}

func mutationByte(mutation []byte, idx *int) byte {
	if len(mutation) == 0 {
		return 0
	}
	b := mutation[*idx%len(mutation)]
	*idx = *idx + 1
	return b
}

func mutateFixturePacket(seed []byte, mutation []byte) []byte {
	packet := append([]byte(nil), seed...)
	if len(mutation) == 0 || len(packet) == 0 {
		return packet
	}

	idx := 0
	edits := 1 + int(mutationByte(mutation, &idx)%4)
	for i := 0; i < edits; i++ {
		pos := int(mutationByte(mutation, &idx)) % len(packet)
		value := mutationByte(mutation, &idx)
		if mutationByte(mutation, &idx)%2 == 0 {
			packet[pos] ^= value
			continue
		}
		packet[pos] = value
	}

	return packet
}

func FuzzDecodeAgainstLibopus(f *testing.F) {
	if _, err := getLibopusRefdecodeSinglePath(); err != nil {
		f.Skipf("libopus reference decode helper unavailable: %v", err)
	}
	seeds := decoderMatrixFuzzSeeds()
	if len(seeds) == 0 {
		f.Skip("decoder matrix fixture unavailable; skipping libopus differential fuzz")
	}

	f.Add(uint8(0), []byte{})
	f.Add(uint8(1), []byte{0x01})
	f.Add(uint8(2), []byte{0x02, 0xFF, 0x10})
	f.Add(uint8(3), []byte{0x04, 0x00, 0x7F, 0x80})
	f.Add(uint8(0x1A), []byte("Q"))
	f.Add(uint8(0xFF), []byte{0x30, 0x79, 0xC0, 0x30})

	f.Fuzz(func(t *testing.T, seedIndex uint8, mutation []byte) {
		seed := seeds[int(seedIndex)%len(seeds)]
		packet := mutateFixturePacket(seed.packet, mutation)
		if len(packet) == 0 {
			return
		}
		if len(packet) < 2 {
			return
		}
		info, err := gopus.ParsePacket(packet)
		if err != nil {
			return
		}
		// Keep differential checks on structurally decodable packets inside the
		// public 120 ms / 1275-byte-per-frame envelope. More aggressively malformed
		// mutations are still covered by the no-panic fuzzers; this lane is for
		// direct libopus API decode comparisons on packets that both sides should
		// meaningfully classify.
		if !packetWithinDifferentialScope(info) {
			return
		}
		channels := normalizeFuzzChannels(seed.channels)

		cfg := gopus.DefaultDecoderConfig(48000, channels)
		dec, err := gopus.NewDecoder(cfg)
		if err != nil {
			t.Fatalf("NewDecoder(%d) error: %v", channels, err)
		}

		pcm := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
		gotN, gotErr := dec.Decode(packet, pcm)
		if gotErr == nil {
			if gotN < 0 || gotN > cfg.MaxPacketSamples {
				t.Fatalf("gopus samples=%d outside [0,%d]", gotN, cfg.MaxPacketSamples)
			}
			requireFiniteDecodedSamples(t, pcm[:gotN*channels])
		}

		refSamples, refErr := decodeSinglePacketWithLibopusReference(channels, packet)
		gotOK := gotErr == nil
		refOK := refErr == nil

		if gotOK != refOK {
			t.Fatalf("accept/reject mismatch: gopus ok=%t err=%v, libopus ok=%t err=%v, len=%d, channels=%d, toc=%+v, frames=%d, sizes=%v, packet=%x", gotOK, gotErr, refOK, refErr, len(packet), channels, info.TOC, info.FrameCount, info.FrameSizes, packet)
		}
		if !gotOK {
			return
		}

		if len(refSamples)%channels != 0 {
			t.Fatalf("libopus decoded %d samples not divisible by channels=%d", len(refSamples), channels)
		}
		if gotN != len(refSamples)/channels {
			t.Fatalf("decoded duration mismatch: gopus=%d samples/ch libopus=%d samples/ch", gotN, len(refSamples)/channels)
		}
		requireFiniteDecodedSamples(t, refSamples)
		requireDecodedWaveformParity(t, pcm[:gotN*channels], refSamples, channels)
	})
}
