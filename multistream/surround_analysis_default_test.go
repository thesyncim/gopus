//go:build !gopus_fixed_point

package multistream

import (
	"math"
	"testing"
	"unsafe"
)

func TestEncode_SurroundBandSMRProduced(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(192000)
	const frameSize = 960
	pcm := make([]float32, frameSize*6)
	for i := range frameSize {
		tm := float64(i) / 48000.0
		pcm[i*6+0] = float32(0.8 * math.Sin(2*math.Pi*2100*tm))
		pcm[i*6+1] = float32(0.5 * math.Sin(2*math.Pi*260*tm))
		pcm[i*6+2] = float32(0.7 * math.Sin(2*math.Pi*460*tm))
		pcm[i*6+3] = float32(0.9 * math.Sin(2*math.Pi*1800*tm))
		pcm[i*6+4] = float32(0.6 * math.Sin(2*math.Pi*320*tm))
		pcm[i*6+5] = float32(0.9 * math.Sin(2*math.Pi*50*tm))
	}
	if _, err := encodePacket(enc, pcm, frameSize); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if got, want := len(enc.surroundAnalysis.bandSMR), 6*surroundBands; got < want {
		t.Fatalf("surround band mask len=%d want>=%d", got, want)
	}
	active := 0
	for ch := 0; ch < 5; ch++ {
		row := enc.surroundAnalysis.bandSMR[ch*surroundBands : (ch+1)*surroundBands]
		for _, v := range row {
			if math.Abs(float64(v)) > 1e-6 {
				active++
				break
			}
		}
	}
	if active < 3 {
		t.Fatalf("non-zero surround mask channels=%d want>=3", active)
	}
	for i, v := range enc.surroundAnalysis.bandSMR[5*surroundBands:] {
		if math.Abs(float64(v)) > 1e-9 {
			t.Fatalf("LFE surround mask[%d]=%f want=0", i, v)
		}
	}
}

func TestEncode_SurroundEnergyMaskPerStream(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(192000)
	const frameSize = 960
	pcm := make([]float32, frameSize*6)
	for i := range frameSize {
		tm := float64(i) / 48000.0
		pcm[i*6+0] = float32(0.8 * math.Sin(2*math.Pi*2100*tm))
		pcm[i*6+1] = float32(0.5 * math.Sin(2*math.Pi*260*tm))
		pcm[i*6+2] = float32(0.7 * math.Sin(2*math.Pi*460*tm))
		pcm[i*6+3] = float32(0.9 * math.Sin(2*math.Pi*1800*tm))
		pcm[i*6+4] = float32(0.6 * math.Sin(2*math.Pi*320*tm))
		pcm[i*6+5] = float32(0.9 * math.Sin(2*math.Pi*50*tm))
	}
	if _, err := encodePacket(enc, pcm, frameSize); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	for stream := 0; stream < enc.streams; stream++ {
		got := enc.encoders[stream].CELTEnergyMask()
		if stream == enc.lfeStream {
			if len(got) != 0 {
				t.Fatalf("LFE stream mask len=%d want=0", len(got))
			}
			continue
		}
		c1, c2 := streamSourceChannels(enc.mapping, enc.coupledStreams, stream)
		wantChannels := 1
		if c2 >= 0 {
			wantChannels = 2
		}
		if len(got) != wantChannels*surroundBands {
			t.Fatalf("stream %d mask len=%d want=%d", stream, len(got), wantChannels*surroundBands)
		}
		for i := range surroundBands {
			if got[i] != enc.surroundAnalysis.bandSMR[c1*surroundBands+i] {
				t.Fatalf("stream %d first mask[%d]=%f want=%f", stream, i, got[i], enc.surroundAnalysis.bandSMR[c1*surroundBands+i])
			}
			if c2 >= 0 && got[surroundBands+i] != enc.surroundAnalysis.bandSMR[c2*surroundBands+i] {
				t.Fatalf("stream %d second mask[%d]=%f want=%f", stream, i, got[surroundBands+i], enc.surroundAnalysis.bandSMR[c2*surroundBands+i])
			}
		}
	}
}

func TestStreamEnergyMaskUsesFloat32Storage(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	if len(enc.surroundAnalysis.streamEnergyMask) == 0 {
		t.Fatal("stream mask scratch is empty")
	}
	if got := unsafe.Sizeof(enc.surroundAnalysis.streamEnergyMask[0]); got != 4 {
		t.Fatalf("stream mask element size=%d want celt_glog-sized 4", got)
	}
}
