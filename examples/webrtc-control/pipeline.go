package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/thesyncim/gopus"
)

// pipeline manages the audio encode/decode loop for a single WebRTC session.
type pipeline struct {
	mu sync.Mutex

	enc *gopus.Encoder
	dec *gopus.Decoder

	gen *signalGenerator

	track       *webrtc.TrackLocalStaticSample
	dataChannel *webrtc.DataChannel

	// Current encoder params (protected by mu).
	channels    int
	frameSize   int
	application gopus.Application
	simLoss     int // simulated packet loss 0-100%

	// Loopback mode: decoded PCM from remote track is sent here.
	loopbackCh chan []float32
	loopback   bool

	// Stats from last encoded packet.
	lastPacketSize int
	lastTOC        gopus.TOC
	packetCount    uint64 // total packets sent since start

	stopCh chan struct{}
}

func newPipeline(track *webrtc.TrackLocalStaticSample) (*pipeline, error) {
	channels := 2
	frameSize := 960
	app := gopus.ApplicationAudio

	enc, err := gopus.NewEncoder(sampleRate, channels, app)
	if err != nil {
		return nil, fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		return nil, err
	}
	if err := enc.SetComplexity(10); err != nil {
		return nil, err
	}

	decCfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(decCfg)
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}

	return &pipeline{
		enc:         enc,
		dec:         dec,
		gen:         newSignalGenerator("chord", channels),
		track:       track,
		channels:    channels,
		frameSize:   frameSize,
		application: app,
		loopbackCh:  make(chan []float32, 50),
		stopCh:      make(chan struct{}),
	}, nil
}

// setDataChannel wires the browser-created DataChannel into the pipeline.
func (p *pipeline) setDataChannel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dataChannel = dc
}

// start launches the encode loop and stats pusher goroutines.
func (p *pipeline) start() {
	go p.encodeLoop()
	go p.statsPusher()
}

// stop signals all goroutines to exit. Safe to call multiple times.
func (p *pipeline) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.stopCh:
		// Already closed.
	default:
		close(p.stopCh)
	}
}

func (p *pipeline) encodeLoop() {
	p.mu.Lock()
	frameSize := p.frameSize
	channels := p.channels
	p.mu.Unlock()

	frameDuration := time.Duration(float64(frameSize) / float64(sampleRate) * float64(time.Second))
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	pcm := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)
	frameNum := 0

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
		}
		frameNum++

		p.mu.Lock()
		curFrameSize := p.frameSize
		curChannels := p.channels
		curSimLoss := p.simLoss
		isLoopback := p.loopback
		p.mu.Unlock()

		// Handle frame size or channel changes.
		if curFrameSize != frameSize || curChannels != channels {
			frameSize = curFrameSize
			channels = curChannels
			pcm = make([]float32, frameSize*channels)
			frameDuration = time.Duration(float64(frameSize) / float64(sampleRate) * float64(time.Second))
			ticker.Reset(frameDuration)
		}

		if isLoopback {
			select {
			case incoming := <-p.loopbackCh:
				// Use incoming PCM (may need to resize).
				if len(incoming) >= frameSize*channels {
					copy(pcm, incoming[:frameSize*channels])
				} else {
					copy(pcm, incoming)
					for i := len(incoming); i < len(pcm); i++ {
						pcm[i] = 0
					}
				}
			default:
				// No data available, send silence.
				for i := range pcm {
					pcm[i] = 0
				}
			}
		} else {
			p.mu.Lock()
			p.gen.fillFrame(pcm, frameSize)
			p.mu.Unlock()
		}

		// Log PCM peak for first few frames to verify signal generation.
		if frameNum <= 3 {
			var peak float32
			for _, s := range pcm {
				if s > peak {
					peak = s
				}
				if -s > peak {
					peak = -s
				}
			}
			log.Printf("[frame %d] PCM samples=%d peak=%.4f", frameNum, len(pcm), peak)
		}

		p.mu.Lock()
		n, err := p.enc.Encode(pcm, packet)
		p.mu.Unlock()
		if err != nil {
			log.Printf("encode error: %v", err)
			continue
		}
		if n == 0 {
			// Internal buffering (lookahead not yet filled)
			continue
		}

		// Self-decode check on first few frames.
		if frameNum <= 3 {
			decBuf := make([]float32, frameSize*channels)
			p.mu.Lock()
			samples, decErr := p.dec.Decode(packet[:n], decBuf)
			p.mu.Unlock()
			if decErr != nil {
				log.Printf("[frame %d] SELF-DECODE FAILED: %v (pkt %d bytes, TOC=0x%02x)", frameNum, decErr, n, packet[0])
			} else {
				var decPeak float32
				for _, s := range decBuf[:samples*channels] {
					if s > decPeak {
						decPeak = s
					}
					if -s > decPeak {
						decPeak = -s
					}
				}
				log.Printf("[frame %d] encode=%d bytes, self-decode OK: %d samples, peak=%.4f, TOC=0x%02x", frameNum, n, samples, decPeak, packet[0])
			}
		}

		// Simulated packet loss: drop this packet randomly.
		if curSimLoss > 0 && rand.IntN(100) < curSimLoss {
			continue
		}

		// Parse TOC for stats.
		toc := gopus.ParseTOC(packet[0])
		p.mu.Lock()
		p.lastPacketSize = n
		p.lastTOC = toc
		p.packetCount++
		p.mu.Unlock()

		dur := time.Duration(float64(frameSize) / float64(sampleRate) * float64(time.Second))
		if err := p.track.WriteSample(media.Sample{
			Data:     packet[:n],
			Duration: dur,
		}); err != nil {
			log.Printf("write sample error: %v", err)
		}
	}
}

