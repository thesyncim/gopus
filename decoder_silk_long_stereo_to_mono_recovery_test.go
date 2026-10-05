package gopus

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestSILKLongStereoToMonoRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	// Fixed wire packets keep this recovery witness independent of encoder behavior.
	mono40, err := hex.DecodeString("50c100aeb623947d81bcc7980058ef2539604e53c2529c15b9499cb7d7dc318eccc9b705c399330c3ba8c4a8f9dd83081768b4c5cfe84c6ca76f3f89e6834b595ae4cbee4165a7eef36c43d23de0cd3520664548400d75e22dda891378b920b5ea9956717905f4692f36ed646edcaa643398c37d36849e9508490f3d6300ae1cc34150d371c3e713840ef0")
	if err != nil {
		t.Fatal(err)
	}
	stereo40, err := hex.DecodeString("54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c099f092391240c7160421137e84a80effbb35c2e0f817a")
	if err != nil {
		t.Fatal(err)
	}
	if mono40[0]&4 != 0 || stereo40[0]&4 == 0 {
		t.Fatalf("packet channel flags are mono=%02x stereo=%02x", mono40[0], stereo40[0])
	}

	type outputFormat struct {
		name string
		id   uint32
	}
	formats := []outputFormat{
		{name: "float32", id: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", id: libopustest.DecodeDiffFormatInt16},
		{name: "int24", id: libopustest.DecodeDiffFormatInt24},
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		packetFrameSize := sampleRate * 40 / 1000
		plcFrameSize := sampleRate * 5 / 2000
		packets := [][]byte{mono40, nil, stereo40, nil, mono40}
		frameSizes := []int{packetFrameSize, plcFrameSize, packetFrameSize, plcFrameSize, packetFrameSize}

		for _, format := range formats {
			format := format
			t.Run(fmt.Sprintf("rate%d/%s", sampleRate, format.name), func(t *testing.T) {
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for step, packet := range packets {
					cases[step] = libopustest.DecodeDiffCase{
						Packet:    packet,
						Format:    format.id,
						FrameSize: uint32(frameSizes[step]),
					}
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, cases)
				if err != nil {
					t.Fatalf("ProbeDecodeSequence: %v", err)
				}
				if len(want) != len(packets) {
					t.Fatalf("oracle returned %d calls, want %d", len(want), len(packets))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				maxOutputSamples := packetFrameSize * 2
				pcmFloat := make([]float32, maxOutputSamples)
				pcm16 := make([]int16, maxOutputSamples)
				pcm24 := make([]int32, maxOutputSamples)
				decode := func(packet []byte, frameSize int) (int, error) {
					samples := frameSize * 2
					switch format.id {
					case libopustest.DecodeDiffFormatFloat32:
						return dec.Decode(packet, pcmFloat[:samples])
					case libopustest.DecodeDiffFormatInt16:
						return dec.DecodeInt16(packet, pcm16[:samples])
					default:
						return dec.DecodeInt24(packet, pcm24[:samples])
					}
				}

				for step, packet := range packets {
					n, err := decode(packet, frameSizes[step])
					if err != nil || int32(n) != want[step].Code {
						t.Fatalf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code)
					}
					if want[step].Code != int32(frameSizes[step]) {
						t.Fatalf("C step%d returned %d samples, want %d", step, want[step].Code, frameSizes[step])
					}
					if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
						t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
					}

					sampleCount := n * 2
					bytesPerSample := 4
					if format.id == libopustest.DecodeDiffFormatInt16 {
						bytesPerSample = 2
					}
					if got, expected := len(want[step].PCM), sampleCount*bytesPerSample; got != expected {
						t.Fatalf("step%d oracle PCM bytes=%d, want %d", step, got, expected)
					}
					switch format.id {
					case libopustest.DecodeDiffFormatFloat32:
						for sample := 0; sample < sampleCount; sample++ {
							got := math.Float32bits(pcmFloat[sample])
							expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
							if got != expected {
								t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
							}
						}
					case libopustest.DecodeDiffFormatInt16:
						for sample := 0; sample < sampleCount; sample++ {
							got := pcm16[sample]
							expected := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:]))
							if got != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
							}
						}
					default:
						for sample := 0; sample < sampleCount; sample++ {
							got := pcm24[sample]
							expected := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:]))
							if got != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
							}
						}
					}
				}

				runSequence := func() error {
					dec.Reset()
					for step, packet := range packets {
						n, err := decode(packet, frameSizes[step])
						if err != nil {
							return err
						}
						if n != frameSizes[step] {
							return fmt.Errorf("step%d returned %d samples, want %d", step, n, frameSizes[step])
						}
					}
					return nil
				}
				if err := runSequence(); err != nil {
					t.Fatalf("warm reset sequence: %v", err)
				}
				var runErr error
				allocs := testing.AllocsPerRun(10, func() {
					runErr = runSequence()
				})
				if runErr != nil {
					t.Fatalf("measured reset sequence: %v", runErr)
				}
				if allocs != 0 {
					t.Fatalf("warm reset sequence allocations=%g", allocs)
				}
			})
		}
	}
}
