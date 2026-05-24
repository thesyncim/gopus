package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	mspkg "github.com/thesyncim/gopus/multistream"
)

var multistreamRefdecodeHelper libopustest.HelperCache

func runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, gainQ8, sampleFormat int, mapping []byte, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := multistreamRefdecodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "multistream reference decode",
		OutputBase: "gopus_libopus_refdecode_public_multistream",
		SourceFile: "libopus_refdecode_multistream.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayloadVersion(
		"GMSI",
		4,
		uint32(sampleRate),
		uint32(int32(gainQ8)),
		uint32(sampleFormat),
		1,
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		0,
	)
	payload.Raw(mapping)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "multistream reference decode", "GMSO")
}

func decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int16, error) {
	reader, err := runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, gainQ8, 1, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	out := make([]int16, nSamples)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]float32, error) {
	reader, err := runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, 0, 0, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]float32, nSamples)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestMultistreamDecodeUsesAPIRatePacketDuration(t *testing.T) {
	modes := []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	}
	for _, mode := range modes {
		mode := mode
		for _, channels := range []int{1, 2} {
			channels := channels
			packet := mode.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				sampleRate := sampleRate
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					want, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					if got, err := mspkg.PacketDurationAtRate(packet, 1, sampleRate); err != nil || got != want {
						t.Fatalf("multistream PacketDurationAtRate()=(%d,%v) want (%d,nil)", got, err, want)
					}

					smallDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					if _, err := smallDec.Decode(packet, make([]float32, want*channels-1)); err != ErrBufferTooSmall {
						t.Fatalf("Decode small-buffer error=%v want %v", err, ErrBufferTooSmall)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					pcm := make([]float32, want*channels)
					n, err := dec.Decode(packet, pcm)
					if err != nil {
						t.Fatalf("Decode: %v", err)
					}
					if n != want {
						t.Fatalf("Decode samples=%d want %d", n, want)
					}
					if got := dec.LastPacketDuration(); got != want {
						t.Fatalf("LastPacketDuration()=%d want %d", got, want)
					}

					plc := make([]float32, want*channels)
					n, err = dec.Decode(nil, plc)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != want {
						t.Fatalf("Decode(nil) samples=%d want %d", n, want)
					}
					if got := dec.LastPacketDuration(); got != want {
						t.Fatalf("LastPacketDuration() after PLC=%d want %d", got, want)
					}

					intDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					pcm16 := make([]int16, want*channels)
					n, err = intDec.DecodeInt16(packet, pcm16)
					if err != nil {
						t.Fatalf("DecodeInt16: %v", err)
					}
					if n != want {
						t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
					}
				})
			}
		}
	}
}

func TestMultistreamDecodeFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modes := []struct {
		name      string
		packet    func(*testing.T, int) []byte
		tolerance float64
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket, tolerance: 8e-3},
		{name: "celt", packet: encodeAPIRateCELTPacket, tolerance: 3e-3},
		{name: "hybrid", packet: encodeAPIRateHybridPacket, tolerance: 1e-2},
	}
	for _, mode := range modes {
		for _, channels := range []int{1, 2} {
			packet := mode.packet(t, channels)
			streams := 1
			coupled := channels - 1
			mapping := []byte{0}
			if channels == 2 {
				mapping = []byte{0, 1}
			}
			for _, sampleRate := range []int{16000, 48000} {
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, frameSize, mapping, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "multistream float32 reference decode", err)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode(packet): %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode(packet)=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode(nil)=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
					assertAPIRateFloat32Close(t, got, want, "multistream "+mode.name+" float32", mode.tolerance)
				})
			}
		}
	}
}

func TestMultistreamDecodeRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modes := []struct {
		name      string
		packet    func(*testing.T, int) []byte
		tolerance float64
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket, tolerance: 8e-3},
		{name: "celt", packet: encodeAPIRateCELTPacket, tolerance: 3e-3},
		{name: "hybrid", packet: encodeAPIRateHybridPacket, tolerance: 1e-2},
	}
	for _, mode := range modes {
		for _, channels := range []int{1, 2} {
			packet := mode.packet(t, channels)
			streams := 1
			coupled := channels - 1
			mapping := []byte{0}
			if channels == 2 {
				mapping = []byte{0, 1}
			}
			for _, sampleRate := range []int{8000, 16000, 48000} {
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					requestedFrameSize := sampleRate / 25
					if requestedFrameSize == packetFrameSize {
						t.Fatalf("requestedFrameSize=%d unexpectedly equals packet frame size", requestedFrameSize)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, requestedFrameSize, mapping, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "multistream requested PLC reference decode", err)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode(packet): %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode(packet)=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)
					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("Decode(nil)=%d want %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)
					assertAPIRateFloat32Close(t, got, want, "multistream "+mode.name+" requested PLC", mode.tolerance)
				})
			}
		}
	}
}

func TestMultistreamDecodeInt16HighGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 2
		streams    = 1
		coupled    = 1
		frameSize  = sampleRate / 50
		gainQ8     = 8192
	)
	mapping := []byte{0, 1}
	packet := encodeAPIRateCELTPacket(t, channels)
	sequence := [][]byte{packet, nil}
	want, err := decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream int16 gain reference decode", err)
	}

	dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got := make([]int16, 0, len(want))
	frame := make([]int16, frameSize*channels)
	n, err := dec.DecodeInt16(packet, frame)
	if err != nil {
		t.Fatalf("DecodeInt16(packet): %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeInt16(packet)=%d want %d", n, frameSize)
	}
	got = append(got, frame[:n*channels]...)
	clear(frame)
	n, err = dec.DecodeInt16(nil, frame)
	if err != nil {
		t.Fatalf("DecodeInt16(nil): %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeInt16(nil)=%d want %d", n, frameSize)
	}
	got = append(got, frame[:n*channels]...)
	assertAPIRateInt16Equal(t, got, want, "multistream high-gain int16")
}

func TestMultistreamDecodeInvalidRequestedPLCFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		streams := 1
		coupled := channels - 1
		mapping := []byte{0}
		if channels == 2 {
			mapping = []byte{0, 1}
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("float_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, frameSize, mapping, [][]byte{nil}); err == nil {
						t.Fatalf("libopus multistream Decode(nil) accepted frame_size=%d", frameSize)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					n, err := dec.Decode(nil, make([]float32, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
				t.Run("int16_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, frameSize, 0, mapping, [][]byte{nil}); err == nil {
						t.Fatalf("libopus multistream DecodeInt16(nil) accepted frame_size=%d", frameSize)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					n, err := dec.DecodeInt16(nil, make([]int16, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestMultistreamDecodeRejectsNonChannelMultipleRequestedPLCBuffer(t *testing.T) {
	for _, channels := range []int{2, 3} {
		channels := channels
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize := sampleRate / 50
				sampleCount := frameSize*channels + 1

				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				n, err := dec.Decode(nil, make([]float32, sampleCount))
				if n != 0 || err != ErrInvalidFrameSize {
					t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
				}

				intDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				n, err = intDec.DecodeInt16(nil, make([]int16, sampleCount))
				if n != 0 || err != ErrInvalidFrameSize {
					t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
				}
			})
		}
	}
}

func TestMultistreamColdPLCAfterResetUsesAPIRateDefault(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				want := sampleRate / 50

				pcm := make([]float32, want*channels)
				n, err := dec.Decode(nil, pcm)
				if err != nil {
					t.Fatalf("cold Decode(nil): %v", err)
				}
				if n != want {
					t.Fatalf("cold Decode(nil) samples=%d want %d", n, want)
				}
				if got := dec.LastPacketDuration(); got != want {
					t.Fatalf("cold LastPacketDuration()=%d want %d", got, want)
				}

				dec.Reset()
				n, err = dec.Decode(nil, pcm)
				if err != nil {
					t.Fatalf("Decode(nil) after Reset: %v", err)
				}
				if n != want {
					t.Fatalf("Decode(nil) after Reset samples=%d want %d", n, want)
				}
				if got := dec.LastPacketDuration(); got != want {
					t.Fatalf("LastPacketDuration() after reset cold PLC=%d want %d", got, want)
				}
			})
		}
	}
}
