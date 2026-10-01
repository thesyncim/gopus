// Package main implements a WebRTC Opus control panel that demonstrates
// every gopus encoder parameter with real-time audio streaming.
//
// Usage:
//
//	go run . -addr :8080
//	# Open http://localhost:8080 in a browser
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/pion/webrtc/v4"
)

const maxOfferBytes = 1 << 20

//go:embed index.html
var content embed.FS

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	log.Printf("Listening on %s — open %s in your browser", *addr, browserURL(*addr))
	if err := http.ListenAndServe(*addr, newHTTPHandler()); err != nil {
		log.Fatal(err)
	}
}

func browserURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + strings.TrimPrefix(addr, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func newHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/offer", handleOffer)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}

		data, err := content.ReadFile("index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	return mux
}

func handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOfferBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "offer is too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "read body", http.StatusBadRequest)
		}
		return
	}

	var offer webrtc.SessionDescription
	if err := json.Unmarshal(body, &offer); err != nil {
		http.Error(w, "bad SDP", http.StatusBadRequest)
		return
	}
	if offer.Type != webrtc.SDPTypeOffer || offer.SDP == "" {
		http.Error(w, "expected an SDP offer", http.StatusBadRequest)
		return
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, "create PC: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var p *pipeline
	keepSession := false
	defer func() {
		if keepSession {
			return
		}
		if p != nil {
			p.stop()
		}
		_ = pc.Close()
	}()

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "gopus-control",
	)
	if err != nil {
		http.Error(w, "create track: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := pc.AddTrack(track); err != nil {
		http.Error(w, "add track: "+err.Error(), http.StatusInternalServerError)
		return
	}

	p, err = newPipeline(track)
	if err != nil {
		http.Error(w, "create pipeline: "+err.Error(), http.StatusInternalServerError)
		return
	}

	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if remote.Kind() == webrtc.RTPCodecTypeAudio {
			go p.handleIncomingTrack(remote)
		}
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("DataChannel received: %s", dc.Label())
		p.setDataChannel(dc)
		dc.OnOpen(func() {
			log.Println("DataChannel open")
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			p.handleControlMessage(msg.Data)
		})
	})

	var closeOnce sync.Once
	closeSession := func() {
		closeOnce.Do(func() {
			p.stop()
			_ = pc.Close()
		})
	}
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Connection state: %s", state)
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateClosed:
			go closeSession()
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		http.Error(w, "set remote: "+err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "create answer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, "set local: "+err.Error(), http.StatusInternalServerError)
		return
	}
	select {
	case <-gatherComplete:
	case <-r.Context().Done():
		return
	}

	resp, err := json.Marshal(pc.LocalDescription())
	if err != nil {
		http.Error(w, "marshal answer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(resp); err != nil {
		return
	}

	p.start()
	keepSession = true
}
