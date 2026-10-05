package libopustest

import (
	"bytes"
	"testing"
)

func TestProbeMultistreamDecodeFreshRejectsOversizedBatch(t *testing.T) {
	if _, err := ProbeMultistreamDecodeFresh(make([]MultistreamDecodeCase, 257)); err == nil {
		t.Fatal("ProbeMultistreamDecodeFresh accepted more than 256 cases")
	}
}

func TestProbeMultistreamDecodeFreshBatchesIndependentCases(t *testing.T) {
	RequireOracle(t)
	mapping := []byte{0, 1}
	formats := []uint32{
		MultistreamDecodeFormatFloat32,
		MultistreamDecodeFormatInt16,
		MultistreamDecodeFormatInt24,
	}
	var cases []MultistreamDecodeCase
	for _, format := range formats {
		base := MultistreamDecodeCase{
			SampleRate: 48000,
			Format:     format,
			Family:     1,
			Channels:   2,
			Streams:    1,
			Coupled:    1,
			FrameSize:  120,
			Mapping:    mapping,
		}
		invalid := base
		invalid.Packet = []byte{0xff}
		cases = append(cases, base, invalid, base)
	}

	results, err := ProbeMultistreamDecodeFresh(cases)
	if err != nil {
		HelperUnavailable(t, "fresh multistream decode", err)
		return
	}
	if len(results) != len(cases) {
		t.Fatalf("results=%d want %d", len(results), len(cases))
	}
	for formatIndex, format := range formats {
		firstIndex := formatIndex * 3
		first := results[firstIndex]
		invalid := results[firstIndex+1]
		repeated := results[firstIndex+2]
		if first.Code <= 0 || repeated.Code != first.Code {
			t.Fatalf("format %d: independent PLC codes=%d,%d want equal positive codes", format, first.Code, repeated.Code)
		}
		if repeated.FinalRange != first.FinalRange {
			t.Fatalf("format %d: independent PLC final ranges=%08x,%08x want equal", format, first.FinalRange, repeated.FinalRange)
		}
		if invalid.Code >= 0 || len(invalid.PCM) != 0 {
			t.Fatalf("format %d: malformed packet code=%d PCM bytes=%d want rejection without PCM", format, invalid.Code, len(invalid.PCM))
		}
		if !bytes.Equal(first.PCM, repeated.PCM) {
			t.Fatalf("format %d: fresh PLC output changed after another record", format)
		}
		itemSize := 4
		if format == MultistreamDecodeFormatInt16 {
			itemSize = 2
		}
		wantBytes := int(first.Code) * 2 * itemSize
		if len(first.PCM) != wantBytes {
			t.Fatalf("format %d PCM bytes=%d want %d", format, len(first.PCM), wantBytes)
		}
		for _, i := range []int{firstIndex, firstIndex + 1, firstIndex + 2} {
			want, err := ProbeDecodeSequence(48000, 2, []DecodeDiffCase{{
				Packet: cases[i].Packet, Format: format, FrameSize: 120,
			}})
			if err != nil {
				HelperUnavailable(t, "fresh single-stream decode", err)
				return
			}
			if len(want) != 1 || results[i].Code != want[0].Code ||
				results[i].FinalRange != want[0].FinalRange || !bytes.Equal(results[i].PCM, want[0].PCM) {
				t.Fatalf("format %d case %d differs from fresh single-stream oracle: got code=%d range=%08x PCM=%d; want %+v",
					format, i, results[i].Code, results[i].FinalRange, len(results[i].PCM), want)
			}
		}
	}
}
