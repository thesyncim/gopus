package main

import (
	"reflect"
	"testing"
)

func TestRepeatedInputFramesMatchOpusDemoFraming(t *testing.T) {
	tests := []struct {
		name      string
		pcm       []float32
		channels  int
		frameSize int
		batch     int
		want      [][]int32
	}{
		{
			name:      "continuous batch boundary and partial EOF",
			pcm:       []float32{0.125, -0.125, 0.25, -0.25, 0.5, -0.5},
			channels:  2,
			frameSize: 4,
			batch:     2,
			want: [][]int32{
				{1048576, -1048576, 2097152, -2097152, 4194304, -4194304, 1048576, -1048576},
				{2097152, -2097152, 4194304, -4194304, 0, 0, 0, 0},
			},
		},
		{
			name:      "exact multiple receives zero EOF frame",
			pcm:       []float32{0.125, -0.125, 0.25, -0.25, 0.5, -0.5, 0.75, -0.75},
			channels:  2,
			frameSize: 4,
			batch:     1,
			want: [][]int32{
				{1048576, -1048576, 2097152, -2097152, 4194304, -4194304, 6291456, -6291456},
				{0, 0, 0, 0, 0, 0, 0, 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputSamples := len(tt.pcm) / tt.channels
			frameCount, totalSamples, err := repeatedInputFrameCount(inputSamples, tt.frameSize, tt.batch)
			if err != nil {
				t.Fatalf("repeatedInputFrameCount: %v", err)
			}
			if frameCount != len(tt.want) || totalSamples != inputSamples*tt.batch {
				t.Fatalf("frame count/total samples = %d/%d, want %d/%d", frameCount, totalSamples, len(tt.want), inputSamples*tt.batch)
			}
			for frameIndex, want := range tt.want {
				got := make([]int32, tt.frameSize*tt.channels)
				if err := fillRepeatedInt24Frame(tt.pcm, tt.channels, tt.frameSize, totalSamples, frameIndex, got); err != nil {
					t.Fatalf("fill frame %d: %v", frameIndex, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("frame %d = %v, want %v", frameIndex, got, want)
				}
			}
		})
	}
}

func TestEncodeGopusOnceRejectsInvalidControls(t *testing.T) {
	tests := []struct {
		name                string
		bitrate, complexity int
		frameSize, batch    int
	}{
		{name: "bitrate", bitrate: 0, complexity: 10, frameSize: 960, batch: 1},
		{name: "complexity", bitrate: 64000, complexity: 11, frameSize: 960, batch: 1},
		{name: "zero frame size", bitrate: 64000, complexity: 10, frameSize: 0, batch: 1},
		{name: "unsupported frame size", bitrate: 64000, complexity: 10, frameSize: 600, batch: 1},
		{name: "batch", bitrate: 64000, complexity: 10, frameSize: 960, batch: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := encodeGopusOnce([]float32{0.25}, 1, tt.bitrate, tt.complexity, tt.frameSize, tt.batch)
			if err == nil {
				t.Fatal("encodeGopusOnce accepted invalid settings")
			}
		})
	}
}
