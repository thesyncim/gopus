package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	mspkg "github.com/thesyncim/gopus/multistream"
)

func assertMultistreamCallerFloatBits(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("PCM length=%d want %d", len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("PCM[%d] bits=%08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func multistreamCallerHasSignalFloat32(pcm []float32) bool {
	for _, sample := range pcm {
		if sample != 0 && !math.IsNaN(float64(sample)) && !math.IsInf(float64(sample), 0) {
			return true
		}
	}
	return false
}

func multistreamCallerHasSignalInt16(pcm []int16) bool {
	for _, sample := range pcm {
		if sample != 0 {
			return true
		}
	}
	return false
}

func multistreamCallerHasSignalInt24(pcm []int32) bool {
	for _, sample := range pcm {
		if sample != 0 {
			return true
		}
	}
	return false
}

func TestMultistreamCallerBuffersMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	const channels = 2
	const sentinel = uint32(0x7fc12345)
	for _, tc := range []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{"celt", encodeAPIRateCELTPacket},
		{"silk", encodeAPIRateSILKPacket},
		{"hybrid", encodeAPIRateHybridPacket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := tc.packet(t, channels)
			mapping := []byte{0, 1}
			packets := [][]byte{packet}
			want32, err := decodeLibopusMultistreamFloat32(48000, channels, 1, 1, frameSize, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "float32 multistream oracle", err)
			}
			want16, err := decodeLibopusMultistreamInt16Gain(48000, channels, 1, 1, frameSize, 0, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "int16 multistream oracle", err)
			}
			want24, err := decodeLibopusMultistreamInt24(48000, channels, 1, 1, frameSize, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "int24 multistream oracle", err)
			}

			floatDec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			floatPCM := make([]float32, frameSize*channels+1)
			for i := range floatPCM {
				floatPCM[i] = math.Float32frombits(sentinel)
			}
			n, err := floatDec.Decode(packet, floatPCM)
			if err != nil || n != frameSize {
				t.Fatalf("Decode=(%d,%v) want (%d,nil)", n, err, frameSize)
			}
			assertMultistreamCallerFloatBits(t, floatPCM[:n*channels], want32)
			if math.Float32bits(floatPCM[n*channels]) != sentinel {
				t.Fatal("Decode modified the incomplete-channel tail")
			}

			shortDec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			shortPCM := make([]int16, frameSize*channels+1)
			for i := range shortPCM {
				shortPCM[i] = 12345
			}
			n, err = shortDec.DecodeInt16(packet, shortPCM)
			if err != nil || n != frameSize {
				t.Fatalf("DecodeInt16=(%d,%v) want (%d,nil)", n, err, frameSize)
			}
			for i, want := range want16 {
				if shortPCM[i] != want {
					t.Fatalf("int16 PCM[%d]=%d want %d", i, shortPCM[i], want)
				}
			}
			if shortPCM[n*channels] != 12345 {
				t.Fatal("DecodeInt16 modified the incomplete-channel tail")
			}

			int24Dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			int24PCM := make([]int32, frameSize*channels+1)
			for i := range int24PCM {
				int24PCM[i] = 1234567
			}
			n, err = int24Dec.DecodeInt24(packet, int24PCM)
			if err != nil || n != frameSize {
				t.Fatalf("DecodeInt24=(%d,%v) want (%d,nil)", n, err, frameSize)
			}
			for i, want := range want24 {
				if int24PCM[i] != want {
					t.Fatalf("int24 PCM[%d]=%d want %d", i, int24PCM[i], want)
				}
			}
			if int24PCM[n*channels] != 1234567 {
				t.Fatal("DecodeInt24 modified the incomplete-channel tail")
			}
		})
	}
}

