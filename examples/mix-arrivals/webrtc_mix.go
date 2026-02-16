package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type arrivalEvent struct {
	arrivalTick int
	trackID     string
	trackGain   float32
	startSample int64
	pcm         []float32
}

type NetworkSimConfig struct {
	BaseLossProbability      float64
	BurstStartProbability    float64
	BurstContinueProbability float64
	MaxNegativeJitterFrames  int
	MaxPositiveJitterFrames  int
	EnablePLC                bool
	PLCDecay                 float32
	Seed                     uint32
}

type NetworkSimStats struct {
	GeneratedFrames  int64
	DroppedByNetwork int64
	ConcealedFrames  int64
}

func DefaultNetworkSimConfig() NetworkSimConfig {
	return NetworkSimConfig{
		BaseLossProbability:      0.08,
		BurstStartProbability:    0.10,
		BurstContinueProbability: 0.55,
		MaxNegativeJitterFrames:  1,
		MaxPositiveJitterFrames:  2,
		EnablePLC:                true,
		PLCDecay:                 0.92,
		Seed:                     1337,
	}
}

func MixTimedTracksWebRTCStyle(tracks []TimedTrack, mixFrameSamples int) ([]float32, StreamMixerStats, error) {
	endSample := maxEndSample(tracks, channels)
	totalMixFrames := int((endSample + int64(mixFrameSamples) - 1) / int64(mixFrameSamples))
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}

	arrivals, err := buildArrivalSchedule(tracks, mixFrameSamples, totalMixFrames)
	if err != nil {
		return nil, StreamMixerStats{}, err
	}

	mixed, streamStats, err := mixTimedTracksWebRTC(arrivals, tracks, mixFrameSamples, endSample)
	return mixed, streamStats, err
}

func MixTimedTracksWebRTCWithNetwork(tracks []TimedTrack, mixFrameSamples int, network NetworkSimConfig) ([]float32, StreamMixerStats, NetworkSimStats, error) {
	endSample := maxEndSample(tracks, channels)
	totalMixFrames := int((endSample + int64(mixFrameSamples) - 1) / int64(mixFrameSamples))
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}

	arrivals, netStats, err := buildArrivalScheduleWithNetwork(tracks, mixFrameSamples, totalMixFrames, network)
	if err != nil {
		return nil, StreamMixerStats{}, NetworkSimStats{}, err
	}

	mixed, streamStats, err := mixTimedTracksWebRTC(arrivals, tracks, mixFrameSamples, endSample)
	if err != nil {
		return nil, StreamMixerStats{}, netStats, err
	}
	return mixed, streamStats, netStats, nil
}

func mixTimedTracksWebRTC(arrivals []arrivalEvent, tracks []TimedTrack, mixFrameSamples int, endSample int64) ([]float32, StreamMixerStats, error) {
	stats := StreamMixerStats{}
	if mixFrameSamples < 1 {
		return nil, stats, fmt.Errorf("mix frame samples must be >= 1")
	}
	if len(tracks) == 0 {
		return make([]float32, 0), stats, nil
	}

	totalMixFrames := int((endSample + int64(mixFrameSamples) - 1) / int64(mixFrameSamples))
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}

	mixer, err := NewStreamMixer(StreamMixerConfig{
		Channels:          channels,
		FrameSamples:      mixFrameSamples,
		MaxLookaheadFrame: 160, // ~3.2s at 20 ms frames.
		StartSample:       0,
	})
	if err != nil {
		return nil, stats, err
	}

	framePCM := make([]float32, mixFrameSamples*channels)
	mixed := make([]float32, totalMixFrames*mixFrameSamples*channels)
	nextArrival := 0
	trackActive := make(map[string]struct{}, len(tracks))
	const playoutDelayFrames = 2 // absorb network jitter before playout

	for tick := 0; tick < totalMixFrames; tick++ {
		ingestLimit := tick + playoutDelayFrames
		for nextArrival < len(arrivals) && arrivals[nextArrival].arrivalTick <= ingestLimit {
			event := arrivals[nextArrival]
			if _, ok := trackActive[event.trackID]; !ok {
				if err := mixer.AddTrack(event.trackID, TrackConfig{Gain: event.trackGain}); err != nil {
					return nil, stats, fmt.Errorf("add track %s: %w", event.trackID, err)
				}
				trackActive[event.trackID] = struct{}{}
			}

			err := mixer.PushFrame(event.trackID, event.startSample, event.pcm)
			if err != nil && err != ErrFrameTooLate {
				return nil, stats, fmt.Errorf("push frame %s @%d: %w", event.trackID, event.startSample, err)
			}
			nextArrival++
		}

		if _, err := mixer.MixNext(framePCM); err != nil {
			return nil, stats, fmt.Errorf("mix frame %d: %w", tick, err)
		}
		copy(mixed[tick*len(framePCM):], framePCM)
	}

	stats = mixer.Stats()
	return mixed[:int(endSample)*channels], stats, nil
}

