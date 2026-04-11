package dred

import (
	"reflect"
	"testing"
)

func TestParsePayload(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 8, -4)

	got, err := ParsePayload(payload, 8)
	if err != nil {
		t.Fatalf("ParsePayload error: %v", err)
	}
	if got.Header.Q0 != 6 || got.Header.DQ != 3 || got.Header.QMax != 9 {
		t.Fatalf("ParsePayload header q=(%d,%d,%d) want (6,3,9)", got.Header.Q0, got.Header.DQ, got.Header.QMax)
	}
	if got.Header.DredOffset != -4 || got.Header.DredFrameOffset != 8 {
		t.Fatalf("ParsePayload header offsets=(%d,%d) want (-4,8)", got.Header.DredOffset, got.Header.DredFrameOffset)
	}
	avail := got.Availability(960, 48000)
	if avail.FeatureFrames != 4 || avail.MaxLatents != 1 || avail.OffsetSamples != -480 || avail.EndSamples != 480 || avail.AvailableSamples != 2400 {
		t.Fatalf("ParsePayload availability=%+v want {FeatureFrames:4 MaxLatents:1 OffsetSamples:-480 EndSamples:480 AvailableSamples:2400}", avail)
	}
	quant := make([]int, 4)
	n := got.FillQuantizerLevels(quant, 5760, 48000)
	if n != len(quant) {
		t.Fatalf("FillQuantizerLevels count=%d want %d", n, len(quant))
	}
	want := []int{6, 6, 7, 7}
	if !reflect.DeepEqual(quant, want) {
		t.Fatalf("FillQuantizerLevels=%v want %v", quant, want)
	}
}
