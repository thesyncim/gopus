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

type customSignallingLifecycleResult struct {
	pcm        []float32
	finalRange uint32
}

var customSignallingLifecycleHelper libopustest.HelperCache
var customSignalledDecodeAllocationSink uint32

func runCustomSignallingLifecycleOracle(t *testing.T, input []float32) ([]byte, [4]customSignallingLifecycleResult) {
	t.Helper()
	path, err := customSignallingLifecycleHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "custom signalling raw/reset lifecycle",
			OutputBase:  "gopus_libopus_custom_signalling_lifecycle",
			SourceFile:  "libopus_custom_signalling_lifecycle.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom signalling raw/reset lifecycle", err)
		return nil, [4]customSignallingLifecycleResult{}
	}
	var request bytes.Buffer
	request.WriteString("GCLF")
	writeU32(&request, uint32(len(input)))
	for _, sample := range input {
		writef32(&request, sample)
	}
	response, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom signalling lifecycle oracle: %v", err)
	}
	r := bytes.NewReader(response)
	var packetSize int32
	if err := binary.Read(r, binary.LittleEndian, &packetSize); err != nil || packetSize < 2 || packetSize > 200 {
		t.Fatalf("custom signalling lifecycle packet size=%d err=%v", packetSize, err)
	}
	packet := make([]byte, int(packetSize))
	if _, err := r.Read(packet); err != nil {
		t.Fatalf("custom signalling lifecycle packet: %v", err)
	}
	var results [4]customSignallingLifecycleResult
	for i := range results {
		var sampleCount int32
		if err := binary.Read(r, binary.LittleEndian, &sampleCount); err != nil || sampleCount != 960 {
			t.Fatalf("lifecycle result %d sample count=%d err=%v", i, sampleCount, err)
		}
		result := &results[i]
		if err := binary.Read(r, binary.LittleEndian, &result.finalRange); err != nil {
			t.Fatalf("lifecycle result %d range: %v", i, err)
		}
		result.pcm = make([]float32, int(sampleCount))
		for j := range result.pcm {
			var bits uint32
			if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
				t.Fatalf("lifecycle result %d PCM[%d]: %v", i, j, err)
			}
			result.pcm[j] = math.Float32frombits(bits)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom signalling lifecycle oracle has %d trailing bytes", r.Len())
	}
	return packet, results
}

func assertCustomSignallingLifecycleResult(t *testing.T, label string, got []float32, gotRange uint32, want customSignallingLifecycleResult) {
	t.Helper()
	if len(got) != len(want.pcm) || gotRange != want.finalRange {
		t.Fatalf("%s samples/range=%d/%08x want=%d/%08x", label, len(got), gotRange, len(want.pcm), want.finalRange)
	}
	assertCustomDecodeExact(t, label, got, want.pcm)
}

func TestCustomSignallingEndBandSurvivesRawToggleAndReset(t *testing.T) {
	input := make([]float32, 960)
	for i := range input {
		input[i] = float32(.21*math.Sin(2*math.Pi*531*float64(i)/48000) + .07*math.Sin(2*math.Pi*1433*float64(i)/48000))
	}
	packet, oracle := runCustomSignallingLifecycleOracle(t, input)
	lowBandPacket := append([]byte(nil), packet...)
	lowBandPacket[0] = 0xd8
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("raw toggle", func(t *testing.T) {
		dec, err := custom.NewDecoder(mode, 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := dec.DecodeFloat(lowBandPacket, 960)
		if err != nil {
			t.Fatalf("seed DecodeFloat: %v", err)
		}
		assertCustomSignallingLifecycleResult(t, "raw seed", got, dec.FinalRange(), oracle[0])
		if err := dec.SetSignalling(false); err != nil {
			t.Fatal(err)
		}
		got, err = dec.DecodeFloat(packet[1:], 960)
		if err != nil {
			t.Fatalf("raw DecodeFloat: %v", err)
		}
		assertCustomSignallingLifecycleResult(t, "raw payload", got, dec.FinalRange(), oracle[1])
	})

	t.Run("reset", func(t *testing.T) {
		dec, err := custom.NewDecoder(mode, 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := dec.DecodeFloat(lowBandPacket, 960)
		if err != nil {
			t.Fatalf("seed DecodeFloat: %v", err)
		}
		assertCustomSignallingLifecycleResult(t, "reset seed", got, dec.FinalRange(), oracle[2])
		dec.Reset()
		got, err = dec.DecodeFloat(nil, 960)
		if err != nil {
			t.Fatalf("post-reset PLC DecodeFloat: %v", err)
		}
		assertCustomSignallingLifecycleResult(t, "post-reset PLC", got, dec.FinalRange(), oracle[3])
	})
}

func TestCustomSignalledDecodeFloatWarmZeroAllocs(t *testing.T) {
	input := make([]float32, 960)
	for i := range input {
		input[i] = float32(.21*math.Sin(2*math.Pi*531*float64(i)/48000) + .07*math.Sin(2*math.Pi*1433*float64(i)/48000))
	}
	packet, _ := runCustomSignallingLifecycleOracle(t, input)
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.DecodeFloat(packet, 960)
	if err != nil {
		t.Fatalf("C-produced signalled packet: %v", err)
	}
	if len(got) != 960 {
		t.Fatalf("C-produced signalled packet decoded %d samples", len(got))
	}

	var runErr error
	run := func() {
		var samples []float32
		samples, runErr = dec.DecodeFloat(packet, 960)
		if runErr == nil && len(samples) != 0 {
			customSignalledDecodeAllocationSink = math.Float32bits(samples[len(samples)-1]) ^ dec.FinalRange()
		}
	}
	for range 12 {
		run()
		if runErr != nil {
			t.Fatalf("warm DecodeFloat: %v", runErr)
		}
	}
	if got := testing.AllocsPerRun(40, run); got != 0 {
		t.Fatalf("warm signalled DecodeFloat allocated %g times per call", got)
	}
	if runErr != nil {
		t.Fatalf("measured DecodeFloat: %v", runErr)
	}
}
