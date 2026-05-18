package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/gopus"
)

type engineConfig struct {
	LossPercent     int
	ExpectedLoss    int
	Bitrate         int
	Profile         string
	FEC             bool
	DRED            bool
	DREDDuration    int
	EncoderBlobPath string
	DecoderBlobPath string
	LivePlayback    bool
	RecordWAV       bool
	RecordDir       string
	LossSeed        uint64
	TraceQuality    bool
}

type engineStats struct {
	Running                   bool
	State                     string
	DREDStatus                string
	LossPercent               int
	ExpectedLoss              int
	Bitrate                   int
	DREDDuration              int
	UptimeSeconds             float64
	PacketsSent               uint64
	PacketsDropped            uint64
	PacketsReceived           uint64
	ConcealedFrames           uint64
	SILKPackets               uint64
	HybridPackets             uint64
	CELTPackets               uint64
	FECRecoveryAttempts       uint64
	FECFrames                 uint64
	FECFallbackFrames         uint64
	DREDRecoveryAttempts      uint64
	DREDFrames                uint64
	DREDFallbackFrames        uint64
	LossPathFrames            uint64
	DREDPackets               uint64
	EncodedBytes              uint64
	DeliveredBytes            uint64
	DroppedBytes              uint64
	ReceivedSamples           uint64
	ConcealedSamples          uint64
	EncodeErrors              uint64
	DecodeErrors              uint64
	MicUnderruns              uint64
	LastPacketBytes           int
	LastRMS                   float64
	LastPeak                  float64
	ActualLossPercent         float64
	DREDCoveragePercent       float64
	EncodedKbps               float64
	DeliveredKbps             float64
	DroppedKbps               float64
	ReceivedAudioMS           float64
	ConcealedAudioMS          float64
	TotalAudioMS              float64
	CurrentPacketsPerSecond   float64
	CurrentDropPercent        float64
	CurrentDeliveredKbps      float64
	CurrentConcealMSPerSecond float64
	ResilienceScore           int
	RecoverySummary           string
	ReferenceLagSamples       int
	ReferenceComparedSamples  uint64
	ReferenceRMSE             float64
	ReferenceSNRDB            float64
	ReferenceCorrelation      float64
	LossComparedSamples       uint64
	LossReferenceRMSE         float64
	LossReferenceSNRDB        float64
	LossReferenceCorrelation  float64
	ReferenceIntelligibility  float64
	LossIntelligibility       float64
	LastRecording             string
}

type engine struct {
	mu sync.Mutex

	cfg      engineConfig
	enc      *gopus.Encoder
	dec      *gopus.Decoder
	audio    audioBackend
	track    *webrtc.TrackLocalStaticRTP
	sender   *webrtc.PeerConnection
	receiver *webrtc.PeerConnection

	recorder      *wavRecorder
	dredProbe     *dredPacketProbe
	loadedEncBlob string
	loadedDecBlob string
	lastRecording string
	stats         engineStats
	started       time.Time
	lastRoll      rollingStats
	lossRand      *rand.Rand
	traceDecoded  []float32
	traceLoss     []bool
	closed        bool
	stopCh        chan struct{}
	done          sync.WaitGroup
}

type decodeKind int

const (
	decodeNormal decodeKind = iota
	decodeLossPath
	decodeFEC
	decodeDRED
)

type rollingStats struct {
	at               time.Time
	totalPackets     uint64
	droppedPackets   uint64
	receivedPackets  uint64
	concealedPackets uint64
	deliveredBytes   uint64
	concealedSamples uint64
}

type audioBackend interface {
	readCaptureFrame([]float32) int
	queuePlayback([]float32)
	setLivePlayback(bool)
	close()
}

func defaultEngineConfig(recordDir, encBlob, decBlob string) engineConfig {
	return engineConfig{
		LossPercent:     15,
		ExpectedLoss:    15,
		Bitrate:         48000,
		Profile:         "dred",
		FEC:             false,
		DRED:            dredControlsAvailable(),
		DREDDuration:    80,
		EncoderBlobPath: encBlob,
		DecoderBlobPath: decBlob,
		LivePlayback:    false,
		RecordWAV:       true,
		RecordDir:       recordDir,
		LossSeed:        1,
	}
}

