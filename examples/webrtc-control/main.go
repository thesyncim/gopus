// Package main implements a WebRTC Opus control panel that demonstrates
// every gopus encoder parameter with real-time audio streaming.
//
// Usage:
//
//	go run . -addr :8080
//	# Open http://localhost:8080 in browser
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/pion/webrtc/v4"
)

//go:embed index.html
var content embed.FS

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := content.ReadFile("index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	http.HandleFunc("/offer", handleOffer)

	log.Printf("Listening on %s — open http://localhost%s in your browser", *addr, *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal(err)
	}
}

func handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.Unmarshal(body, &offer); err != nil {
		http.Error(w, "bad SDP", http.StatusBadRequest)
		return
	}

	// Create peer connection.
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, "create PC: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create output audio track.
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

	// Create pipeline (DataChannel is set when the browser's DC arrives).
	p, err := newPipeline(track)
	if err != nil {
		http.Error(w, "create pipeline: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle incoming audio tracks (for mic loopback).
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if remote.Kind() == webrtc.RTPCodecTypeAudio {
			go p.handleIncomingTrack(remote)
		}
	})

	// The browser creates the DataChannel (so its offer includes SCTP).
	// We receive it here and wire it into the pipeline.
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

	// Clean up on connection close.
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Connection state: %s", state)
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			p.stop()
		}
	})

	// Set remote description.
	if err := pc.SetRemoteDescription(offer); err != nil {
		http.Error(w, "set remote: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create answer.
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "create answer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Wait for ICE gathering to complete for a single-roundtrip signaling.
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, "set local: "+err.Error(), http.StatusInternalServerError)
		return
	}
	<-gatherComplete

	// Start the audio pipeline.
	p.start()

	// Return the answer with complete ICE candidates.
	resp, err := json.Marshal(pc.LocalDescription())
	if err != nil {
		http.Error(w, "marshal answer", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, string(resp))
}
