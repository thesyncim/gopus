package testvectors

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

type decoderFuzzSeed struct {
	packet     []byte
	finalRange uint32
	channels   int
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
			packet:     append([]byte(nil), packets[0]...),
			finalRange: c.Packets[0].FinalRange,
			channels:   c.Channels,
		})

		lastIdx := len(packets) - 1
		if lastIdx > 0 {
			seeds = append(seeds, decoderFuzzSeed{
				packet:     append([]byte(nil), packets[lastIdx]...),
				finalRange: c.Packets[lastIdx].FinalRange,
				channels:   c.Channels,
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

func singlePacketBitstream(packet []byte, finalRange uint32) []byte {
	out := make([]byte, 8+len(packet))
	binary.BigEndian.PutUint32(out[:4], uint32(len(packet)))
	binary.BigEndian.PutUint32(out[4:8], finalRange)
	copy(out[8:], packet)
	return out
}

func decodeSinglePacketWithOpusDemo(opusDemo string, packet []byte, finalRange uint32, channels int) ([]float32, error) {
	tmpDir, err := os.MkdirTemp("", "gopus-decode-fuzz-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	bitPath := filepath.Join(tmpDir, "input.bit")
	outPath := filepath.Join(tmpDir, "output.f32")
	if err := os.WriteFile(bitPath, singlePacketBitstream(packet, finalRange), 0o644); err != nil {
		return nil, err
	}

	cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprintf("%d", channels), "-f32", bitPath, outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("opus_demo decode failed: %v (%s)", err, out)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	return decodeRawFloat32LE(raw)
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
	opusDemo, ok := getFixtureOpusDemoPath()
	if !ok {
		f.Skip("tmp_check opus_demo not found; skipping libopus differential fuzz")
	}
	seeds := decoderMatrixFuzzSeeds()
	if len(seeds) == 0 {
		f.Skip("decoder matrix fixture unavailable; skipping libopus differential fuzz")
	}

	f.Add(uint8(0), []byte{})
	f.Add(uint8(1), []byte{0x01})
	f.Add(uint8(2), []byte{0x02, 0xFF, 0x10})
	f.Add(uint8(3), []byte{0x04, 0x00, 0x7F, 0x80})

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
		// mutations are still covered by the no-panic fuzzers, but opus_demo does
		// not reliably surface those parser failures as non-zero exits for
		// accept/reject comparisons.
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

		refSamples, refErr := decodeSinglePacketWithOpusDemo(opusDemo, packet, seed.finalRange, channels)
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
	})
}
