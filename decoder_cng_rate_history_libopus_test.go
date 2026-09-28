package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	cngRateHistoryFrameSize = 960
	cngRateHistorySteps     = 17
)

type cngRateHistoryStep struct {
	packet     []byte
	pcm        []float32
	finalRange uint32
	pitch      int32
}

type cngRateHistoryCase struct {
	direction int
	amplitude int
	steps     []cngRateHistoryStep
}

var (
	cngRateHistoryHelper libopustest.HelperCache
	cngRateHistorySink   uint32
)

func readCNGRateHistoryU32(t *testing.T, r *bytes.Reader, field string) uint32 {
	t.Helper()
	var value uint32
	if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
		t.Fatalf("read CNG rate-history %s: %v", field, err)
	}
	return value
}

func runCNGRateHistoryOracle(t *testing.T) []cngRateHistoryCase {
	t.Helper()
	path, err := cngRateHistoryHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "SILK CNG rate-history transition",
			OutputBase:  "gopus_libopus_decoder_cng_rate_history",
			SourceFile:  "libopus_decoder_cng_rate_history.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"", "include", "src", "silk", "celt"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK CNG rate-history transition", err)
		return nil
	}
	output, err := libopustest.RunHelper(path, nil)
	if err != nil {
		t.Fatalf("CNG rate-history oracle: %v", err)
	}
	r := bytes.NewReader(output)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "GCRH" {
		t.Fatalf("CNG rate-history oracle magic=%q err=%v", magic, err)
	}
	caseCount := readCNGRateHistoryU32(t, r, "case count")
	if caseCount != 8 {
		t.Fatalf("CNG rate-history oracle cases=%d, want 8", caseCount)
	}
	cases := make([]cngRateHistoryCase, int(caseCount))
	pcmBytes := make([]byte, cngRateHistoryFrameSize*4)
	for caseIndex := range cases {
		tc := &cases[caseIndex]
		tc.direction = int(readCNGRateHistoryU32(t, r, "direction"))
		tc.amplitude = int(readCNGRateHistoryU32(t, r, "amplitude"))
		stepCount := readCNGRateHistoryU32(t, r, "step count")
		if tc.direction > 1 || tc.amplitude > 3 || stepCount != cngRateHistorySteps {
			t.Fatalf("CNG sequence %d metadata=%d/%d/%d", caseIndex, tc.direction, tc.amplitude, stepCount)
		}
		tc.steps = make([]cngRateHistoryStep, int(stepCount))
		for stepIndex := range tc.steps {
			step := &tc.steps[stepIndex]
			packetSize := readCNGRateHistoryU32(t, r, "packet size")
			samples := readCNGRateHistoryU32(t, r, "decoded sample count")
			step.finalRange = readCNGRateHistoryU32(t, r, "final range")
			step.pitch = int32(readCNGRateHistoryU32(t, r, "pitch"))
			if packetSize > 1000 || samples != cngRateHistoryFrameSize {
				t.Fatalf("CNG sequence %d step %d packet/sample counts=%d/%d", caseIndex, stepIndex, packetSize, samples)
			}
			step.packet = make([]byte, int(packetSize))
			if _, err := io.ReadFull(r, step.packet); err != nil {
				t.Fatalf("CNG sequence %d step %d packet: %v", caseIndex, stepIndex, err)
			}
			if _, err := io.ReadFull(r, pcmBytes); err != nil {
				t.Fatalf("CNG sequence %d step %d PCM: %v", caseIndex, stepIndex, err)
			}
			step.pcm = make([]float32, cngRateHistoryFrameSize)
			for sample := range step.pcm {
				step.pcm[sample] = math.Float32frombits(binary.LittleEndian.Uint32(pcmBytes[sample*4:]))
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("CNG rate-history oracle has %d trailing bytes", r.Len())
	}
	return cases
}

func assertCNGRateHistorySequence(t *testing.T, dec *Decoder, tc cngRateHistoryCase, pcm []float32) {
	t.Helper()
	dec.Reset()
	firstBandwidth, secondBandwidth := BandwidthWideband, BandwidthNarrowband
	if tc.direction == 1 {
		firstBandwidth, secondBandwidth = secondBandwidth, firstBandwidth
	}
	for stepIndex, step := range tc.steps {
		if stepIndex < 7 {
			if len(step.packet) == 0 {
				t.Fatalf("sequence %s step %d has no C-encoded packet", cngRateHistoryCaseName(tc), stepIndex)
			}
			wantBandwidth := firstBandwidth
			if stepIndex == 6 {
				wantBandwidth = secondBandwidth
			}
			info := ParseTOC(step.packet[0])
			if info.Mode != ModeSILK || info.Bandwidth != wantBandwidth {
				t.Fatalf("sequence %s step %d C packet mode/bandwidth=%v/%v, want SILK/%v", cngRateHistoryCaseName(tc), stepIndex, info.Mode, info.Bandwidth, wantBandwidth)
			}
		} else if len(step.packet) != 0 {
			t.Fatalf("sequence %s loss step %d contains a packet", cngRateHistoryCaseName(tc), stepIndex)
		}

		n, err := dec.Decode(step.packet, pcm)
		if err != nil || n != cngRateHistoryFrameSize {
			t.Fatalf("sequence %s step %d Decode=(%d,%v), want (%d,nil)", cngRateHistoryCaseName(tc), stepIndex, n, err, cngRateHistoryFrameSize)
		}
		if got := dec.FinalRange(); got != step.finalRange {
			t.Fatalf("sequence %s step %d range=%08x, want %08x", cngRateHistoryCaseName(tc), stepIndex, got, step.finalRange)
		}
		if got := int32(dec.Pitch()); got != step.pitch {
			t.Fatalf("sequence %s step %d pitch=%d, want %d", cngRateHistoryCaseName(tc), stepIndex, got, step.pitch)
		}
		for sample := range step.pcm {
			if got, want := math.Float32bits(pcm[sample]), math.Float32bits(step.pcm[sample]); got != want {
				t.Fatalf("sequence %s step %d PCM[%d]=%08x, want %08x", cngRateHistoryCaseName(tc), stepIndex, sample, got, want)
			}
		}
	}
}

func cngRateHistoryCaseName(tc cngRateHistoryCase) string {
	direction := "wideband-to-narrowband"
	if tc.direction == 1 {
		direction = "narrowband-to-wideband"
	}
	return fmt.Sprintf("%s-amplitude-%d", direction, tc.amplitude)
}

func TestSILKCNGRateChangeRetainsExcitationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := runCNGRateHistoryOracle(t)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, cngRateHistoryFrameSize)
	for _, tc := range cases {
		t.Run(cngRateHistoryCaseName(tc), func(t *testing.T) {
			assertCNGRateHistorySequence(t, dec, tc, pcm)
		})
	}
}

func TestSILKCNGRateChangeWarmZeroAllocs(t *testing.T) {
	cases := runCNGRateHistoryOracle(t)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, cngRateHistoryFrameSize)
	var runErr error
	run := func() {
		for _, tc := range cases {
			dec.Reset()
			for stepIndex, step := range tc.steps {
				n, err := dec.Decode(step.packet, pcm)
				if err != nil || n != cngRateHistoryFrameSize {
					runErr = fmt.Errorf("sequence %s step %d Decode=(%d,%v)", cngRateHistoryCaseName(tc), stepIndex, n, err)
					return
				}
				cngRateHistorySink ^= math.Float32bits(pcm[(stepIndex*47)%len(pcm)]) ^ dec.FinalRange()
			}
		}
	}
	for range 3 {
		run()
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if got := testing.AllocsPerRun(10, run); got != 0 {
		t.Fatalf("warm SILK CNG rate-change decode allocated %g times per sequence batch", got)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
}