func startEngine(cfg engineConfig) (*engine, error) {
	audio, err := startAudio(audioChannels)
	if err != nil {
		return nil, err
	}
	return startEngineWithAudio(cfg, audio)
}

func startEngineWithAudio(cfg engineConfig, audio audioBackend) (*engine, error) {
	lossSeed := cfg.LossSeed
	if lossSeed == 0 {
		lossSeed = 1
	}
	application := gopus.ApplicationLowDelay
	switch cfg.Profile {
	case "hybrid":
		application = gopus.ApplicationAudio
	case "voice":
		application = gopus.ApplicationVoIP
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  audioSampleRate,
		Channels:    audioChannels,
		Application: application,
	})
	if err != nil {
		audio.close()
		return nil, fmt.Errorf("create encoder: %w", err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(audioSampleRate, audioChannels))
	if err != nil {
		audio.close()
		return nil, fmt.Errorf("create decoder: %w", err)
	}

	e := &engine{
		enc:      enc,
		dec:      dec,
		audio:    audio,
		started:  time.Now(),
		lossRand: rand.New(rand.NewPCG(lossSeed, lossSeed^0x9e3779b97f4a7c15)),
		stopCh:   make(chan struct{}),
		stats: engineStats{
			Running:    true,
			State:      "starting",
			DREDStatus: dredBuildStatus(),
		},
	}
	e.lastRoll.at = e.started
	if err := e.setupWebRTC(); err != nil {
		e.close()
		return nil, err
	}
	if err := e.UpdateConfig(cfg); err != nil {
		e.close()
		return nil, err
	}

	e.done.Add(1)
	go e.sendLoop()
	return e, nil
}

func (e *engine) setupWebRTC() error {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return fmt.Errorf("register WebRTC codecs: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))

	sender, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return fmt.Errorf("create sender peer: %w", err)
	}
	receiver, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		_ = sender.Close()
		return fmt.Errorf("create receiver peer: %w", err)
	}

	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus,
	}, "audio", "gopus-dred-loopback")
	if err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("create RTP track: %w", err)
	}

	rtpSender, err := sender.AddTrack(track)
	if err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("add RTP track: %w", err)
	}
	e.done.Add(1)
	go func() {
		defer e.done.Done()
		buf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	receiver.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if remote.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		e.done.Add(1)
		go e.receiveLoop(remote)
	})
	sender.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		e.setState("sender " + state.String())
	})
	receiver.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		e.setState("receiver " + state.String())
	})

	offer, err := sender.CreateOffer(nil)
	if err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("create offer: %w", err)
	}
	gatherOffer := webrtc.GatheringCompletePromise(sender)
	if err := sender.SetLocalDescription(offer); err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("set sender local description: %w", err)
	}
	<-gatherOffer
	if err := receiver.SetRemoteDescription(*sender.LocalDescription()); err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("set receiver remote description: %w", err)
	}

	answer, err := receiver.CreateAnswer(nil)
	if err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("create answer: %w", err)
	}
	gatherAnswer := webrtc.GatheringCompletePromise(receiver)
	if err := receiver.SetLocalDescription(answer); err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("set receiver local description: %w", err)
	}
	<-gatherAnswer
	if err := sender.SetRemoteDescription(*receiver.LocalDescription()); err != nil {
		_ = sender.Close()
		_ = receiver.Close()
		return fmt.Errorf("set sender remote description: %w", err)
	}

	e.track = track
	e.sender = sender
	e.receiver = receiver
	return nil
}

