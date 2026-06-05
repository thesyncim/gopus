# gopus Examples

Runnable programs that exercise the public gopus API.

These examples target the supported default build. QEXT examples require `-tags gopus_qext`; DRED examples require `-tags gopus_dred`; OSCE BWE remains extra-controls parity only.

Most examples run from the repository root. `external-consumer-smoke`,
`webrtc-control`, and `webrtc-dred-loopback` are separate Go modules (run them
from their own directory). Build the in-module examples with
`go build ./examples/...`.

Examples that download or play audio shell out to optional tools (`ffmpeg`,
`ffplay`, `ffprobe`, `opusenc`, or a system audio player) and cache any sample
assets under a local cache directory. The core encode/decode examples need none
of these.

## Start here

| Example | What it shows | Run |
| --- | --- | --- |
| `roundtrip-min` | The smallest encode then decode using the caller-buffer API. | `go run ./examples/roundtrip-min` |
| `packet-loss` | Loss recovery: PLC via `Decode(nil)` and in-band FEC via `DecodeWithFEC`. | `go run ./examples/packet-loss` |
| `ogg-file` | Write and read an Ogg Opus file (`container/ogg`), with seeking. | `go run ./examples/ogg-file -out demo.opus -duration 5` |

## Public API focus

Small, single-purpose programs covering one public API area each.

| Example | What it shows | Run |
| --- | --- | --- |
| `sample-rates` | Encode/decode the int16 PCM API at 8/12/16/24/48 kHz. | `go run ./examples/sample-rates` |
| `low-delay` | CELT-only `ApplicationLowDelay` with short frames; reports algorithmic delay. | `go run ./examples/low-delay` |
| `repacketizer` | Merge/split frames with `Repacketizer`, inspect with `ParsePacket`, pad/unpad. | `go run ./examples/repacketizer` |
| `surround` | Multistream 5.1 encode/decode via the default Vorbis-order mapping. | `go run ./examples/surround` |

## Encode / decode / files

| Example | What it shows | Run |
| --- | --- | --- |
| `roundtrip` | Encode then decode quality metrics (SNR, correlation) across a matrix. | `go run ./examples/roundtrip -all` |
| `encode-play` | Encode generated audio to Ogg Opus and optionally play it. | `go run ./examples/encode-play -out demo.opus` |
| `decode-play` | Decode an Ogg Opus file to WAV or stream it to a player. | `go run ./examples/decode-play -in input.opus -out decoded.wav` |
| `ffmpeg-interop` | Encode for ffmpeg/ffprobe and decode ffmpeg-produced Opus. | `go run ./examples/ffmpeg-interop -out output.opus -duration 2` |
| `mix-arrivals` | WebRTC-style timed mixing of speech tracks with loss/jitter simulation. | `go run ./examples/mix-arrivals -out mixed_arrivals.opus` |

## Benchmarks

| Example | What it shows | Run |
| --- | --- | --- |
| `bench-encode` | Encode throughput, gopus vs pinned libopus (`opus_demo`). | `go run ./examples/bench-encode` |
| `bench-decode` | Decode throughput, gopus vs pinned libopus (`opus_demo`). | `go run ./examples/bench-decode` |

## Separate modules

| Example | What it shows | Run |
| --- | --- | --- |
| `external-consumer-smoke` | A downstream module using the public encode/decode, Ogg, RED, and PLC APIs. | `(cd examples/external-consumer-smoke && go test ./...)` |
| `webrtc-control` | Browser control panel driving every encoder parameter over Pion WebRTC. | `(cd examples/webrtc-control && go run .)` |
| `webrtc-dred-loopback` | Desktop loopback comparing PLC, FEC, RED, and DRED loss recovery. | `(cd examples/webrtc-dred-loopback && go run .)` |

`webrtc-dred-loopback` has its own focused README covering DRED blob export,
headless mode, and desktop audio notes.
