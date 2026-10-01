package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	prevRedundancySampleRate = 48000
	prevRedundancyFrameSize  = 960
)

func encodePrevRedundancyPLCSequence(t *testing.T) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)

	const frameCount = 4
	pcm := make([]int16, prevRedundancyFrameSize*frameCount)
	for frame := 0; frame < frameCount; frame++ {
		for i := 0; i < prevRedundancyFrameSize; i++ {
			time := float64(frame*prevRedundancyFrameSize+i) / prevRedundancySampleRate
			value := 0.25*math.Sin(2*math.Pi*220*time) + 0.08*math.Sin(2*math.Pi*1700*time)
			pcm[frame*prevRedundancyFrameSize+i] = int16(value * 32767)
		}
	}

	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{
			ShortPCM:  pcm[:prevRedundancyFrameSize],
			ForceMode: libopustest.OpusForceModeSILKOnly,
			Bandwidth: libopustest.OpusBandwidthWideband,
		},
		{
			ShortPCM:  pcm[prevRedundancyFrameSize : 2*prevRedundancyFrameSize],
			ForceMode: libopustest.OpusForceModeSILKOnly,
			Bandwidth: libopustest.OpusBandwidthWideband,
		},
		{
			ShortPCM:  pcm[2*prevRedundancyFrameSize : 3*prevRedundancyFrameSize],
			ForceMode: libopustest.OpusForceModeCELTOnly,
			Bandwidth: libopustest.OpusBandwidthFullband,
		},
		{
			ShortPCM:  pcm[3*prevRedundancyFrameSize:],
			ForceMode: libopustest.OpusForceModeCELTOnly,
			Bandwidth: libopustest.OpusBandwidthFullband,
		},
	}
	params := libopustest.OpusEncodeFixedParams{
		SampleRate:    prevRedundancySampleRate,
		Channels:      1,
		Application:   libopustest.OpusApplicationAudio,
		Bitrate:       128000,
		Complexity:    10,
		ForceChannels: 1,
		LSBDepth:      16,
		FrameSize:     prevRedundancyFrameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected-C CELT redundancy sequence", err)
	}
	if len(records) != frameCount {
		t.Fatalf("encoder returned %d records, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("frame %d encoded status=%d packet-bytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	if parseStreamTOC(packets[0][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[1][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[2][0]).mode != streamModeHybrid ||
		parseStreamTOC(packets[3][0]).mode != streamModeCELT {
		t.Fatalf("selected-C mode sequence is %v, want SILK/SILK/Hybrid/CELT",
			[]int{parseStreamTOC(packets[0][0]).mode, parseStreamTOC(packets[1][0]).mode, parseStreamTOC(packets[2][0]).mode, parseStreamTOC(packets[3][0]).mode})
	}
	return packets
}

func assertPreviousRedundancySelectsCELT(t *testing.T, dec *Decoder) {
	t.Helper()
	st, ok := dec.decoders[0].(*streamState)
	if !ok || st.lastMode != streamModeHybrid || !st.prevRedundancy {
		if ok {
			t.Fatalf("redundancy seed state lastMode=%d prevRedundancy=%v, want Hybrid with CELT redundancy", st.lastMode, st.prevRedundancy)
		}
		t.Fatal("single stream decoder state has an unexpected type")
	}
}

func assertPLCConsumedPreviousRedundancy(t *testing.T, dec *Decoder) {
	t.Helper()
	st, ok := dec.decoders[0].(*streamState)
	if !ok || st.lastMode != streamModeCELT || st.prevRedundancy {
		if ok {
			t.Fatalf("post-PLC state lastMode=%d prevRedundancy=%v, want CELT with redundancy consumed", st.lastMode, st.prevRedundancy)
		}
		t.Fatal("single stream decoder state has an unexpected type")
	}
}
