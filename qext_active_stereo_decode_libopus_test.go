//go:build gopus_qext

package gopus

import "testing"

func TestQEXTActiveStereoDecodeMatchesLibopus(t *testing.T) {
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr", true)
	compareQEXTDecodeSequenceWithLibopus(t, 2, packets)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 1920)
	for _, packet := range packets {
		if n, err := dec.Decode(packet, pcm); err != nil || n != 960 {
			t.Fatalf("warm active QEXT decode: samples=%d err=%v", n, err)
		}
	}
	var decoded int
	var decodeErr error
	allocs := testing.AllocsPerRun(20, func() {
		decoded, decodeErr = dec.Decode(packets[0], pcm)
	})
	if decoded != 960 || decodeErr != nil {
		t.Fatalf("measured active QEXT decode: samples=%d err=%v", decoded, decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm active QEXT decode allocs/call=%g, want 0", allocs)
	}
}
