package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/thesyncim/gopus"
)

const (
	minOpusRTPPacketSamples = 120
	maxOpusRTPPacketSamples = 5760
	maxIncomingGapSamples   = sampleRate
	maxIncomingGapPackets   = 100
)

// pipeline manages the audio encode/decode loop for a single WebRTC session.
type pipeline struct {
	mu sync.Mutex

	enc           *gopus.Encoder
	dec           *gopus.Decoder
	decoderConfig gopus.DecoderConfig

	gen *signalGenerator

	track       *webrtc.TrackLocalStaticSample
	dataChannel *webrtc.DataChannel

	// Current encoder params (protected by mu).
	channels    int
	frameSize   int
	application gopus.Application
	simLoss     int // simulated packet loss 0-50%

	// Loopback mode: decoded PCM from remote track is sent here.
	loopbackCh         chan []float32
	loopback           bool
	loopbackGeneration uint64

	// Stats from last encoded packet.
	lastPacketSize int
	lastTOC        gopus.TOC
	packetCount    uint64 // total packets sent since start

	stopCh chan struct{}
}

// pcmFIFO preserves decoded samples when input packet sizes and encoder frame
// sizes differ. The caller drains at most one decoded packet beyond each output
// frame, so its storage is bounded by one output frame plus one Opus packet.
type pcmFIFO struct {
	samples []float32
	offset  int
}

func (q *pcmFIFO) available() int {
	return len(q.samples) - q.offset
}

func (q *pcmFIFO) append(samples []float32) {
	if q.offset > 0 {
		q.samples = append(q.samples[:0], q.samples[q.offset:]...)
		q.offset = 0
	}
	q.samples = append(q.samples, samples...)
}

func (q *pcmFIFO) readFrame(dst []float32) bool {
	if q.available() < len(dst) {
		return false
	}
	copy(dst, q.samples[q.offset:q.offset+len(dst)])
	q.offset += len(dst)
	if q.offset == len(q.samples) {
		q.samples = q.samples[:0]
		q.offset = 0
	}
	return true
}

func (q *pcmFIFO) reset() {
	q.samples = q.samples[:0]
	q.offset = 0
}

type incomingRTPReader interface {
	ReadRTP() (*rtp.Packet, interceptor.Attributes, error)
}

type incomingRTPState struct {
	expectedSequence  uint16
	expectedTimestamp uint32
	initialized       bool
}

func drainLoopbackQueue(ch <-chan []float32) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func newPipeline(track *webrtc.TrackLocalStaticSample) (*pipeline, error) {
	channels := 2
	frameSize := 960
	app := gopus.ApplicationAudio

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: app})
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
		enc:           enc,
		dec:           dec,
		decoderConfig: decCfg,
		gen:           newSignalGenerator("chord", channels),
		track:         track,
		channels:      channels,
		frameSize:     frameSize,
		application:   app,
		loopbackCh:    make(chan []float32, 50),
		stopCh:        make(chan struct{}),
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
	var loopbackPCM pcmFIFO
	var loopbackGeneration uint64

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
		curLoopbackGeneration := p.loopbackGeneration
		p.mu.Unlock()
		if curLoopbackGeneration != loopbackGeneration {
			loopbackPCM.reset()
			loopbackGeneration = curLoopbackGeneration
		}

		// Handle frame size or channel changes.
		if curFrameSize != frameSize || curChannels != channels {
			frameSize = curFrameSize
			channels = curChannels
			pcm = make([]float32, frameSize*channels)
			frameDuration = time.Duration(float64(frameSize) / float64(sampleRate) * float64(time.Second))
			ticker.Reset(frameDuration)
		}

		if isLoopback {
			frameSamples := frameSize * channels
		gatherLoopback:
			for loopbackPCM.available() < frameSamples {
				select {
				case incoming := <-p.loopbackCh:
					loopbackPCM.append(incoming)
				default:
					break gatherLoopback
				}
			}
			if !loopbackPCM.readFrame(pcm) {
				// Keep partial input for the next frame and send silence meanwhile.
				clear(pcm)
			}
		} else {
			loopbackPCM.reset()
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

		dur := time.Duration(float64(frameSize) / float64(sampleRate) * float64(time.Second))
		// Advance RTP sequence and timestamp for a simulated drop without sending payload.
		if curSimLoss > 0 && rand.IntN(100) < curSimLoss {
			if err := p.track.WriteSample(media.Sample{Duration: dur, PrevDroppedPackets: 1}); err != nil {
				log.Printf("advance dropped sample: %v", err)
			}
			continue
		}

		// Parse TOC for stats.
		toc := gopus.ParseTOC(packet[0])
		p.mu.Lock()
		p.lastPacketSize = n
		p.lastTOC = toc
		p.packetCount++
		p.mu.Unlock()

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
	codec := remote.Codec()
	if codec.ClockRate != sampleRate || codec.Channels > 2 {
		log.Printf("unsupported incoming Opus format: clock rate=%d channels=%d", codec.ClockRate, codec.Channels)
		return
	}
	p.handleIncomingRTP(remote, negotiatedInbandFEC(codec.SDPFmtpLine))
}