// handleIncomingTrack reads RTP from a remote audio track, decodes Opus, and
// pushes PCM into the loopback channel.
func (p *pipeline) handleIncomingTrack(remote *webrtc.TrackRemote) {
	p.mu.Lock()
	p.loopback = true
	channels := p.channels
	p.mu.Unlock()

	pcm := make([]float32, 5760*channels)

	for {
		select {
		case <-p.stopCh:
			return
		default:
		}

		// ReadRTP returns a parsed RTP packet; .Payload is the Opus frame.
		rtpPkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		payload := rtpPkt.Payload
		if len(payload) == 0 {
			continue
		}

		p.mu.Lock()
		samples, err := p.dec.Decode(payload, pcm)
		p.mu.Unlock()
		if err != nil {
			log.Printf("decode error: %v", err)
			continue
		}
		if samples == 0 {
			continue
		}

		// Non-blocking send to loopback channel.
		frame := make([]float32, samples*channels)
		copy(frame, pcm[:samples*channels])
		select {
		case p.loopbackCh <- frame:
		default:
			// Drop if channel is full.
		}
	}
}

func (p *pipeline) statsPusher() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
		}

		if p.dataChannel == nil || p.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
			continue
		}

		p.mu.Lock()
		stats := map[string]any{
			"type":             "stats",
			"packetSize":       p.lastPacketSize,
			"packetCount":      p.packetCount,
			"frameSize":        p.frameSize,
			"channels":         p.channels,
			"bitrate":          p.enc.Bitrate(),
			"complexity":       p.enc.Complexity(),
			"fec":              p.enc.FECEnabled(),
			"dtx":              p.enc.DTXEnabled(),
			"packetLoss":       p.enc.PacketLoss(),
			"simLoss":          p.simLoss,
			"lsbDepth":         p.enc.LSBDepth(),
			"predDisabled":     p.enc.PredictionDisabled(),
			"phaseInvDisabled": p.enc.PhaseInversionDisabled(),
			"forceChannels":    p.enc.ForceChannels(),
			"loopback":         p.loopback,
		}

		toc := p.lastTOC
		p.mu.Unlock()

		var modeName string
		switch toc.Mode {
		case gopus.ModeSILK:
			modeName = "SILK"
		case gopus.ModeHybrid:
			modeName = "Hybrid"
		case gopus.ModeCELT:
			modeName = "CELT"
		}
		stats["lastMode"] = modeName

		var bwName string
		switch toc.Bandwidth {
		case gopus.BandwidthNarrowband:
			bwName = "NB"
		case gopus.BandwidthMediumband:
			bwName = "MB"
		case gopus.BandwidthWideband:
			bwName = "WB"
		case gopus.BandwidthSuperwideband:
			bwName = "SWB"
		case gopus.BandwidthFullband:
			bwName = "FB"
		}
		stats["lastBandwidth"] = bwName
		stats["tocStereo"] = toc.Stereo
		stats["tocConfig"] = toc.Config

		// Compute approximate bitrate from last packet.
		if p.lastPacketSize > 0 && p.frameSize > 0 {
			bitrateKbps := float64(p.lastPacketSize*8) / (float64(p.frameSize) / float64(sampleRate)) / 1000.0
			stats["bitrateKbps"] = bitrateKbps
		}

		data, _ := json.Marshal(stats)
		if err := p.dataChannel.SendText(string(data)); err != nil {
			log.Printf("send stats error: %v", err)
		}
	}
}

type controlMessage struct {
	Type  string `json:"type"`
	Param string `json:"param"`
	Value any    `json:"value"`
}