func buildArrivalSchedule(tracks []TimedTrack, mixFrameSamples int, totalMixFrames int) ([]arrivalEvent, error) {
	events := make([]arrivalEvent, 0, len(tracks)*8)
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}

	for i := range tracks {
		track := tracks[i]
		if track.StartSample < 0 {
			return nil, fmt.Errorf("track %q has negative start sample", track.Name)
		}
		if len(track.PCM)%channels != 0 {
			return nil, fmt.Errorf("track %q PCM length (%d) is not aligned to %d channels", track.Name, len(track.PCM), channels)
		}

		totalTrackSamples := len(track.PCM) / channels
		for frameIdx, srcSample := 0, 0; srcSample < totalTrackSamples; frameIdx, srcSample = frameIdx+1, srcSample+mixFrameSamples {
			framePCM := copyFrame(track.PCM, srcSample, mixFrameSamples)
			startSample := int64(track.StartSample + srcSample)
			baseTick := int(startSample / int64(mixFrameSamples))
			jitter := deterministicJitterTick(track.Name, frameIdx)
			arrivalTick := clampTick(baseTick+jitter, totalMixFrames)

			events = append(events, arrivalEvent{
				arrivalTick: arrivalTick,
				trackID:     track.Name,
				trackGain:   track.Gain,
				startSample: startSample,
				pcm:         framePCM,
			})
		}
	}

	sortArrivalEvents(events)
	return events, nil
}

func buildArrivalScheduleWithNetwork(tracks []TimedTrack, mixFrameSamples int, totalMixFrames int, cfg NetworkSimConfig) ([]arrivalEvent, NetworkSimStats, error) {
	var stats NetworkSimStats
	events := make([]arrivalEvent, 0, len(tracks)*8)
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}
	cfg = sanitizeNetworkSimConfig(cfg)

	for i := range tracks {
		track := tracks[i]
		if track.StartSample < 0 {
			return nil, stats, fmt.Errorf("track %q has negative start sample", track.Name)
		}
		if len(track.PCM)%channels != 0 {
			return nil, stats, fmt.Errorf("track %q PCM length (%d) is not aligned to %d channels", track.Name, len(track.PCM), channels)
		}

		state := newTrackNetworkState(track.Name, cfg.Seed)
		totalTrackSamples := len(track.PCM) / channels
		for frameIdx, srcSample := 0, 0; srcSample < totalTrackSamples; frameIdx, srcSample = frameIdx+1, srcSample+mixFrameSamples {
			stats.GeneratedFrames++
			framePCM := copyFrame(track.PCM, srcSample, mixFrameSamples)
			startSample := int64(track.StartSample + srcSample)
			baseTick := int(startSample / int64(mixFrameSamples))

			frameLost := shouldDropFrame(&state, cfg)
			if frameLost {
				stats.DroppedByNetwork++
				if cfg.EnablePLC && len(state.lastGood) == len(framePCM) {
					state.concealRun++
					framePCM = concealFromLastGood(state.lastGood, cfg.PLCDecay, state.concealRun)
					stats.ConcealedFrames++
				} else {
					state.concealRun++
					continue
				}
			} else {
				state.concealRun = 0
				state.lastGood = make([]float32, len(framePCM))
				copy(state.lastGood, framePCM)
			}

			jitter := randomJitterTick(&state.rand, cfg.MaxNegativeJitterFrames, cfg.MaxPositiveJitterFrames)
			arrivalTick := clampTick(baseTick+jitter, totalMixFrames)
			events = append(events, arrivalEvent{
				arrivalTick: arrivalTick,
				trackID:     track.Name,
				trackGain:   track.Gain,
				startSample: startSample,
				pcm:         framePCM,
			})
		}
	}

	sortArrivalEvents(events)
	return events, stats, nil
}

type trackNetworkState struct {
	rand       uint32
	burst      bool
	concealRun int
	lastGood   []float32
}

func newTrackNetworkState(trackID string, seed uint32) trackNetworkState {
	if seed == 0 {
		seed = 1
	}
	h := uint32(2166136261)
	for i := 0; i < len(trackID); i++ {
		h ^= uint32(trackID[i])
		h *= 16777619
	}
	h ^= seed
	if h == 0 {
		h = 1
	}
	return trackNetworkState{rand: h}
}