func (e *engine) UpdateConfig(cfg engineConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if cfg.LossPercent < 0 {
		cfg.LossPercent = 0
	}
	if cfg.LossPercent > 100 {
		cfg.LossPercent = 100
	}
	if cfg.ExpectedLoss < 0 {
		cfg.ExpectedLoss = 0
	}
	if cfg.ExpectedLoss > 100 {
		cfg.ExpectedLoss = 100
	}
	if cfg.Bitrate < 6000 {
		cfg.Bitrate = 6000
	}
	if cfg.Bitrate > 510000 {
		cfg.Bitrate = 510000
	}
	switch cfg.Profile {
	case "dred", "hybrid", "voice":
	default:
		cfg.Profile = "dred"
	}
	if cfg.DREDDuration < 0 {
		cfg.DREDDuration = 0
	}
	if cfg.DREDDuration > 104 {
		cfg.DREDDuration = 104
	}
	if cfg.LossSeed == 0 {
		cfg.LossSeed = 1
	}

	if err := e.enc.SetBitrate(cfg.Bitrate); err != nil {
		return err
	}
	if err := e.enc.SetComplexity(10); err != nil {
		return err
	}
	switch cfg.Profile {
	case "voice":
		if err := e.enc.SetSignal(gopus.SignalVoice); err != nil {
			return err
		}
		if err := e.enc.SetBandwidth(gopus.BandwidthWideband); err != nil {
			return err
		}
		if err := e.enc.SetMaxBandwidth(gopus.BandwidthWideband); err != nil {
			return err
		}
	case "hybrid":
		if err := e.enc.SetSignal(gopus.SignalVoice); err != nil {
			return err
		}
		if err := e.enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
			return err
		}
		if err := e.enc.SetMaxBandwidth(gopus.BandwidthFullband); err != nil {
			return err
		}
	default:
		if err := e.enc.SetSignal(gopus.SignalMusic); err != nil {
			return err
		}
		if err := e.enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
			return err
		}
		if err := e.enc.SetMaxBandwidth(gopus.BandwidthFullband); err != nil {
			return err
		}
	}
	e.enc.SetFEC(cfg.FEC)
	if err := e.enc.SetPacketLoss(cfg.ExpectedLoss); err != nil {
		return err
	}
	e.audio.setLivePlayback(cfg.LivePlayback)

	status := dredBuildStatus()
	if cfg.EncoderBlobPath != "" && cfg.EncoderBlobPath != e.loadedEncBlob {
		data, err := os.ReadFile(cfg.EncoderBlobPath)
		if err != nil {
			status = "encoder DNN load failed: " + err.Error()
		} else if err := e.enc.SetDNNBlob(data); err != nil {
			status = "encoder DNN rejected"
		} else {
			e.loadedEncBlob = cfg.EncoderBlobPath
			status = "encoder DNN loaded"
		}
	}
	if cfg.DecoderBlobPath != "" && cfg.DecoderBlobPath != e.loadedDecBlob {
		data, err := os.ReadFile(cfg.DecoderBlobPath)
		if err != nil {
			status = "decoder DNN load failed: " + err.Error()
		} else if err := e.dec.SetDNNBlob(data); err != nil {
			status = "decoder DNN rejected"
		} else {
			e.loadedDecBlob = cfg.DecoderBlobPath
			if probe, err := newDREDPacketProbe(data); err == nil {
				e.dredProbe = probe
			}
			if status == "encoder DNN loaded" {
				status = "encoder and decoder DNN loaded"
			} else {
				status = "decoder DNN loaded"
			}
		}
	}
	if err := setEncoderDRED(e.enc, cfg.DRED, cfg.DREDDuration); err != nil {
		if cfg.DRED {
			status = err.Error()
		}
	}
	if cfg.DRED && e.loadedEncBlob != "" && e.loadedDecBlob != "" {
		if cfg.ExpectedLoss == 0 {
			status = fmt.Sprintf("DRED armed, expected loss is 0%%, depth=%d", cfg.DREDDuration)
		} else {
			status = fmt.Sprintf("DRED armed, expected loss=%d%%, depth=%d", cfg.ExpectedLoss, cfg.DREDDuration)
		}
	}

	if cfg.RecordWAV && e.recorder == nil {
		rec, err := newWAVRecorder(cfg.RecordDir, audioChannels, audioSampleRate)
		if err != nil {
			status = "recording failed: " + err.Error()
		} else {
			e.recorder = rec
			e.lastRecording = rec.Path()
		}
	} else if !cfg.RecordWAV && e.recorder != nil {
		if err := e.recorder.Close(); err != nil {
			status = "record close failed: " + err.Error()
		}
		e.recorder = nil
	}

	e.cfg = cfg
	e.stats.LossPercent = cfg.LossPercent
	e.stats.ExpectedLoss = cfg.ExpectedLoss
	e.stats.Bitrate = cfg.Bitrate
	e.stats.DREDDuration = cfg.DREDDuration
	e.stats.DREDStatus = status
	e.stats.LastRecording = e.lastRecording
	return nil
}

