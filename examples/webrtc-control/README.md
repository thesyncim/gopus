# WebRTC Control Panel

This separate Go module serves a browser control panel for a gopus Opus encoder.
The server streams generated audio over Pion WebRTC and reports encoder settings
and packet details through a DataChannel. Selecting **Mic Loopback** sends the
browser microphone through the server encoder and back to the browser.

## Run

From this module directory:

```sh
go run . -addr :8080
```

Open [http://localhost:8080](http://localhost:8080). Microphone loopback asks
for browser microphone access when selected. Other audio sources work without
a microphone.

## Check

```sh
go test ./...
go build -o /tmp/webrtc-control .
```

The tests exercise the embedded page, bounded offer validation, and a local Pion
client-to-server audio and DataChannel session without physical audio hardware.