func TestMultistreamCallerBufferWarmZeroAlloc(t *testing.T) {
	const frameSize = 960
	const channels = 2
	for _, tc := range []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{"celt", encodeAPIRateCELTPacket},
		{"silk", encodeAPIRateSILKPacket},
		{"hybrid", encodeAPIRateHybridPacket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := tc.packet(t, channels)
			floatDec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			int16Dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			int24Dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			floatPCM := make([]float32, frameSize*channels)
			int16PCM := make([]int16, frameSize*channels)
			int24PCM := make([]int32, frameSize*channels)
			for range 3 {
				if _, err := floatDec.Decode(packet, floatPCM); err != nil {
					t.Fatal(err)
				}
				if _, err := floatDec.Decode(nil, floatPCM); err != nil {
					t.Fatal(err)
				}
				if !multistreamCallerHasSignalFloat32(floatPCM) {
					t.Fatal("float PLC did not exercise active nonzero PCM")
				}
				if _, err := int16Dec.DecodeInt16(packet, int16PCM); err != nil {
					t.Fatal(err)
				}
				if _, err := int16Dec.DecodeInt16(nil, int16PCM); err != nil {
					t.Fatal(err)
				}
				if !multistreamCallerHasSignalInt16(int16PCM) {
					t.Fatal("int16 PLC did not exercise active nonzero PCM")
				}
				if _, err := int24Dec.DecodeInt24(packet, int24PCM); err != nil {
					t.Fatal(err)
				}
				if _, err := int24Dec.DecodeInt24(nil, int24PCM); err != nil {
					t.Fatal(err)
				}
				if !multistreamCallerHasSignalInt24(int24PCM) {
					t.Fatal("int24 PLC did not exercise active nonzero PCM")
				}
			}
			if got := testing.AllocsPerRun(50, func() {
				if n, err := floatDec.Decode(packet, floatPCM); err != nil || n != frameSize {
					t.Fatalf("float packet=(%d,%v)", n, err)
				}
				if n, err := floatDec.Decode(nil, floatPCM); err != nil || n != frameSize {
					t.Fatalf("float PLC=(%d,%v)", n, err)
				}
			}); got != 0 {
				t.Fatalf("warm float packet+active PLC allocations=%v want 0", got)
			}
			if !multistreamCallerHasSignalFloat32(floatPCM) {
				t.Fatal("measured float PLC did not exercise active nonzero PCM")
			}
			if got := testing.AllocsPerRun(50, func() {
				if n, err := int16Dec.DecodeInt16(packet, int16PCM); err != nil || n != frameSize {
					t.Fatalf("int16 packet=(%d,%v)", n, err)
				}
				if n, err := int16Dec.DecodeInt16(nil, int16PCM); err != nil || n != frameSize {
					t.Fatalf("int16 PLC=(%d,%v)", n, err)
				}
			}); got != 0 {
				t.Fatalf("warm int16 packet+active PLC allocations=%v want 0", got)
			}
			if !multistreamCallerHasSignalInt16(int16PCM) {
				t.Fatal("measured int16 PLC did not exercise active nonzero PCM")
			}
			if got := testing.AllocsPerRun(50, func() {
				if n, err := int24Dec.DecodeInt24(packet, int24PCM); err != nil || n != frameSize {
					t.Fatalf("int24 packet=(%d,%v)", n, err)
				}
				if n, err := int24Dec.DecodeInt24(nil, int24PCM); err != nil || n != frameSize {
					t.Fatalf("int24 PLC=(%d,%v)", n, err)
				}
			}); got != 0 {
				t.Fatalf("warm int24 packet+active PLC allocations=%v want 0", got)
			}
			if !multistreamCallerHasSignalInt24(int24PCM) {
				t.Fatal("measured int24 PLC did not exercise active nonzero PCM")
			}
		})
	}
}