func (e *engine) sendLoop() {
	defer e.done.Done()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	pcm := make([]float32, frameSamples*audioChannels)
	packet := make([]byte, 4000)
	seq := uint16(rand.Uint32())
	timestamp := rand.Uint32()
	ssrc := rand.Uint32()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
		}

		nRead := e.audio.readCaptureFrame(pcm)
		if nRead < len(pcm) {
			e.bump(func(s *engineStats) { s.MicUnderruns++ })
		}

		e.mu.Lock()
		n, err := e.enc.Encode(pcm, packet)
		lossPercent := e.cfg.LossPercent
		track := e.track
		hasDRED := err == nil && n > 0 && e.dredProbe != nil && e.dredProbe.packetHasDRED(packet[:n], frameSamples)
		var packetMode gopus.Mode
		if err == nil && n > 0 {
			packetMode = gopus.ParseTOC(packet[0]).Mode
		}
		e.mu.Unlock()
		if err != nil {
			e.bump(func(s *engineStats) { s.EncodeErrors++ })
			continue
		}
		if n == 0 {
			continue
		}

		drop := lossPercent > 0 && e.lossRand.IntN(100) < lossPercent
		if drop {
			e.bump(func(s *engineStats) {
				s.PacketsDropped++
				s.EncodedBytes += uint64(n)
				s.DroppedBytes += uint64(n)
				s.LastPacketBytes = n
				if hasDRED {
					s.DREDPackets++
				}
				incrementPacketMode(s, packetMode)
			})
			seq++
			timestamp += frameSamples
			continue
		}

		pkt := rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    111,
				SequenceNumber: seq,
				Timestamp:      timestamp,
				SSRC:           ssrc,
			},
			Payload: packet[:n],
		}
		seq++
		timestamp += frameSamples
		if err := track.WriteRTP(&pkt); err != nil {
			e.bump(func(s *engineStats) { s.EncodeErrors++ })
			continue
		}
		e.bump(func(s *engineStats) {
			s.PacketsSent++
			s.EncodedBytes += uint64(n)
			s.DeliveredBytes += uint64(n)
			s.LastPacketBytes = n
			if hasDRED {
				s.DREDPackets++
			}
			incrementPacketMode(s, packetMode)
		})
	}
}

func (e *engine) receiveLoop(remote *webrtc.TrackRemote) {
	defer e.done.Done()

	pcm := make([]float32, 5760*audioChannels)
	var expected uint16
	haveExpected := false

	for {
		select {
		case <-e.stopCh:
			return
		default:
		}

		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}

		if haveExpected {
			missing := int(pkt.SequenceNumber - expected)
			if missing > 0 && missing < 100 {
				recoverWithFEC := e.fecEnabledFor(pkt.Payload)
				dredAvailable, dredReady := e.prepareDREDRecovery(pkt.Payload, missing*frameSamples)
				for lostAgo := missing; lostAgo >= 1; lostAgo-- {
					if recoverWithFEC && lostAgo == 1 {
						if e.decodeAndOutput(pkt.Payload, pcm, decodeFEC) {
							continue
						}
						e.bump(func(s *engineStats) { s.FECFallbackFrames++ })
					}
					if dredReady && dredAvailable >= lostAgo*frameSamples && e.decodeDREDAndOutput(lostAgo*frameSamples, pcm) {
						continue
					}
					if dredReady {
						e.bump(func(s *engineStats) { s.DREDFallbackFrames++ })
					}
					e.decodeAndOutput(nil, pcm, decodeLossPath)
				}
			}
		}
		expected = pkt.SequenceNumber + 1
		haveExpected = true

		e.decodeAndOutput(pkt.Payload, pcm, decodeNormal)
	}
}

func incrementPacketMode(s *engineStats, mode gopus.Mode) {
	switch mode {
	case gopus.ModeSILK:
		s.SILKPackets++
	case gopus.ModeHybrid:
		s.HybridPackets++
	case gopus.ModeCELT:
		s.CELTPackets++
	}
}

func (e *engine) fecEnabledFor(payload []byte) bool {
	e.mu.Lock()
	enabled := e.cfg.FEC
	e.mu.Unlock()
	if !enabled || len(payload) == 0 {
		return false
	}
	mode := gopus.ParseTOC(payload[0]).Mode
	return mode == gopus.ModeSILK || mode == gopus.ModeHybrid
}

