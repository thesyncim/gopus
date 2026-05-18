package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

const (
	audioSampleRate = 48000
	audioChannels   = 1
	frameSamples    = 960
)

type audioIO struct {
	ctx      *malgo.AllocatedContext
	device   *malgo.Device
	capture  *sampleRing
	playback *sampleRing

	livePlayback atomic.Bool
}

func startAudio(channels int) (*audioIO, error) {
	if channels < 1 {
		return nil, fmt.Errorf("invalid channel count %d", channels)
	}
	a := &audioIO{
		capture:  newSampleRing(audioSampleRate * channels * 2),
		playback: newSampleRing(audioSampleRate * channels * 2),
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize audio context: %w", err)
	}
	a.ctx = ctx

	cfg := malgo.DefaultDeviceConfig(malgo.Duplex)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = uint32(channels)
	cfg.Playback.Format = malgo.FormatF32
	cfg.Playback.Channels = uint32(channels)
	cfg.SampleRate = audioSampleRate
	cfg.PeriodSizeInFrames = 480
	cfg.Alsa.NoMMap = 1

	callbacks := malgo.DeviceCallbacks{
		Data: func(output, input []byte, _ uint32) {
			if len(input) > 0 {
				a.capture.writeF32LE(input)
			}
			if len(output) == 0 {
				return
			}
			if !a.livePlayback.Load() {
				clear(output)
				return
			}
			n := a.playback.readF32LE(output)
			clear(output[n*4:])
		},
	}

	device, err := malgo.InitDevice(ctx.Context, cfg, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("open audio device: %w", err)
	}
	a.device = device
	if err := device.Start(); err != nil {
		device.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("start audio device: %w", err)
	}
	return a, nil
}

func (a *audioIO) readCaptureFrame(dst []float32) int {
	if a == nil {
		clear(dst)
		return 0
	}
	n := a.capture.read(dst)
	clear(dst[n:])
	return n
}

func (a *audioIO) queuePlayback(pcm []float32) {
	if a == nil || len(pcm) == 0 {
		return
	}
	a.playback.write(pcm)
}

func (a *audioIO) setLivePlayback(enabled bool) {
	if a == nil {
		return
	}
	a.livePlayback.Store(enabled)
	if !enabled {
		a.playback.clear()
	}
}

func (a *audioIO) close() {
	if a == nil {
		return
	}
	if a.device != nil {
		a.device.Uninit()
		a.device = nil
	}
	if a.ctx != nil {
		_ = a.ctx.Uninit()
		a.ctx.Free()
		a.ctx = nil
	}
}

func playPCM(samples []float32, channels, sampleRate int) error {
	if len(samples) == 0 {
		return fmt.Errorf("recording is empty")
	}
	if channels < 1 || sampleRate <= 0 {
		return fmt.Errorf("invalid playback format")
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("initialize playback context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatF32
	cfg.Playback.Channels = uint32(channels)
	cfg.SampleRate = uint32(sampleRate)
	cfg.Alsa.NoMMap = 1

	pos := 0
	callbacks := malgo.DeviceCallbacks{
		Data: func(output, _ []byte, _ uint32) {
			total := len(output) / 4
			for i := 0; i < total; i++ {
				var s float32
				if pos < len(samples) {
					s = samples[pos]
					pos++
				}
				binary.LittleEndian.PutUint32(output[i*4:], math.Float32bits(s))
			}
		},
	}

	device, err := malgo.InitDevice(ctx.Context, cfg, callbacks)
	if err != nil {
		return fmt.Errorf("open playback device: %w", err)
	}
	defer device.Uninit()
	if err := device.Start(); err != nil {
		return fmt.Errorf("start playback device: %w", err)
	}

	duration := float64(len(samples)) / float64(channels*sampleRate)
	time.Sleep(time.Duration((duration + 0.15) * float64(time.Second)))
	return nil
}
