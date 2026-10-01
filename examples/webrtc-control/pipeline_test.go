package main

import (
	"math"
	"testing"

	"github.com/pion/rtp"
	"github.com/thesyncim/gopus"
)

const incomingTestFrameSamples = 960

func TestHandleControlMessageRejectsInvalidValuesWithoutChangingState(t *testing.T) {
	p, err := newPipeline(nil)
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	if err := p.enc.SetBitrate(128000); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetComplexity(8); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetInBandFEC(gopus.InBandFECMusicSafe); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetPacketLoss(25); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetSignal(gopus.SignalVoice); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetMaxBandwidth(gopus.BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetLSBDepth(20); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetForceChannels(1); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetBitrateMode(gopus.BitrateModeCVBR); err != nil {
		t.Fatal(err)
	}
	p.enc.SetDTX(true)
	p.enc.SetPredictionDisabled(true)
	p.enc.SetPhaseInversionDisabled(true)
	p.loopback = true
	p.loopbackGeneration = 9
	p.simLoss = 27
	p.gen.setSignal("noise")
	p.loopbackCh = make(chan []float32, 1)
	queued := []float32{0.25, -0.5}
	p.loopbackCh <- queued

	enc := p.enc
	dec := p.dec
	generator := *p.gen
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		if p.enc != enc || p.dec != dec || p.application != gopus.ApplicationAudio || p.frameSize != 960 {
			t.Fatal("rejected control replaced codec state or changed the application/frame size")
		}
		if p.enc.Bitrate() != 128000 || p.enc.Complexity() != 8 || p.enc.InBandFEC() != gopus.InBandFECMusicSafe ||
			p.enc.PacketLoss() != 25 || p.enc.Signal() != gopus.SignalVoice || p.enc.MaxBandwidth() != gopus.BandwidthWideband ||
			!p.enc.DTXEnabled() || p.enc.LSBDepth() != 20 || !p.enc.PredictionDisabled() ||
			!p.enc.PhaseInversionDisabled() || p.enc.ForceChannels() != 1 || p.enc.BitrateMode() != gopus.BitrateModeCVBR {
			t.Fatal("rejected control changed encoder settings")
		}
		if p.simLoss != 27 || !p.loopback || p.loopbackGeneration != 9 || *p.gen != generator {
			t.Fatal("rejected control changed source, generation, simulated loss, or generator state")
		}
		select {
		case got := <-p.loopbackCh:
			if len(got) != len(queued) || &got[0] != &queued[0] {
				t.Fatal("rejected control changed the queued loopback frame")
			}
			p.loopbackCh <- got
		default:
			t.Fatal("rejected control drained the loopback queue")
		}
	}

	tests := []struct {
		name    string
		message string
	}{
		{"string bool", `{"type":"set_param","param":"fec","value":"not-a-bool"}`},
		{"numeric bool", `{"type":"set_param","param":"fec","value":1}`},
		{"string number", `{"type":"set_param","param":"complexity","value":"not-a-number"}`},
		{"fractional number", `{"type":"set_param","param":"complexity","value":7.5}`},
		{"decimal number form", `{"type":"set_param","param":"frameSize","value":960.0}`},
		{"exponent number form", `{"type":"set_param","param":"frameSize","value":9.6e2}`},
		{"codec range", `{"type":"set_param","param":"complexity","value":11}`},
		{"invalid frame size", `{"type":"set_param","param":"frameSize","value":961}`},
		{"bitrate overflow", `{"type":"set_param","param":"bitrate","value":9223372036854775808}`},
		{"packet loss range", `{"type":"set_param","param":"packetLoss","value":101}`},
		{"simulated loss range", `{"type":"set_param","param":"simLoss","value":51}`},
		{"application type", `{"type":"set_param","param":"application","value":2}`},
		{"application enum", `{"type":"set_param","param":"application","value":"unknown"}`},
		{"bitrate mode type", `{"type":"set_param","param":"bitrateMode","value":1}`},
		{"signal enum", `{"type":"set_param","param":"signal","value":"unknown"}`},
		{"bandwidth type", `{"type":"set_param","param":"maxBandwidth","value":48000}`},
		{"source type", `{"type":"set_param","param":"audioSource","value":12}`},
		{"source enum", `{"type":"set_param","param":"audioSource","value":"unknown"}`},
		{"trailing json", `{"type":"set_param","param":"audioSource","value":"sine"} {}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p.handleControlMessage([]byte(tc.message))
			assertUnchanged(t)
		})
	}
}

func TestHandleControlMessageCopiesEncoderSettingsBeforeApplicationSwap(t *testing.T) {
	p, err := newPipeline(nil)
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	if err := p.enc.SetBitrate(128000); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetComplexity(8); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetFrameSize(960); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetInBandFEC(gopus.InBandFECMusicSafe); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetPacketLoss(25); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetSignal(gopus.SignalVoice); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetMaxBandwidth(gopus.BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetLSBDepth(20); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetForceChannels(1); err != nil {
		t.Fatal(err)
	}
	if err := p.enc.SetBitrateMode(gopus.BitrateModeCVBR); err != nil {
		t.Fatal(err)
	}
	p.enc.SetDTX(true)
	p.enc.SetPredictionDisabled(true)
	p.enc.SetPhaseInversionDisabled(true)
	oldEncoder := p.enc

	p.handleControlMessage([]byte(`{"type":"set_param","param":"application","value":"lowdelay"}`))

	if p.enc == oldEncoder || p.application != gopus.ApplicationLowDelay || p.frameSize != 960 {
		t.Fatal("application control did not replace encoder and update application")
	}
	if p.enc.Bitrate() != 128000 || p.enc.Complexity() != 8 || p.enc.InBandFEC() != gopus.InBandFECMusicSafe ||
		p.enc.PacketLoss() != 25 || p.enc.Signal() != gopus.SignalVoice || p.enc.MaxBandwidth() != gopus.BandwidthWideband ||
		!p.enc.DTXEnabled() || p.enc.LSBDepth() != 20 || !p.enc.PredictionDisabled() ||
		!p.enc.PhaseInversionDisabled() || p.enc.ForceChannels() != 1 || p.enc.BitrateMode() != gopus.BitrateModeCVBR {
		t.Fatal("application control did not copy every encoder setting")
	}
}

func TestIncomingRTPUsesNegotiatedFECAcrossSequenceAndTimestampWrap(t *testing.T) {
	const (
		frameCount = 28
		lostIndex  = 20
	)
	packets := incomingVoicePackets(t, frameCount)
	if !gopus.PacketHasLBRR(packets[lostIndex+1]) {
		t.Fatal("recovery packet does not carry LBRR")
	}

	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}
	p := &pipeline{
		dec:           decoder,
		decoderConfig: gopus.DefaultDecoderConfig(sampleRate, 1),
		channels:      1,
		loopback:      true,
		loopbackCh:    make(chan []float32, frameCount),
	}
	state := incomingRTPState{}
	pcm := make([]float32, maxOpusRTPPacketSamples)
	sequenceBase := uint16(65525)
	timestampBase := ^uint32(0) - uint32(incomingTestFrameSamples*10)
	for i, payload := range packets {
		if i == lostIndex {
			continue
		}
		p.decodeIncomingRTP(&rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: sequenceBase + uint16(i),
				Timestamp:      timestampBase + uint32(i*incomingTestFrameSamples),
			},
			Payload: payload,
		}, &state, negotiatedInbandFEC("minptime=10; useinbandfec=1"), pcm)
	}

	got := collectIncomingPCM(p.loopbackCh)
	want := decodeIncomingVoiceSequence(t, packets, lostIndex)
	if len(got) != len(want) {
		t.Fatalf("loopback produced %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("loopback sample %d = %v, want FEC reference %v", i, got[i], want[i])
		}
	}
}

func TestIncomingRTPPreservesVariableDurationsAndTimestampOnlyGaps(t *testing.T) {
	timestampBase := ^uint32(0) - uint32(240)
	packets := [][]byte{
		incomingPacketAtDuration(t, 480, 0),
		incomingPacketAtDuration(t, 1920, 480),
		incomingPacketAtDuration(t, 960, 2400),
	}
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}
	p := &pipeline{
		dec:           decoder,
		decoderConfig: gopus.DefaultDecoderConfig(sampleRate, 1),
		channels:      1,
		loopback:      true,
		loopbackCh:    make(chan []float32, 8),
	}
	state := incomingRTPState{}
	pcm := make([]float32, maxOpusRTPPacketSamples)
	arrivals := []struct {
		sequence  uint16
		timestamp uint32
		payload   []byte
	}{
		{sequence: 65535, timestamp: timestampBase, payload: packets[0]},
		// No RTP packet is missing here; the timestamp gap represents a DTX interval.
		{sequence: 0, timestamp: timestampBase + uint32(480+240), payload: packets[1]},
		{sequence: 1, timestamp: timestampBase + uint32(480+240+1920+120), payload: packets[2]},
	}
	for _, arrival := range arrivals {
		p.decodeIncomingRTP(&rtp.Packet{
			Header:  rtp.Header{SequenceNumber: arrival.sequence, Timestamp: arrival.timestamp},
			Payload: arrival.payload,
		}, &state, false, pcm)
	}

	frames := drainIncomingFrames(p.loopbackCh)
	wantLengths := []int{480, 240, 1920, 120, 960}
	if len(frames) != len(wantLengths) {
		t.Fatalf("loopback frames=%d, want %d", len(frames), len(wantLengths))
	}
	for i, want := range wantLengths {
		if len(frames[i]) != want {
			t.Fatalf("frame %d has %d samples, want %d", i, len(frames[i]), want)
		}
	}
}

func TestIncomingRTPDropsReorderedPacketsAndMovesPastInvalidGap(t *testing.T) {
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}
	p := &pipeline{
		dec:           decoder,
		decoderConfig: gopus.DefaultDecoderConfig(sampleRate, 1),
		channels:      1,
		loopback:      true,
		loopbackCh:    make(chan []float32, 8),
	}
	state := incomingRTPState{}
	pcm := make([]float32, maxOpusRTPPacketSamples)
	first := incomingPacketAtDuration(t, incomingTestFrameSamples, 0)
	second := incomingPacketAtDuration(t, incomingTestFrameSamples, incomingTestFrameSamples)
	p.decodeIncomingRTP(testIncomingPacket(100, 0, first), &state, false, pcm)
	p.decodeIncomingRTP(testIncomingPacket(102, incomingTestFrameSamples*2, second), &state, false, pcm)
	p.decodeIncomingRTP(testIncomingPacket(101, incomingTestFrameSamples, second), &state, false, pcm)   // Late packet.
	p.decodeIncomingRTP(testIncomingPacket(102, incomingTestFrameSamples*2, second), &state, false, pcm) // Duplicate.
	if got := len(drainIncomingFrames(p.loopbackCh)); got != 3 {
		t.Fatalf("frames after one loss and two stale arrivals=%d, want 3", got)
	}

	invalidDecoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create invalid-gap decoder: %v", err)
	}
	p.dec = invalidDecoder
	p.loopbackCh = make(chan []float32, 8)
	state = incomingRTPState{}
	p.decodeIncomingRTP(testIncomingPacket(200, 0, first), &state, false, pcm)
	// A timestamp jump that is not an Opus duration is a discontinuity, not a
	// request for an unbounded PLC allocation.
	badTimestamp := uint32(incomingTestFrameSamples + 1)
	p.decodeIncomingRTP(testIncomingPacket(201, badTimestamp, second), &state, false, pcm)
	p.decodeIncomingRTP(testIncomingPacket(202, badTimestamp+incomingTestFrameSamples, second), &state, false, pcm)
	if got := len(drainIncomingFrames(p.loopbackCh)); got != 3 {
		t.Fatalf("frames after rejected gap=%d, want 3 valid packets", got)
	}
	if state.expectedSequence != 203 || state.expectedTimestamp != badTimestamp+2*incomingTestFrameSamples {
		t.Fatalf("state after invalid-gap rebase=(%d,%d), want (%d,%d)",
			state.expectedSequence, state.expectedTimestamp, 203, badTimestamp+2*incomingTestFrameSamples)
	}
}

func TestIncomingRTPMalformedPacketDoesNotConcealGapTwice(t *testing.T) {
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}
	p := &pipeline{
		dec:           decoder,
		decoderConfig: gopus.DefaultDecoderConfig(sampleRate, 1),
		channels:      1,
		loopback:      true,
		loopbackCh:    make(chan []float32, 8),
	}
	state := incomingRTPState{}
	pcm := make([]float32, maxOpusRTPPacketSamples)
	payload := incomingPacketAtDuration(t, incomingTestFrameSamples, 0)
	p.decodeIncomingRTP(testIncomingPacket(100, 0, payload), &state, false, pcm)
	p.decodeIncomingRTP(testIncomingPacket(102, 2*incomingTestFrameSamples, []byte{0xfb}), &state, false, pcm)
	p.decodeIncomingRTP(testIncomingPacket(103, 3*incomingTestFrameSamples, payload), &state, false, pcm)

	frames := drainIncomingFrames(p.loopbackCh)
	var samples int
	for _, frame := range frames {
		samples += len(frame)
	}
	if samples != 4*incomingTestFrameSamples {
		t.Fatalf("timeline samples=%d, want one received frame, two PLC frames, and one received frame (%d)",
			samples, 4*incomingTestFrameSamples)
	}
}

func TestIncomingRTPConcealsMaximumDurationGapInBoundedChunks(t *testing.T) {
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}
	p := &pipeline{
		dec:           decoder,
		decoderConfig: gopus.DefaultDecoderConfig(sampleRate, 1),
		channels:      1,
		loopback:      true,
		loopbackCh:    make(chan []float32, 8),
	}
	state := incomingRTPState{}
	pcm := make([]float32, maxOpusRTPPacketSamples)
	payload := incomingPacketAtDuration(t, incomingTestFrameSamples, 0)
	p.decodeIncomingRTP(testIncomingPacket(100, 0, payload), &state, false, pcm)
	p.decodeIncomingRTP(testIncomingPacket(107, 7*incomingTestFrameSamples, payload), &state, false, pcm)

	frames := drainIncomingFrames(p.loopbackCh)
	if len(frames) != 3 {
		t.Fatalf("loopback frames=%d, want initial packet, one PLC chunk, and final packet", len(frames))
	}
	if len(frames[1]) != maxOpusRTPPacketSamples {
		t.Fatalf("PLC frame length=%d, want %d", len(frames[1]), maxOpusRTPPacketSamples)
	}
	if state.expectedSequence != 108 || state.expectedTimestamp != 8*incomingTestFrameSamples {
		t.Fatalf("state after long PLC gap=(%d,%d), want (108,%d)", state.expectedSequence, state.expectedTimestamp, 8*incomingTestFrameSamples)
	}

	referenceDecoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create reference decoder: %v", err)
	}
	referencePCM := make([]float32, maxOpusRTPPacketSamples)
	firstN, err := referenceDecoder.Decode(payload, referencePCM)
	if err != nil {
		t.Fatalf("decode reference first packet: %v", err)
	}
	want := append([]float32(nil), referencePCM[:firstN]...)
	plcN, err := referenceDecoder.DecodeWithFEC(nil, referencePCM[:maxOpusRTPPacketSamples], true)
	if err != nil {
		t.Fatalf("decode reference 5760-sample PLC request: %v", err)
	}
	if plcN != maxOpusRTPPacketSamples {
		t.Fatalf("reference PLC returned %d samples, want %d", plcN, maxOpusRTPPacketSamples)
	}
	want = append(want, referencePCM[:plcN]...)
	lastN, err := referenceDecoder.Decode(payload, referencePCM)
	if err != nil {
		t.Fatalf("decode reference final packet: %v", err)
	}
	want = append(want, referencePCM[:lastN]...)
	got := make([]float32, 0, len(want))
	for _, frame := range frames {
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("loopback PCM samples=%d, want reference sequence length %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("loopback sample %d=%v, want public Decoder sequence %v", i, got[i], want[i])
		}
	}
}

func TestIncomingRTPRejectsPacketsBeyondDecoderLimitsBeforeConcealingGap(t *testing.T) {
	oversizePayload := make([]byte, 1601)
	oversizePayload[0] = 0xf9 // Two CBR frames, each 800 bytes: valid framing over the default byte cap.
	oversizeInfo, err := gopus.ParsePacket(oversizePayload)
	if err != nil {
		t.Fatalf("parse over-limit packet framing: %v", err)
	}
	oversizeDuration := oversizeInfo.TOC.FrameSize * oversizeInfo.FrameCount

	tests := []struct {
		name             string
		decoderConfig    gopus.DecoderConfig
		firstSamples     int
		firstSequence    uint16
		invalidPayload   []byte
		invalidSequence  uint16
		invalidTimestamp uint32
		finalSequence    uint16
		finalTimestamp   uint32
		wantSamples      int
	}{
		{
			name:             "packet byte limit",
			decoderConfig:    gopus.DefaultDecoderConfig(sampleRate, 1),
			firstSamples:     incomingTestFrameSamples,
			firstSequence:    100,
			invalidPayload:   oversizePayload,
			invalidSequence:  102,
			invalidTimestamp: 2 * incomingTestFrameSamples,
			finalSequence:    103,
			finalTimestamp:   uint32(2*incomingTestFrameSamples + oversizeDuration),
			wantSamples:      3*incomingTestFrameSamples + oversizeDuration,
		},
		{
			name:             "packet sample limit",
			decoderConfig:    gopus.DecoderConfig{SampleRate: sampleRate, Channels: 1, MaxPacketSamples: 480, MaxPacketBytes: 1500},
			firstSamples:     480,
			firstSequence:    100,
			invalidPayload:   incomingPacketAtDuration(t, incomingTestFrameSamples, 0),
			invalidSequence:  102,
			invalidTimestamp: 960,
			finalSequence:    103,
			finalTimestamp:   960 + incomingTestFrameSamples,
			wantSamples:      480 + 960 + incomingTestFrameSamples,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decoder, err := gopus.NewDecoder(tc.decoderConfig)
			if err != nil {
				t.Fatalf("create decoder: %v", err)
			}
			p := &pipeline{
				dec:           decoder,
				decoderConfig: tc.decoderConfig,
				channels:      1,
				loopback:      true,
				loopbackCh:    make(chan []float32, 8),
			}
			state := incomingRTPState{}
			pcm := make([]float32, maxOpusRTPPacketSamples)
			firstPayload := incomingPacketAtDuration(t, tc.firstSamples, 0)
			p.decodeIncomingRTP(testIncomingPacket(tc.firstSequence, 0, firstPayload), &state, false, pcm)
			p.decodeIncomingRTP(testIncomingPacket(tc.invalidSequence, tc.invalidTimestamp, tc.invalidPayload), &state, false, pcm)
			p.decodeIncomingRTP(testIncomingPacket(tc.finalSequence, tc.finalTimestamp, firstPayload), &state, false, pcm)

			got := len(collectIncomingPCM(p.loopbackCh))
			if got != tc.wantSamples {
				t.Fatalf("timeline samples=%d, want %d", got, tc.wantSamples)
			}
		})
	}
}

func TestNegotiatedInbandFEC(t *testing.T) {
	for _, tc := range []struct {
		fmtp string
		want bool
	}{
		{fmtp: "minptime=10;useinbandfec=1", want: true},
		{fmtp: "useinbandfec=0", want: false},
		{fmtp: "minptime=10", want: false},
		{fmtp: "x-useinbandfec=1", want: false},
	} {
		if got := negotiatedInbandFEC(tc.fmtp); got != tc.want {
			t.Errorf("negotiatedInbandFEC(%q)=%v, want %v", tc.fmtp, got, tc.want)
		}
	}
}

func incomingVoicePackets(t *testing.T, count int) [][]byte {
	t.Helper()
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    1,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("create voice encoder: %v", err)
	}
	if err := enc.SetMode(gopus.EncoderModeSILK); err != nil {
		t.Fatalf("set encoder mode: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthWideband); err != nil {
		t.Fatalf("set encoder bandwidth: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalVoice); err != nil {
		t.Fatalf("set encoder signal: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("set encoder bitrate: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("set packet loss: %v", err)
	}

	packets := make([][]byte, count)
	pcm := make([]float32, incomingTestFrameSamples)
	packet := make([]byte, 4000)
	for frame := range count {
		for i := range pcm {
			sample := frame*incomingTestFrameSamples + i
			timeSec := float64(sample) / sampleRate
			pcm[i] = 0.38*float32(math.Sin(2*math.Pi*220*timeSec)) + 0.14*float32(math.Sin(2*math.Pi*440*timeSec+0.11))
		}
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("encode voice frame %d: %v", frame, err)
		}
		packets[frame] = append([]byte(nil), packet[:n]...)
	}
	return packets
}

func incomingPacketAtDuration(t *testing.T, samples, phase int) []byte {
	t.Helper()
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    1,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("create duration encoder: %v", err)
	}
	if err := enc.SetFrameSize(samples); err != nil {
		t.Fatalf("set %d-sample frame size: %v", samples, err)
	}
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = 0.25 * float32(math.Sin(2*math.Pi*440*float64(phase+i)/sampleRate))
	}
	packet := make([]byte, 4000)
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatalf("encode %d-sample frame: %v", samples, err)
	}
	return append([]byte(nil), packet[:n]...)
}

func decodeIncomingVoiceSequence(t *testing.T, packets [][]byte, lostIndex int) []float32 {
	t.Helper()
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))
	if err != nil {
		t.Fatalf("create reference decoder: %v", err)
	}
	pcm := make([]float32, maxOpusRTPPacketSamples)
	output := make([]float32, 0, len(packets)*incomingTestFrameSamples)
	for i := 0; i < len(packets); i++ {
		if i == lostIndex {
			n, err := dec.DecodeWithFEC(packets[i+1], pcm[:incomingTestFrameSamples], true)
			if err != nil {
				t.Fatalf("reference FEC recovery for frame %d: %v", i, err)
			}
			output = append(output, pcm[:n]...)
			i++
		}
		n, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("reference decode frame %d: %v", i, err)
		}
		output = append(output, pcm[:n]...)
	}
	return output
}

func testIncomingPacket(sequence uint16, timestamp uint32, payload []byte) *rtp.Packet {
	return &rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: sequence, Timestamp: timestamp},
		Payload: payload,
	}
}

func collectIncomingPCM(frames <-chan []float32) []float32 {
	var output []float32
	for len(frames) > 0 {
		output = append(output, (<-frames)...)
	}
	return output
}

func drainIncomingFrames(frames <-chan []float32) [][]float32 {
	var output [][]float32
	for len(frames) > 0 {
		output = append(output, <-frames)
	}
	return output
}