func (p *pipeline) handleIncomingRTP(remote incomingRTPReader, fecEnabled bool) {
	p.mu.Lock()
	channels := p.channels
	p.mu.Unlock()
	pcm := make([]float32, maxOpusRTPPacketSamples*channels)
	state := incomingRTPState{}

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
		p.decodeIncomingRTP(rtpPkt, &state, fecEnabled, pcm)
	}
}

func negotiatedInbandFEC(fmtp string) bool {
	for _, parameter := range strings.Split(fmtp, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "useinbandfec") {
			return strings.TrimSpace(value) == "1"
		}
	}
	return false
}

func (p *pipeline) decodeIncomingRTP(packet *rtp.Packet, state *incomingRTPState, fecEnabled bool, pcm []float32) {
	if packet == nil || len(packet.Payload) == 0 {
		return
	}
	packetInfo, err := gopus.ParsePacket(packet.Payload)
	if err != nil {
		// Reject malformed framing before concealment can advance the decoder.
		// The next valid RTP packet then recovers the complete interval once.
		log.Printf("malformed incoming Opus packet: %v", err)
		return
	}
	decoderConfig := p.decoderConfig
	if len(packet.Payload) > decoderConfig.MaxPacketBytes {
		log.Printf("incoming Opus packet has %d bytes, decoder limit is %d", len(packet.Payload), decoderConfig.MaxPacketBytes)
		return
	}
	packetSamples := packetInfo.TOC.FrameSize * packetInfo.FrameCount
	if packetSamples > decoderConfig.MaxPacketSamples {
		log.Printf("incoming Opus packet has %d samples, decoder limit is %d", packetSamples, decoderConfig.MaxPacketSamples)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	channels := p.channels
	if channels < 1 || len(pcm) < maxOpusRTPPacketSamples*channels {
		log.Printf("incoming PCM buffer does not hold one maximum Opus packet")
		return
	}

	if state.initialized {
		missingPackets := uint16(packet.SequenceNumber - state.expectedSequence)
		if missingPackets >= 0x8000 {
			// Modular sequence arithmetic classifies reordered and duplicate RTP
			// packets without confusing a normal sequence-number wrap for loss.
			return
		}

		gapSamples := packet.Timestamp - state.expectedTimestamp
		if gapSamples > 0 {
			plausibleGap := gapSamples <= maxIncomingGapSamples && gapSamples%minOpusRTPPacketSamples == 0
			if missingPackets > 0 {
				minGap := uint32(missingPackets) * minOpusRTPPacketSamples
				maxGap := uint32(missingPackets) * maxOpusRTPPacketSamples
				plausibleGap = plausibleGap && int(missingPackets) <= maxIncomingGapPackets && gapSamples >= minGap && gapSamples <= maxGap
			}
			if plausibleGap {
				p.recoverIncomingGap(packet.Payload, missingPackets, int(gapSamples), fecEnabled, channels, pcm)
			} else {
				log.Printf("skip implausible incoming RTP gap: packets=%d samples=%d", missingPackets, gapSamples)
			}
		}
	}

	samples, err := p.dec.Decode(packet.Payload, pcm)
	if err != nil {
		log.Printf("decode error: %v", err)
		return
	}
	state.expectedSequence = packet.SequenceNumber + 1
	state.expectedTimestamp = packet.Timestamp + uint32(samples)
	state.initialized = true
	p.enqueueLoopbackPCM(pcm, samples, channels)
}

func (p *pipeline) recoverIncomingGap(payload []byte, missingPackets uint16, gapSamples int, fecEnabled bool, channels int, pcm []float32) {
	if gapSamples <= 0 || gapSamples%minOpusRTPPacketSamples != 0 {
		return
	}
	if missingPackets == 1 && fecEnabled && gapSamples <= maxOpusRTPPacketSamples {
		n, err := p.dec.DecodeWithFEC(payload, pcm[:gapSamples*channels], true)
		if err == nil && n == gapSamples {
			p.enqueueLoopbackPCM(pcm, n, channels)
			return
		}
		if err != nil {
			log.Printf("FEC recovery error: %v; using PLC", err)
		} else {
			log.Printf("FEC recovery returned %d samples, want %d; using PLC", n, gapSamples)
		}
	}

	for remaining := gapSamples; remaining > 0; {
		chunkSamples := min(remaining, maxOpusRTPPacketSamples)
		// Empty-packet DecodeWithFEC treats the output length as an explicit PLC
		// duration, including a full MaxPacketSamples buffer.
		n, err := p.dec.DecodeWithFEC(nil, pcm[:chunkSamples*channels], true)
		if err != nil {
			log.Printf("PLC for incoming RTP gap error: %v", err)
			return
		}
		if n != chunkSamples {
			log.Printf("PLC returned %d samples for incoming RTP gap, want %d", n, chunkSamples)
			return
		}
		p.enqueueLoopbackPCM(pcm, n, channels)
		remaining -= n
	}
}

func (p *pipeline) enqueueLoopbackPCM(pcm []float32, samples, channels int) {
	if samples <= 0 || !p.loopback {
		return
	}
	frame := make([]float32, samples*channels)
	copy(frame, pcm[:samples*channels])
	select {
	case p.loopbackCh <- frame:
	default:
		// Drop if the loopback queue is full.
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

		p.mu.Lock()
		dc := p.dataChannel
		p.mu.Unlock()
		if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
			continue
		}

		p.mu.Lock()
		packetSize := p.lastPacketSize
		frameSize := p.frameSize
		stats := map[string]any{
			"type":             "stats",
			"packetSize":       packetSize,
			"packetCount":      p.packetCount,
			"frameSize":        frameSize,
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

		var bandwidthName string
		switch toc.Bandwidth {
		case gopus.BandwidthNarrowband:
			bandwidthName = "NB"
		case gopus.BandwidthMediumband:
			bandwidthName = "MB"
		case gopus.BandwidthWideband:
			bandwidthName = "WB"
		case gopus.BandwidthSuperwideband:
			bandwidthName = "SWB"
		case gopus.BandwidthFullband:
			bandwidthName = "FB"
		}
		stats["lastBandwidth"] = bandwidthName
		stats["tocStereo"] = toc.Stereo
		stats["tocConfig"] = toc.Config

		if packetSize > 0 && frameSize > 0 {
			stats["bitrateKbps"] = float64(packetSize*8) / (float64(frameSize) / float64(sampleRate)) / 1000.0
		}

		data, _ := json.Marshal(stats)
		if err := dc.SendText(string(data)); err != nil {
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
		newEnc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: p.channels, Application: app})
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
		_ = newEnc.SetSignal(p.enc.Signal())
		_ = newEnc.SetMaxBandwidth(p.enc.MaxBandwidth())
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
		p.loopbackGeneration++
		drainLoopbackQueue(p.loopbackCh)
		if s == "loopback" {
			p.loopback = true
		} else {
			p.loopback = false
			p.gen.setSignal(s)
		}
	}
}