func (e *engine) prepareDREDRecovery(payload []byte, maxDREDSamples int) (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cfg.DRED || e.dredProbe == nil || maxDREDSamples <= 0 {
		return 0, false
	}
	return e.dredProbe.prepareRecovery(payload, maxDREDSamples)
}

func (e *engine) decodeDREDAndOutput(dredOffsetSamples int, pcm []float32) bool {
	e.mu.Lock()
	if e.dredProbe == nil {
		e.mu.Unlock()
		return false
	}
	e.stats.DREDRecoveryAttempts++
	samples, err := e.dredProbe.decodeRecovery(e.dec, dredOffsetSamples, pcm, frameSamples)
	if err != nil {
		e.mu.Unlock()
		return false
	}
	e.writeDecodedLocked(pcm, samples, decodeDRED)
	e.mu.Unlock()
	return true
}

func (e *engine) decodeAndOutput(payload []byte, pcm []float32, kind decodeKind) bool {
	e.mu.Lock()
	var samples int
	var err error
	if kind == decodeFEC {
		e.stats.FECRecoveryAttempts++
		samples, err = e.dec.DecodeWithFEC(payload, pcm, true)
	} else {
		samples, err = e.dec.Decode(payload, pcm)
	}
	if err != nil {
		e.stats.DecodeErrors++
		e.mu.Unlock()
		return false
	}
	e.writeDecodedLocked(pcm, samples, kind)
	e.mu.Unlock()
	return true
}

func (e *engine) writeDecodedLocked(pcm []float32, samples int, kind decodeKind) {
	if samples > 0 {
		out := pcm[:samples*audioChannels]
		var sumSquares float64
		var peak float64
		for _, sample := range out {
			v := math.Abs(float64(sample))
			sumSquares += float64(sample) * float64(sample)
			if v > peak {
				peak = v
			}
		}
		e.stats.LastRMS = math.Sqrt(sumSquares / float64(len(out)))
		e.stats.LastPeak = peak
		if e.cfg.LivePlayback {
			e.audio.queuePlayback(out)
		}
		if e.recorder != nil {
			if err := e.recorder.WriteFloat32(out); err != nil {
				e.stats.DREDStatus = "recording failed: " + err.Error()
			}
		}
		if e.cfg.TraceQuality {
			e.traceDecoded = append(e.traceDecoded, out...)
			lost := kind != decodeNormal
			for range out {
				e.traceLoss = append(e.traceLoss, lost)
			}
		}
	}
	switch kind {
	case decodeLossPath:
		e.stats.ConcealedFrames++
		e.stats.LossPathFrames++
		e.stats.ConcealedSamples += uint64(samples)
	case decodeFEC:
		e.stats.ConcealedFrames++
		e.stats.FECFrames++
		e.stats.ConcealedSamples += uint64(samples)
	case decodeDRED:
		e.stats.ConcealedFrames++
		e.stats.DREDFrames++
		e.stats.ConcealedSamples += uint64(samples)
	default:
		e.stats.PacketsReceived++
		e.stats.ReceivedSamples += uint64(samples)
	}
}

func (e *engine) decodedTraceCopy() ([]float32, []bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	decoded := append([]float32(nil), e.traceDecoded...)
	loss := append([]bool(nil), e.traceLoss...)
	return decoded, loss
}

func (e *engine) Stats() engineStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshStatsLocked(time.Now())
	return e.stats
}

