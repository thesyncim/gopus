//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var customSignallingHelper libopustest.HelperCache

func customSignallingHelperPath() (string, error) {
	return customSignallingHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "opus custom signalling",
			OutputBase:  "gopus_libopus_custom_signalling_channels",
			SourceFile:  "libopus_custom_signalling_oracle.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
}

type customSignallingCase struct {
	fs, frameSize, channels, decodeChannels, maxBytes int
	pcm                                               []float32
}

type customSignallingResult struct {
	signalling      bool
	packet          []byte
	decoded         []float32
	encRange        uint32
	decRange        uint32
	sampleCount     int
	decodedChannels int
}

var customSignallingAllocationSink byte

func runCustomSignallingOracle(t *testing.T, cases []customSignallingCase) [][]customSignallingResult {
	t.Helper()
	path, err := customSignallingHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "opus custom signalling", err)
		return nil
	}
	var request bytes.Buffer
	request.WriteString("GCSG")
	writeU32(&request, uint32(len(cases)))
	for _, tc := range cases {
		writeU32(&request, uint32(tc.fs))
		writeU32(&request, uint32(tc.frameSize))
		writeU32(&request, uint32(tc.channels))
		writeU32(&request, uint32(tc.decodeChannels))
		writeU32(&request, uint32(tc.maxBytes))
		writeU32(&request, uint32(len(tc.pcm)))
		for _, sample := range tc.pcm {
			writef32(&request, sample)
		}
	}
	response, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom signalling oracle: %v", err)
	}
	r := bytes.NewReader(response)
	magic := make([]byte, 4)
	if _, err := r.Read(magic); err != nil || string(magic) != "GCSG" {
		t.Fatalf("custom signalling oracle magic=%q err=%v", magic, err)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || int(count) != len(cases) {
		t.Fatalf("custom signalling oracle count=%d want=%d err=%v", count, len(cases), err)
	}
	results := make([][]customSignallingResult, len(cases))
	for i, tc := range cases {
		results[i] = make([]customSignallingResult, 2)
		for j := range results[i] {
			var signalling, packetSize, sampleCount int32
			var encRange, decRange uint32
			if err := binary.Read(r, binary.LittleEndian, &signalling); err != nil {
				t.Fatalf("case %d result %d signalling: %v", i, j, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &packetSize); err != nil {
				t.Fatalf("case %d result %d packet size: %v", i, j, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &sampleCount); err != nil {
				t.Fatalf("case %d result %d sample count: %v", i, j, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &encRange); err != nil {
				t.Fatalf("case %d result %d encoder range: %v", i, j, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &decRange); err != nil {
				t.Fatalf("case %d result %d decoder range: %v", i, j, err)
			}
			if packetSize < 0 || int(sampleCount) != tc.frameSize {
				t.Fatalf("case %d result %d packet=%d samples=%d", i, j, packetSize, sampleCount)
			}
			packet := make([]byte, packetSize)
			if _, err := r.Read(packet); err != nil && len(packet) != 0 {
				t.Fatalf("case %d result %d packet: %v", i, j, err)
			}
			decodedChannels := tc.channels
			if signalling != 0 {
				decodedChannels = tc.decodeChannels
			}
			decoded := make([]float32, int(sampleCount)*decodedChannels)
			for k := range decoded {
				var sampleBits uint32
				if err := binary.Read(r, binary.LittleEndian, &sampleBits); err != nil {
					t.Fatalf("case %d result %d pcm[%d]: %v", i, j, k, err)
				}
				decoded[k] = math.Float32frombits(sampleBits)
			}
			results[i][j] = customSignallingResult{
				signalling:      signalling != 0,
				packet:          packet,
				decoded:         decoded,
				encRange:        encRange,
				decRange:        decRange,
				sampleCount:     int(sampleCount),
				decodedChannels: decodedChannels,
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom signalling oracle has %d trailing bytes", r.Len())
	}
	return results
}

func TestCustomSignallingDefaultAndRawMatchLibopus(t *testing.T) {
	var cases []customSignallingCase
	for _, frameSize := range []int{960, 640} {
		for _, channels := range []int{1, 2} {
			for _, decodeChannels := range []int{1, 2} {
				pcm := make([]float32, frameSize*channels)
				for i := 0; i < frameSize; i++ {
					for channel := 0; channel < channels; channel++ {
						frequency := 523.0 + 197.0*float64(channel)
						pcm[i*channels+channel] = float32(0.24 * math.Sin(2*math.Pi*frequency*float64(i)/48000))
					}
				}
				cases = append(cases, customSignallingCase{48000, frameSize, channels, decodeChannels, 200, pcm})
			}
		}
	}
	oracle := runCustomSignallingOracle(t, cases)
	for i, tc := range cases {
		mode, err := custom.NewMode(tc.fs, tc.frameSize)
		if err != nil {
			t.Fatalf("case %d NewMode: %v", i, err)
		}
		for resultIndex, enabled := range []bool{true, false} {
			want := oracle[i][resultIndex]
			if want.signalling != enabled {
				t.Fatalf("case %d oracle result %d signalling=%v", i, resultIndex, want.signalling)
			}
			enc, err := custom.NewEncoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("case %d NewEncoder: %v", i, err)
			}
			if !enc.Signalling() {
				t.Fatalf("case %d encoder signalling default=%v want true", i, enc.Signalling())
			}
			if err := enc.SetVBR(false); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetConstrainedVBR(false); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetComplexity(9); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetLSBDepth(16); err != nil {
				t.Fatal(err)
			}
			if !enabled {
				if err := enc.SetSignalling(false); err != nil {
					t.Fatal(err)
				}
			}
			if enc.Signalling() != enabled {
				t.Fatalf("case %d encoder signalling state=%v want %v", i, enc.Signalling(), enabled)
			}
			packet, err := enc.EncodeFloat(tc.pcm, tc.maxBytes)
			if err != nil {
				t.Fatalf("case %d EncodeFloat signalling=%v: %v", i, enabled, err)
			}
			if !bytes.Equal(packet, want.packet) {
				t.Fatalf("case %d encode signalling=%v packet mismatch\n got %x\nwant %x", i, enabled, packet, want.packet)
			}
			if got := enc.FinalRange(); got != want.encRange {
				t.Fatalf("case %d encode signalling=%v range=%08x want %08x", i, enabled, got, want.encRange)
			}
			if enabled && len(packet) < 2 || !enabled && len(packet) == 0 {
				t.Fatalf("case %d signalling=%v invalid packet length %d", i, enabled, len(packet))
			}

			dec, err := custom.NewDecoder(mode, want.decodedChannels)
			if err != nil {
				t.Fatalf("case %d NewDecoder: %v", i, err)
			}
			if !dec.Signalling() {
				t.Fatalf("case %d decoder signalling default=%v want true", i, dec.Signalling())
			}
			if !enabled {
				if err := dec.SetSignalling(false); err != nil {
					t.Fatal(err)
				}
			}
			if dec.Signalling() != enabled {
				t.Fatalf("case %d decoder signalling state=%v want %v", i, dec.Signalling(), enabled)
			}
			outputCapacity := tc.frameSize
			if enabled {
				outputCapacity++
			}
			pcm, err := dec.DecodeFloat(want.packet, outputCapacity)
			if err != nil {
				t.Fatalf("case %d DecodeFloat signalling=%v: %v", i, enabled, err)
			}
			if len(pcm) != len(want.decoded) {
				t.Fatalf("case %d decode signalling=%v samples=%d want %d", i, enabled, len(pcm), len(want.decoded))
			}
			for sample := range pcm {
				if math.Float32bits(pcm[sample]) != math.Float32bits(want.decoded[sample]) {
					t.Fatalf("case %d decode signalling=%v pcm[%d]=%08x want %08x", i, enabled, sample, math.Float32bits(pcm[sample]), math.Float32bits(want.decoded[sample]))
				}
			}
			if got := dec.FinalRange(); got != want.decRange {
				t.Fatalf("case %d decode signalling=%v range=%08x want %08x", i, enabled, got, want.decRange)
			}
		}
	}
}

func TestCustomSignalledVBRInputAPIsWarmZeroAllocs(t *testing.T) {
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	floatFrames := make([][]float32, 4)
	shortFrames := make([][]int16, len(floatFrames))
	for kind := range floatFrames {
		floatFrames[kind] = make([]float32, mode.FrameSize)
		for i := range floatFrames[kind] {
			switch kind {
			case 0:
				floatFrames[kind][i] = float32(.22 * math.Sin(2*math.Pi*440*float64(i)/48000))
			case 1:
				floatFrames[kind][i] = float32(.2*math.Sin(2*math.Pi*523*float64(i)/48000) + .16*math.Sin(2*math.Pi*1733*float64(i)/48000))
			case 2:
				state := uint32(i*1664525 + 1013904223)
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				floatFrames[kind][i] = float32(int32(state)) * (0.25 / 2147483648.0)
			}
		}
		shortFrames[kind] = make([]int16, len(floatFrames[kind]))
		for i, sample := range floatFrames[kind] {
			shortFrames[kind][i] = int16(sample * 32767)
		}
	}

	newEncoder := func() *custom.CustomEncoder {
		t.Helper()
		enc, err := custom.NewEncoder(mode, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetBitrate(64000); err != nil {
			t.Fatal(err)
		}
		if err := enc.SetVBR(true); err != nil {
			t.Fatal(err)
		}
		return enc
	}

	t.Run("float", func(t *testing.T) {
		enc := newEncoder()
		var step int
		var packetSizes [4]int
		run := func() {
			packet, err := enc.EncodeFloat(floatFrames[step], 500)
			if err != nil {
				panic(err)
			}
			packetSizes[step] = len(packet)
			customSignallingAllocationSink ^= packet[0] ^ packet[len(packet)-1]
			step = (step + 1) % len(floatFrames)
		}
		for range 12 {
			run()
		}
		if got := testing.AllocsPerRun(40, run); got != 0 {
			t.Fatalf("warm signalled VBR float encode allocated %g times per call", got)
		}
		if packetSizes[0] == packetSizes[1] && packetSizes[1] == packetSizes[2] && packetSizes[2] == packetSizes[3] {
			t.Fatalf("VBR inputs did not produce varied packet lengths: %v", packetSizes)
		}
	})

	t.Run("int16", func(t *testing.T) {
		enc := newEncoder()
		var step int
		var packetSizes [4]int
		run := func() {
			packet, err := enc.Encode(shortFrames[step], 500)
			if err != nil {
				panic(err)
			}
			packetSizes[step] = len(packet)
			customSignallingAllocationSink ^= packet[0] ^ packet[len(packet)-1]
			step = (step + 1) % len(shortFrames)
		}
		for range 12 {
			run()
		}
		if got := testing.AllocsPerRun(40, run); got != 0 {
			t.Fatalf("warm signalled VBR int16 encode allocated %g times per call", got)
		}
		if packetSizes[0] == packetSizes[1] && packetSizes[1] == packetSizes[2] && packetSizes[2] == packetSizes[3] {
			t.Fatalf("VBR inputs did not produce varied packet lengths: %v", packetSizes)
		}
	})
}
