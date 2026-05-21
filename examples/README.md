# gopus Examples

These examples target the supported default build. QEXT examples require `-tags gopus_qext`; DRED examples require `-tags gopus_dred`; OSCE BWE remains extra-controls parity only.

Most examples run from the repository root. `external-consumer-smoke`, `webrtc-control`, and `webrtc-dred-loopback` are separate modules.

| Example | Run |
| --- | --- |
| `external-consumer-smoke` | `(cd examples/external-consumer-smoke && go test ./...)` |
| `ffmpeg-interop` | `go run ./examples/ffmpeg-interop -out output.opus -duration 2` |
| `roundtrip` | `go run ./examples/roundtrip -all` |
| `ogg-file` | `go run ./examples/ogg-file -out demo.opus -duration 5` |
| `decode-play` | `go run ./examples/decode-play -in input.opus -out decoded.wav` |
| `encode-play` | `go run ./examples/encode-play -out demo.opus` |
| `mix-arrivals` | `go run ./examples/mix-arrivals -out mixed_arrivals.opus` |
| `bench-encode` | `go run ./examples/bench-encode` |
| `bench-decode` | `go run ./examples/bench-decode` |
| `webrtc-control` | `(cd examples/webrtc-control && go run .)` |
| `webrtc-dred-loopback` | `(cd examples/webrtc-dred-loopback && go run .)` |

## Notes

- Playback examples may use `ffmpeg`, `ffprobe`, `ffplay`, or a system audio player.
- Downloading examples cache sample assets under their local cache directories.
- `webrtc-dred-loopback` has its own focused README for DRED blob export, headless mode, and desktop audio notes.
- Build non-module examples with `go build ./examples/...`.
