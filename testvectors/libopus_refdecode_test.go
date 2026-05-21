package testvectors

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusRefdecodeSingleFormatFloat32 = uint32(0)
	libopusRefdecodeSingleFormatInt16   = uint32(1)
)

var libopusRefdecodeSingleHelper libopustest.HelperCache

func getLibopusRefdecodeSinglePath() (string, error) {
	return libopusRefdecodeSingleHelper.Path(func() (string, error) {
		if _, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); !ok {
			return "", fmt.Errorf("libopus reference tree not found")
		}
		return libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "single reference decode",
			OutputBase: "gopus_libopus_refdecode_single",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		})
	})
}

func runLibopusReferencePacketsSingle(channels, frameSize int, packets [][]byte, sampleFormat uint32) (*libopustest.OracleReader, error) {
	binPath, err := getLibopusRefdecodeSinglePath()
	if err != nil {
		return nil, err
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("unsupported single-stream channel count: %d", channels)
	}

	payload := libopustest.NewOraclePayloadVersion("GOSI", 2, sampleFormat, uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "single reference decode", "GOSO")
}

func decodeWithLibopusReferencePacketsSingle(channels, frameSize int, packets [][]byte) ([]float32, error) {
	reader, err := runLibopusReferencePacketsSingle(channels, frameSize, packets, libopusRefdecodeSingleFormatFloat32)
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

func decodeWithLibopusReferencePacketsSingleInt16(channels, frameSize int, packets [][]byte) ([]int16, error) {
	reader, err := runLibopusReferencePacketsSingle(channels, frameSize, packets, libopusRefdecodeSingleFormatInt16)
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

func TestLibopusReferenceDecodeInt16Helper(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name     string
		channels int
	}{
		{name: "mono", channels: 1},
		{name: "stereo", channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 960
			packets := encodeReferenceDecodePackets(t, tc.channels, frameSize, 6)
			decodedI16, err := decodeWithLibopusReferencePacketsSingleInt16(tc.channels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "single reference decode int16", err)
			}
			decodedF32, err := decodeWithLibopusReferencePacketsSingle(tc.channels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "single reference decode float32", err)
			}

			if len(decodedI16) != len(decodedF32) {
				t.Fatalf("decoded int16 samples=%d float32 samples=%d", len(decodedI16), len(decodedF32))
			}
			if len(decodedI16) == 0 {
				t.Fatal("int16 helper returned no samples")
			}
			nonZero := false
			for _, sample := range decodedI16 {
				if sample != 0 {
					nonZero = true
					break
				}
			}
			if !nonZero {
				t.Fatal("int16 helper returned only silence")
			}
		})
	}
}

func TestDecodeInt16ColdPLCMatchesLibopusReference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name     string
		channels int
	}{
		{name: "mono", channels: 1},
		{name: "stereo", channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 960
			want, err := decodeWithLibopusReferencePacketsSingleInt16(tc.channels, frameSize, [][]byte{nil})
			if err != nil {
				libopustest.HelperUnavailable(t, "single reference decode int16 plc", err)
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, tc.channels))
			if err != nil {
				t.Fatalf("create decoder: %v", err)
			}
			got := make([]int16, frameSize*tc.channels)
			n, err := dec.DecodeInt16(nil, got)
			if err != nil {
				t.Fatalf("DecodeInt16(nil): %v", err)
			}
			got = got[:n*tc.channels]
			if len(got) != len(want) {
				t.Fatalf("DecodeInt16(nil) samples=%d want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("DecodeInt16(nil) sample[%d]=%d want %d", i, got[i], want[i])
				}
			}
		})
	}
}

func encodeReferenceDecodePackets(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("create encoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("set frame size: %v", err)
	}

	pcm := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)
	packets := make([][]byte, 0, frames)
	for frame := 0; frame < frames; frame++ {
		for i := 0; i < frameSize; i++ {
			base := float32(((frame*frameSize+i)*73)%20000-10000) / 24000
			for ch := 0; ch < channels; ch++ {
				pcm[i*channels+ch] = base * float32(ch+1) / float32(channels)
			}
		}
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("encode frame %d: %v", frame, err)
		}
		if n > 0 {
			packets = append(packets, append([]byte(nil), packet[:n]...))
		}
	}
	if len(packets) == 0 {
		t.Fatal("encoder produced no packets")
	}
	return packets
}