// handleControlMessage processes a JSON control message from the browser.
func (p *pipeline) handleControlMessage(data []byte) {
	var msg controlMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("bad control message: %v", err)
		return
	}
	if msg.Type != "set_param" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Helper to get numeric value.
	numVal := func() int {
		switch v := msg.Value.(type) {
		case float64:
			return int(v)
		case int:
			return v
		default:
			return 0
		}
	}
	boolVal := func() bool {
		switch v := msg.Value.(type) {
		case bool:
			return v
		case float64:
			return v != 0
		default:
			return false
		}
	}
	strVal := func() string {
		s, _ := msg.Value.(string)
		return s
	}

	switch msg.Param {
	case "application":
		var app gopus.Application
		switch strVal() {
		case "voip":
			app = gopus.ApplicationVoIP
		case "audio":
			app = gopus.ApplicationAudio
		case "lowdelay":
			app = gopus.ApplicationLowDelay
		default:
			return
		}
		// Application can only be changed before first encode in gopus,
		// so we recreate the encoder.
		newEnc, err := gopus.NewEncoder(sampleRate, p.channels, app)
		if err != nil {
			log.Printf("recreate encoder: %v", err)
			return
		}
		// Copy over current settings.
		_ = newEnc.SetBitrate(p.enc.Bitrate())
		_ = newEnc.SetComplexity(p.enc.Complexity())
		_ = newEnc.SetFrameSize(p.frameSize)
		newEnc.SetFEC(p.enc.FECEnabled())
		_ = newEnc.SetPacketLoss(p.enc.PacketLoss())
		newEnc.SetDTX(p.enc.DTXEnabled())
		_ = newEnc.SetLSBDepth(p.enc.LSBDepth())
		newEnc.SetPredictionDisabled(p.enc.PredictionDisabled())
		newEnc.SetPhaseInversionDisabled(p.enc.PhaseInversionDisabled())
		_ = newEnc.SetForceChannels(p.enc.ForceChannels())
		bm := p.enc.BitrateMode()
		_ = newEnc.SetBitrateMode(bm)
		p.enc = newEnc
		p.application = app

	case "bitrate":
		_ = p.enc.SetBitrate(numVal())

	case "complexity":
		_ = p.enc.SetComplexity(numVal())

	case "frameSize":
		fs := numVal()
		if err := p.enc.SetFrameSize(fs); err == nil {
			p.frameSize = fs
		}

	case "bitrateMode":
		switch strVal() {
		case "vbr":
			_ = p.enc.SetBitrateMode(gopus.BitrateModeVBR)
		case "cvbr":
			_ = p.enc.SetBitrateMode(gopus.BitrateModeCVBR)
		case "cbr":
			_ = p.enc.SetBitrateMode(gopus.BitrateModeCBR)
		}

	case "fec":
		p.enc.SetFEC(boolVal())

	case "packetLoss":
		_ = p.enc.SetPacketLoss(numVal())

	case "dtx":
		p.enc.SetDTX(boolVal())

	case "signal":
		switch strVal() {
		case "auto":
			_ = p.enc.SetSignal(gopus.SignalAuto)
		case "voice":
			_ = p.enc.SetSignal(gopus.SignalVoice)
		case "music":
			_ = p.enc.SetSignal(gopus.SignalMusic)
		}

	case "maxBandwidth":
		switch strVal() {
		case "nb":
			_ = p.enc.SetMaxBandwidth(gopus.BandwidthNarrowband)
		case "mb":
			_ = p.enc.SetMaxBandwidth(gopus.BandwidthMediumband)
		case "wb":
			_ = p.enc.SetMaxBandwidth(gopus.BandwidthWideband)
		case "swb":
			_ = p.enc.SetMaxBandwidth(gopus.BandwidthSuperwideband)
		case "fb":
			_ = p.enc.SetMaxBandwidth(gopus.BandwidthFullband)
		}

	case "forceChannels":
		_ = p.enc.SetForceChannels(numVal())

	case "lsbDepth":
		_ = p.enc.SetLSBDepth(numVal())

	case "predictionDisabled":
		p.enc.SetPredictionDisabled(boolVal())

	case "phaseInvDisabled":
		p.enc.SetPhaseInversionDisabled(boolVal())

	case "simLoss":
		v := numVal()
		if v < 0 {
			v = 0
		}
		if v > 50 {
			v = 50
		}
		p.simLoss = v

	case "audioSource":
		s := strVal()
		if s == "loopback" {
			p.loopback = true
		} else {
			p.loopback = false
			p.gen.setSignal(s)
		}
	}
}
