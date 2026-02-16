package main

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrFrameTooLate      = errors.New("frame is late for current mixer cursor")
	ErrFrameTooFarAhead  = errors.New("frame start is beyond mixer lookahead window")
	ErrOutputBufferSmall = errors.New("output buffer is smaller than one mix frame")
	ErrTrackAlreadyAdded = errors.New("track is already registered")
	ErrUnknownTrack      = errors.New("track is not registered")
)

// StreamMixerConfig configures WebRTC-style streaming frame mixing.
type StreamMixerConfig struct {
	Channels          int
	FrameSamples      int
	MaxLookaheadFrame int
	StartSample       int64
}

// TrackConfig controls runtime track behavior.
type TrackConfig struct {
	Gain  float32
	Muted bool
}

// RuntimeTrackMixer defines explicit runtime track lifecycle operations.
type RuntimeTrackMixer interface {
	AddTrack(trackID string, cfg TrackConfig) error
	RemoveTrack(trackID string) bool
	SetTrackGain(trackID string, gain float32) error
	SetTrackMuted(trackID string, muted bool) error
	PushFrame(trackID string, startSample int64, pcm []float32) error
	MixNext(out []float32) (int64, error)
}

// StreamMixer ingests timestamped PCM frames and emits fixed-size mixed frames.
//
// Intended for real-time "tracks arrive at different times" scenarios where
// packetization is out of scope and frames are already decoded to PCM.
// Tracks must be registered explicitly with AddTrack before PushFrame.
type StreamMixer struct {
	mu sync.Mutex

	channels     int
	frameSamples int
	lookahead    int64
	cursor       int64

	tracks map[string]*streamTrack
	stats  StreamMixerStats
}

var _ RuntimeTrackMixer = (*StreamMixer)(nil)

// StreamMixerStats reports frame acceptance behavior.
type StreamMixerStats struct {
	AcceptedFrames int64
	DroppedLate    int64
	DroppedAhead   int64
}

type streamTrack struct {
	gain  float32
	muted bool
	queue []streamSegment
}

type streamSegment struct {
	startSample int64
	pcm         []float32
}

func NewStreamMixer(cfg StreamMixerConfig) (*StreamMixer, error) {
	if cfg.Channels < 1 {
		return nil, fmt.Errorf("channels must be >= 1")
	}
	if cfg.FrameSamples < 1 {
		return nil, fmt.Errorf("frame samples must be >= 1")
	}
	if cfg.MaxLookaheadFrame < 1 {
		cfg.MaxLookaheadFrame = 100
	}

	return &StreamMixer{
		channels:     cfg.Channels,
		frameSamples: cfg.FrameSamples,
		lookahead:    int64(cfg.MaxLookaheadFrame) * int64(cfg.FrameSamples),
		cursor:       cfg.StartSample,
		tracks:       make(map[string]*streamTrack),
	}, nil
}

func (m *StreamMixer) CursorSample() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursor
}

func (m *StreamMixer) Stats() StreamMixerStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

func (m *StreamMixer) AddTrack(trackID string, cfg TrackConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if trackID == "" {
		return fmt.Errorf("track id is required")
	}
	if _, ok := m.tracks[trackID]; ok {
		return ErrTrackAlreadyAdded
	}
	gain := cfg.Gain
	if gain == 0 {
		gain = 1
	}
	m.tracks[trackID] = &streamTrack{
		gain:  gain,
		muted: cfg.Muted,
	}
	return nil
}

func (m *StreamMixer) SetTrackGain(trackID string, gain float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, ok := m.tracks[trackID]
	if !ok {
		return ErrUnknownTrack
	}
	tr.gain = gain
	return nil
}

func (m *StreamMixer) SetTrackMuted(trackID string, muted bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, ok := m.tracks[trackID]
	if !ok {
		return ErrUnknownTrack
	}
	tr.muted = muted
	return nil
}

func (m *StreamMixer) RemoveTrack(trackID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tracks[trackID]; !ok {
		return false
	}
	delete(m.tracks, trackID)
	return true
}

func (m *StreamMixer) PushFrame(trackID string, startSample int64, pcm []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if trackID == "" {
		return fmt.Errorf("track id is required")
	}
	if startSample < 0 {
		return fmt.Errorf("start sample must be >= 0")
	}
	if len(pcm) == 0 {
		return nil
	}
	if len(pcm)%m.channels != 0 {
		return fmt.Errorf("pcm length (%d) is not aligned to %d channels", len(pcm), m.channels)
	}

	frameSamples := int64(len(pcm) / m.channels)
	endSample := startSample + frameSamples
	if endSample <= m.cursor {
		m.stats.DroppedLate++
		return ErrFrameTooLate
	}
	if startSample > m.cursor+m.lookahead {
		m.stats.DroppedAhead++
		return ErrFrameTooFarAhead
	}

	segmentPCM := make([]float32, len(pcm))
	copy(segmentPCM, pcm)
	seg := streamSegment{startSample: startSample, pcm: segmentPCM}

	tr, ok := m.tracks[trackID]
	if !ok {
		return ErrUnknownTrack
	}
	idx := sort.Search(len(tr.queue), func(i int) bool {
		return tr.queue[i].startSample > startSample
	})
	tr.queue = append(tr.queue, streamSegment{})
	copy(tr.queue[idx+1:], tr.queue[idx:])
	tr.queue[idx] = seg

	m.stats.AcceptedFrames++
	return nil
}

func (m *StreamMixer) MixNext(out []float32) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	required := m.frameSamples * m.channels
	if len(out) < required {
		return 0, ErrOutputBufferSmall
	}

	out = out[:required]
	for i := range out {
		out[i] = 0
	}

	windowStart := m.cursor
	windowEnd := m.cursor + int64(m.frameSamples)

	for _, tr := range m.tracks {
		if tr.muted || tr.gain == 0 {
			tr.discardBefore(windowEnd, m.channels)
			continue
		}
		for _, seg := range tr.queue {
			segSamples := int64(len(seg.pcm) / m.channels)
			segStart := seg.startSample
			segEnd := segStart + segSamples

			overlapStart := max64(segStart, windowStart)
			overlapEnd := min64(segEnd, windowEnd)
			if overlapEnd <= overlapStart {
				continue
			}

			srcSampleOffset := int(overlapStart - segStart)
			dstSampleOffset := int(overlapStart - windowStart)
			mixSamples := int(overlapEnd - overlapStart)

			src := seg.pcm[srcSampleOffset*m.channels:]
			dst := out[dstSampleOffset*m.channels:]
			for i := 0; i < mixSamples*m.channels; i++ {
				dst[i] += src[i] * tr.gain
			}
		}
		tr.discardBefore(windowEnd, m.channels)
	}

	startSample := m.cursor
	m.cursor = windowEnd
	return startSample, nil
}

func (t *streamTrack) discardBefore(sample int64, channels int) {
	if len(t.queue) == 0 {
		return
	}
	dst := 0
	for _, seg := range t.queue {
		segSamples := int64(len(seg.pcm) / channels)
		if seg.startSample+segSamples <= sample {
			continue
		}
		if seg.startSample < sample {
			drop := int(sample - seg.startSample)
			seg.pcm = seg.pcm[drop*channels:]
			seg.startSample = sample
		}
		t.queue[dst] = seg
		dst++
	}
	t.queue = t.queue[:dst]
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
