//go:build !gopus_fixed_point

package silk

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestSILKPacket0MidFrameCoreOracle compares the float encodeFrame path with
// the matching libopus silk_encode_frame_FLP state and entropy coder.
func TestSILKPacket0MidFrameCoreOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		bitRate       = 48000
		maxBits       = 1500
		payloadSizeMs = 20
	)
	signal := chirpSweepWB20msStereo48kPacket0Signal(t)
	want, err := probeLibopusSILKPacket0Wrapper(signal, bitRate, maxBits, true, payloadSizeMs, 0)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk stereo packet0 wrapper", err)
	}
	f := prepareSILKPacket0MidFrameCoreOracle(t, signal, bitRate, maxBits, payloadSizeMs, want)
	nBytesOut := f.mid.encodeFrame(f.re, f.condCoding, f.maxBits, f.useCBR)
	f.mid.nFramesEncoded++

	if want.midEncodeRet != 0 {
		t.Fatalf("libopus mid silk_encode_frame_FLP ret=%d", want.midEncodeRet)
	}
	if nBytesOut != want.midNBytesOut {
		t.Fatalf("packet-0 mid silk_encode_frame_FLP nBytesOut=%d want %d", nBytesOut, want.midNBytesOut)
	}
	if gotTell := int32(f.re.Tell()); gotTell != want.midTellAfterFrame {
		t.Fatalf("packet-0 mid silk_encode_frame_FLP tellAfterFrame=%d want %d", gotTell, want.midTellAfterFrame)
	}
	if gotRange := int32(f.re.Range()); gotRange != want.midRangeAfterFrame {
		t.Fatalf("packet-0 mid silk_encode_frame_FLP rangeAfterFrame=%d want %d", gotRange, want.midRangeAfterFrame)
	}
	checkSILKPacket0MidState(t, f.enc, want, false)
}