func shouldDropFrame(state *trackNetworkState, cfg NetworkSimConfig) bool {
	if state.burst {
		if randomFloat01(&state.rand) < cfg.BurstContinueProbability {
			return true
		}
		state.burst = false
	}

	if randomFloat01(&state.rand) < cfg.BaseLossProbability {
		return true
	}
	if randomFloat01(&state.rand) < cfg.BurstStartProbability {
		state.burst = true
		return true
	}
	return false
}

func randomJitterTick(state *uint32, maxNegative, maxPositive int) int {
	if maxNegative < 0 {
		maxNegative = 0
	}
	if maxPositive < 0 {
		maxPositive = 0
	}
	span := maxNegative + maxPositive + 1
	if span <= 1 {
		return 0
	}
	return int(nextRand(state)%uint32(span)) - maxNegative
}

func concealFromLastGood(lastGood []float32, decay float32, concealRun int) []float32 {
	if decay <= 0 || decay >= 1 {
		decay = 0.92
	}
	gain := float32(math.Pow(float64(decay), float64(concealRun)))
	if gain < 0.2 {
		gain = 0.2
	}
	out := make([]float32, len(lastGood))
	for i := range lastGood {
		out[i] = lastGood[i] * gain
	}
	return out
}

func sanitizeNetworkSimConfig(cfg NetworkSimConfig) NetworkSimConfig {
	cfg.BaseLossProbability = clampProb(cfg.BaseLossProbability)
	cfg.BurstStartProbability = clampProb(cfg.BurstStartProbability)
	cfg.BurstContinueProbability = clampProb(cfg.BurstContinueProbability)
	if cfg.PLCDecay <= 0 || cfg.PLCDecay >= 1 {
		cfg.PLCDecay = 0.92
	}
	if cfg.Seed == 0 {
		cfg.Seed = 1337
	}
	if cfg.MaxNegativeJitterFrames < 0 {
		cfg.MaxNegativeJitterFrames = 0
	}
	if cfg.MaxPositiveJitterFrames < 0 {
		cfg.MaxPositiveJitterFrames = 0
	}
	return cfg
}

func clampProb(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func copyFrame(pcm []float32, srcSample, frameSamples int) []float32 {
	totalSamples := len(pcm) / channels
	if srcSample >= totalSamples {
		return make([]float32, 0)
	}
	count := frameSamples
	if remain := totalSamples - srcSample; remain < count {
		count = remain
	}
	out := make([]float32, count*channels)
	copy(out, pcm[srcSample*channels:(srcSample+count)*channels])
	return out
}

func sortArrivalEvents(events []arrivalEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].arrivalTick != events[j].arrivalTick {
			return events[i].arrivalTick < events[j].arrivalTick
		}
		if events[i].startSample != events[j].startSample {
			return events[i].startSample < events[j].startSample
		}
		return events[i].trackID < events[j].trackID
	})
}

func deterministicJitterTick(track string, frameIdx int) int {
	seed := uint32(2166136261)
	for i := 0; i < len(track); i++ {
		seed ^= uint32(track[i])
		seed *= 16777619
	}
	seed ^= uint32(frameIdx + 1)
	seed = seed*1664525 + 1013904223
	return int(seed%3) - 1 // -1,0,+1 frame jitter
}

func clampTick(tick, totalMixFrames int) int {
	if tick < 0 {
		return 0
	}
	if tick >= totalMixFrames {
		return totalMixFrames - 1
	}
	return tick
}

func nextRand(state *uint32) uint32 {
	x := *state
	if x == 0 {
		x = 1
	}
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*state = x
	return x
}

func randomFloat01(state *uint32) float64 {
	return float64(nextRand(state)) / float64(^uint32(0))
}

func maxEndSample(tracks []TimedTrack, channels int) int64 {
	var maxEnd int64
	for i := range tracks {
		track := tracks[i]
		samples := int64(len(track.PCM) / channels)
		end := int64(track.StartSample) + samples
		if end > maxEnd {
			maxEnd = end
		}
	}
	return maxEnd
}

func equalPowerPan(pan float64) (float32, float32) {
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	angle := (pan + 1) * math.Pi / 4
	return float32(math.Cos(angle)), float32(math.Sin(angle))
}

func durationToSamples(d time.Duration, rate int) int {
	if d <= 0 || rate <= 0 {
		return 0
	}
	return int((d.Nanoseconds()*int64(rate) + int64(time.Second)/2) / int64(time.Second))
}