func TestMultistreamCallerBufferMappingAndRetry(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := encodeAPIRateSILKPacket(t, 2)
	mapping := []byte{0, 0, 1, 255}
	want, err := decodeLibopusMultistreamFloat32(48000, 4, 1, 1, 960, mapping, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "mapped multistream oracle", err)
	}
	dec, err := NewMultistreamDecoder(48000, 4, 1, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(packet, make([]float32, 960*4-1)); err != ErrBufferTooSmall {
		t.Fatalf("short Decode error=%v want %v", err, ErrBufferTooSmall)
	}
	const sentinel = uint32(0x7fc12345)
	pcm := make([]float32, 5760*4+3)
	for i := range pcm {
		pcm[i] = math.Float32frombits(sentinel)
	}
	n, err := dec.Decode(packet, pcm)
	if err != nil || n != 960 {
		t.Fatalf("Decode retry=(%d,%v) want (960,nil)", n, err)
	}
	assertMultistreamCallerFloatBits(t, pcm[:n*4], want)
	for i := n * 4; i < len(pcm); i++ {
		if math.Float32bits(pcm[i]) != sentinel {
			t.Fatalf("output tail[%d] was overwritten", i)
		}
	}
	seenSignal := false
	for s := range n {
		if math.Float32bits(pcm[s*4]) != math.Float32bits(pcm[s*4+1]) {
			t.Fatalf("duplicate mapping differs at sample %d", s)
		}
		if pcm[s*4+3] != 0 {
			t.Fatalf("muted channel[%d]=%v want zero", s, pcm[s*4+3])
		}
		if pcm[s*4] != 0 || pcm[s*4+2] != 0 {
			seenSignal = true
		}
	}
	if !seenSignal {
		t.Fatal("mapped packet did not exercise nonzero PCM")
	}
	fresh, err := NewMultistreamDecoder(48000, 4, 1, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	freshPCM := make([]float32, n*4)
	if _, err := fresh.Decode(packet, freshPCM); err != nil {
		t.Fatal(err)
	}
	assertMultistreamCallerFloatBits(t, pcm[:n*4], freshPCM)
	if dec.GetFinalRange() != fresh.GetFinalRange() {
		t.Fatal("short-buffer retry changed final range")
	}
}

func TestMultistreamCallerBufferDTXAndOwnedOutput(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := encodeAPIRateSILKPacket(t, 1)
	dtx := []byte{0x11} // two empty 40 ms SILK frames in one code-1 packet
	want, err := decodeLibopusMultistreamFloat32(48000, 1, 1, 0, 3840, []byte{0}, [][]byte{packet, dtx})
	if err != nil {
		libopustest.HelperUnavailable(t, "two-frame DTX oracle", err)
	}
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 1)
	first := make([]float32, 960)
	if n, err := dec.Decode(packet, first); err != nil || n != 960 {
		t.Fatalf("good packet=(%d,%v)", n, err)
	}
	concealed := make([]float32, 3840)
	if n, err := dec.Decode(dtx, concealed); err != nil || n != 3840 {
		t.Fatalf("DTX packet=(%d,%v)", n, err)
	}
	assertMultistreamCallerFloatBits(t, concealed, want[960:])

	ownedDec, err := mspkg.NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := ownedDec.DecodeToFloat32(packet, 960)
	if err != nil {
		t.Fatal(err)
	}
	saved := append([]float32(nil), owned...)
	twinDec, err := mspkg.NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := twinDec.DecodeToFloat32(packet, 960); err != nil {
		t.Fatal(err)
	}
	caller := make([]float32, 960)
	if _, err := ownedDec.DecodeIntoFloat32(nil, caller, 960); err != nil {
		t.Fatal(err)
	}
	twinCaller := make([]float32, 960)
	if _, err := twinDec.DecodeIntoFloat32(nil, twinCaller, 960); err != nil {
		t.Fatal(err)
	}
	for i := range caller {
		caller[i] = math.Float32frombits(0x7fc12345)
	}
	recovery := make([]float32, 960)
	twinRecovery := make([]float32, 960)
	if _, err := ownedDec.DecodeIntoFloat32(packet, recovery, 960); err != nil {
		t.Fatal(err)
	}
	if _, err := twinDec.DecodeIntoFloat32(packet, twinRecovery, 960); err != nil {
		t.Fatal(err)
	}
	assertMultistreamCallerFloatBits(t, recovery, twinRecovery)
	if ownedDec.FinalRange() != twinDec.FinalRange() {
		t.Fatal("mutating caller PCM changed recovery range")
	}
	ownedDec.Reset()
	assertMultistreamCallerFloatBits(t, owned, saved)
}