func (e *engine) refreshStatsLocked(now time.Time) {
	if e.started.IsZero() {
		e.started = now
	}
	elapsed := now.Sub(e.started).Seconds()
	if elapsed < 0.001 {
		elapsed = 0.001
	}

	s := &e.stats
	totalPackets := s.PacketsSent + s.PacketsDropped
	totalSamples := s.ReceivedSamples + s.ConcealedSamples

	s.UptimeSeconds = elapsed
	s.ActualLossPercent = percent(float64(s.PacketsDropped), float64(totalPackets))
	s.DREDCoveragePercent = percent(float64(s.DREDPackets), float64(totalPackets))
	s.EncodedKbps = float64(s.EncodedBytes*8) / elapsed / 1000
	s.DeliveredKbps = float64(s.DeliveredBytes*8) / elapsed / 1000
	s.DroppedKbps = float64(s.DroppedBytes*8) / elapsed / 1000
	s.ReceivedAudioMS = samplesToMS(s.ReceivedSamples)
	s.ConcealedAudioMS = samplesToMS(s.ConcealedSamples)
	s.TotalAudioMS = samplesToMS(totalSamples)

	if dt := now.Sub(e.lastRoll.at).Seconds(); dt >= 0.25 {
		prev := e.lastRoll
		current := rollingStats{
			at:               now,
			totalPackets:     totalPackets,
			droppedPackets:   s.PacketsDropped,
			receivedPackets:  s.PacketsReceived,
			concealedPackets: s.ConcealedFrames,
			deliveredBytes:   s.DeliveredBytes,
			concealedSamples: s.ConcealedSamples,
		}
		deltaTotal := current.totalPackets - prev.totalPackets
		deltaDropped := current.droppedPackets - prev.droppedPackets
		deltaDeliveredBytes := current.deliveredBytes - prev.deliveredBytes
		deltaConcealedSamples := current.concealedSamples - prev.concealedSamples

		s.CurrentPacketsPerSecond = ema(s.CurrentPacketsPerSecond, float64(deltaTotal)/dt)
		s.CurrentDropPercent = ema(s.CurrentDropPercent, percent(float64(deltaDropped), float64(deltaTotal)))
		s.CurrentDeliveredKbps = ema(s.CurrentDeliveredKbps, float64(deltaDeliveredBytes*8)/dt/1000)
		s.CurrentConcealMSPerSecond = ema(s.CurrentConcealMSPerSecond, samplesToMS(deltaConcealedSamples)/dt)
		e.lastRoll = current
	}

	s.ResilienceScore, s.RecoverySummary = recoverySummary(*s, totalPackets)
}

func percent(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return 100 * part / total
}

func samplesToMS(samples uint64) float64 {
	return float64(samples) * 1000 / audioSampleRate
}

func ema(old, current float64) float64 {
	if old == 0 {
		return current
	}
	return old*0.65 + current*0.35
}

func recoverySummary(s engineStats, totalPackets uint64) (int, string) {
	if totalPackets == 0 {
		return 0, "waiting for the first packet"
	}
	lossScore := math.Min(40, s.ActualLossPercent*1.2)
	dredScore := 0.0
	if s.DREDFrames > 0 {
		dredScore = math.Min(30, s.DREDCoveragePercent*0.30)
	}
	fecScore := math.Min(15, float64(s.FECFrames)*0.5)
	concealScore := math.Min(15, s.ConcealedAudioMS/120)
	cleanScore := 0.0
	if s.EncodeErrors+s.DecodeErrors == 0 {
		cleanScore = 10
	}
	score := int(math.Round(math.Min(100, lossScore+dredScore+fecScore+concealScore+cleanScore)))
	switch {
	case score >= 90 && s.DREDFrames > 0:
		return score, fmt.Sprintf("%.1f%% loss with %.1f%% DRED coverage and %d DRED frames", s.ActualLossPercent, s.DREDCoveragePercent, s.DREDFrames)
	case s.DREDPackets > 0 && s.DREDFrames == 0:
		return score, "DRED payloads detected; explicit recovery has not fired"
	case score >= 75:
		return score, fmt.Sprintf("%.0f ms/s recovered on the loss path", s.CurrentConcealMSPerSecond)
	case score >= 50:
		return score, "loss recovery is active"
	case s.DREDPackets == 0:
		return score, "DRED payloads not detected yet"
	default:
		return score, "link is clean"
	}
}

func (e *engine) close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.refreshStatsLocked(time.Now())
	e.closed = true
	close(e.stopCh)
	sender := e.sender
	receiver := e.receiver
	audio := e.audio
	rec := e.recorder
	e.recorder = nil
	e.stats.Running = false
	e.stats.State = "stopped"
	e.stats.LastRecording = e.lastRecording
	e.mu.Unlock()

	if sender != nil {
		_ = sender.Close()
	}
	if receiver != nil {
		_ = receiver.Close()
	}
	if audio != nil {
		audio.close()
	}
	if rec != nil {
		_ = rec.Close()
	}
	e.done.Wait()
}

func (e *engine) setState(state string) {
	e.bump(func(s *engineStats) {
		s.State = state
	})
}

func (e *engine) bump(fn func(*engineStats)) {
	e.mu.Lock()
	fn(&e.stats)
	e.mu.Unlock()
}
