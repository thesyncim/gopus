package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/thesyncim/gopus"
)

func TestPCMFrameFIFOHandlesDifferentFrameSizes(t *testing.T) {
	first := make([]float32, 960)
	second := make([]float32, 960)
	for i := range first {
		first[i] = float32(i)
		second[i] = float32(i + len(first))
	}

	var fifo pcmFIFO
	fifo.append(first)
	for frame := 0; frame < 8; frame++ {
		got := make([]float32, 120)
		if !fifo.readFrame(got) {
			t.Fatalf("read 120-sample frame %d", frame)
		}
		for i, sample := range got {
			want := first[frame*len(got)+i]
			if sample != want {
				t.Fatalf("frame %d sample %d = %v, want %v", frame, i, sample, want)
			}
		}
	}

	fifo.append(first)
	fifo.append(second)
	wideFrame := make([]float32, 2*len(first))
	if !fifo.readFrame(wideFrame) {
		t.Fatal("read a frame assembled from two input packets")
	}
	for i, sample := range wideFrame {
		want := float32(i)
		if sample != want {
			t.Fatalf("wide frame sample %d = %v, want %v", i, sample, want)
		}
	}
}

func TestBrowserURL(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{addr: ":8080", want: "http://localhost:8080/"},
		{addr: "127.0.0.1:8080", want: "http://127.0.0.1:8080/"},
		{addr: "[::]:8080", want: "http://localhost:8080/"},
		{addr: "[2001:db8::1]:8080", want: "http://[2001:db8::1]:8080/"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := browserURL(tt.addr); got != tt.want {
				t.Fatalf("browserURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestHTTPHandlerRoutes(t *testing.T) {
	handler := newHTTPHandler()

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", page.Code, http.StatusOK)
	}
	if got := page.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("GET / content type = %q", got)
	}
	if page.Body.Len() == 0 {
		t.Fatal("GET / returned an empty page")
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET /missing status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}

func TestOfferRejectsInvalidRequests(t *testing.T) {
	overLimit := strings.Repeat("x", maxOfferBytes+1)
	tests := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{name: "method", method: http.MethodGet, body: `{}`, want: http.StatusMethodNotAllowed},
		{name: "malformed JSON", method: http.MethodPost, body: `{`, want: http.StatusBadRequest},
		{name: "answer type", method: http.MethodPost, body: `{"type":"answer","sdp":"v=0"}`, want: http.StatusBadRequest},
		{name: "empty SDP", method: http.MethodPost, body: `{"type":"offer"}`, want: http.StatusBadRequest},
		{name: "invalid SDP", method: http.MethodPost, body: `{"type":"offer","sdp":"v=0\r\n"}`, want: http.StatusBadRequest},
		{name: "oversized body", method: http.MethodPost, body: overLimit, want: http.StatusRequestEntityTooLarge},
	}

	handler := newHTTPHandler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "/offer", strings.NewReader(tt.body))
			handler.ServeHTTP(response, req)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

func TestOfferHandlerPionLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create client peer: %v", err)
	}
	defer client.Close()

	type observedRTP struct {
		sequenceNumber uint16
		timestamp      uint32
	}
	remoteRTP := make(chan observedRTP, 128)
	client.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				select {
				case remoteRTP <- observedRTP{sequenceNumber: packet.SequenceNumber, timestamp: packet.Timestamp}:
				case <-ctx.Done():
					return
				}
			}
		}()
	})

	control, err := client.CreateDataChannel("control", nil)
	if err != nil {
		t.Fatalf("create control channel: %v", err)
	}
	controlOpened := make(chan struct{})
	control.OnOpen(func() { close(controlOpened) })
	type receivedStats struct {
		Loopback  bool `json:"loopback"`
		SimLoss   int  `json:"simLoss"`
		FrameSize int  `json:"frameSize"`
	}
	statsUpdates := make(chan receivedStats, 32)
	control.OnMessage(func(message webrtc.DataChannelMessage) {
		var stats receivedStats
		if json.Unmarshal(message.Data, &stats) == nil {
			select {
			case statsUpdates <- stats:
			default:
			}
		}
	})

	localTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"microphone", "client",
	)
	if err != nil {
		t.Fatalf("create microphone track: %v", err)
	}
	rtpSender, err := client.AddTrack(localTrack)
	if err != nil {
		t.Fatalf("add microphone track: %v", err)
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buffer); err != nil {
				return
			}
		}
	}()

	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    2,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("create microphone encoder: %v", err)
	}
	pcm := make([]float32, 960*2)
	for i := 0; i < len(pcm); i += 2 {
		sample := float32(0.3 * math.Sin(2*math.Pi*440*float64(i/2)/sampleRate))
		pcm[i], pcm[i+1] = sample, sample
	}
	packet := make([]byte, 4000)
	n, err := encoder.Encode(pcm, packet)
	if err != nil || n == 0 {
		t.Fatalf("encode microphone frame: n=%d err=%v", n, err)
	}

	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create client offer: %v", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatalf("set client local description: %v", err)
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		t.Fatalf("gather client ICE candidates: %v", ctx.Err())
	}
	body, err := json.Marshal(client.LocalDescription())
	if err != nil {
		t.Fatalf("marshal client offer: %v", err)
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/offer", bytes.NewReader(body))
	response := httptest.NewRecorder()
	newHTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("offer status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}

	var answer webrtc.SessionDescription
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode server answer: %v", err)
	}
	if answer.Type != webrtc.SDPTypeAnswer {
		t.Fatalf("answer type = %s, want %s", answer.Type, webrtc.SDPTypeAnswer)
	}
	if err := client.SetRemoteDescription(answer); err != nil {
		t.Fatalf("set client remote description: %v", err)
	}

	stopSamples := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopSamples:
				return
			case <-ticker.C:
				_ = localTrack.WriteSample(media.Sample{Data: packet[:n], Duration: 20 * time.Millisecond})
			}
		}
	}()
	defer close(stopSamples)

	gotControl, gotRemoteRTP, gotLoopback, gotLossSetting, gotFrameSize, gotLossGap := false, false, false, false, false, false
	var previousRTP observedRTP
	havePreviousRTP := false
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for !gotControl || !gotRemoteRTP || !gotLoopback || !gotLossSetting || !gotFrameSize || !gotLossGap {
		select {
		case <-controlOpened:
			gotControl = true
			controlOpened = nil
			for _, message := range []string{
				`{"type":"set_param","param":"audioSource","value":"loopback"}`,
				`{"type":"set_param","param":"frameSize","value":120}`,
				`{"type":"set_param","param":"simLoss","value":50}`,
			} {
				if err := control.SendText(message); err != nil {
					t.Fatalf("send control message %s: %v", message, err)
				}
			}
		case packet := <-remoteRTP:
			gotRemoteRTP = true
			if havePreviousRTP && gotLossSetting && gotFrameSize {
				if packet.sequenceNumber-previousRTP.sequenceNumber > 1 && packet.timestamp-previousRTP.timestamp > 120 {
					gotLossGap = true
				}
			}
			previousRTP = packet
			havePreviousRTP = true
		case stats := <-statsUpdates:
			gotLoopback = gotLoopback || stats.Loopback
			gotLossSetting = gotLossSetting || stats.SimLoss == 50
			gotFrameSize = gotFrameSize || stats.FrameSize == 120
		case <-timer.C:
			t.Fatalf("loopback smoke timed out: DataChannel=%v serverRTP=%v loopback=%v frameSize=%v lossApplied=%v lossGap=%v", gotControl, gotRemoteRTP, gotLoopback, gotFrameSize, gotLossSetting, gotLossGap)
		case <-ctx.Done():
			t.Fatalf("loopback smoke ended: %v", ctx.Err())
		}
	}
}
