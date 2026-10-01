# WebRTC DRED Loopback

Desktop Gio example for sending microphone audio through an in-process Pion
WebRTC RTP link, injecting configurable packet loss, decoding at the receiver,
and recording or monitoring the recovered audio.

This is a separate Go module with a local `replace` directive for gopus.
It requires Go 1.27 or later. WebRTC, RTP, and Opus processing use Go;
microphone and speaker access uses `malgo` and requires cgo and a C compiler.
The desktop UI uses Gio.

## Run

```bash
cd examples/webrtc-dred-loopback
go run .
```

DRED controls require the tagged DRED build and compatible libopus-style DNN
blob files:

```bash
go run -tags gopus_dred . \
  -encoder-dnn /path/to/encoder-dnn.blob \
  -decoder-dnn /path/to/decoder-dnn.blob
```

If the pinned libopus DRED helper build already exists under `tmp_check/`, the
example can export compatible blobs for local demos:

```bash
go run -tags gopus_dred . -export-dnn
```

This writes `dnn/encoder-dred.blob` and `dnn/decoder-dred.blob`.

## Terminal comparison

Run a terminal test of the WebRTC/RTP/loss/decode loop with JSON statistics.
`-headless` selects the terminal path at runtime; the program still builds its
desktop dependencies. The default `-source speech` generates a deterministic
speech-like signal without opening an audio device. Use `-source tone` for a
tonal signal or `-source mic` for microphone capture.

Each run records received audio under `recordings/`. A DRED run needs the build
tag and both compatible model blobs:

```bash
go run -tags gopus_dred . \
  -headless -duration 6s -loss 30 -loss-seed 1 -profile dred \
  -encoder-dnn dnn/encoder-dred.blob \
  -decoder-dnn dnn/decoder-dred.blob
```

For a deterministic comparison with the same source, loss seed, and loss rate in
each run:

```bash
go run -tags gopus_dred . \
  -headless -compare -duration 6s -loss 60 -loss-seed 7 \
  -encoder-dnn dnn/encoder-dred.blob \
  -decoder-dnn dnn/decoder-dred.blob
```

`-compare` selects the fullband Hybrid profile unless `-profile` is set
explicitly. This profile permits comparison of in-band FEC and 48 kHz DRED
recovery. The JSON array reports `plc`, `fec`, `red`, `red+fec`, `dred`,
`fec+dred`, `red+dred`, and `red+fec+dred` runs. On receive, exact RTP RED
recovery wins before FEC, DRED, then PLC.

The encoder blob must satisfy the encoder DNN control surface. The decoder blob
must satisfy the decoder DNN control surface and include the DRED decoder family
if receiver-side cached DRED recovery should be exercised.

## Profiles and controls

All profiles use mono 48 kHz PCM and 20 ms frames.

| Profile | Encoder mode | Intended comparison |
|---|---|---|
| `dred` (default) | Low-delay fullband CELT | 48 kHz DRED neural recovery |
| `hybrid` | Fullband Hybrid | In-band FEC and DRED together |
| `voice` | Wideband SILK | Speech-oriented Opus and in-band FEC |

SILK packets can carry DRED, but `Decode(nil)` uses normal model-gated PLC and
does not consume cached DRED features. `DREDFrames` counts explicit DRED recovery;
`DREDPackets` counts packets carrying a parseable DRED payload.

| Control | Behavior |
|---|---|
| RTP loss / `-loss` | Drop outgoing RTP packets while preserving sequence gaps; also set the encoder's expected-loss percentage. |
| Bitrate / `-bitrate` | Set the encoder target in bits per second. |
| In-band FEC / `-fec` | Recover a missing frame with `DecodeWithFEC(nextPacket, pcm, true)` when available. |
| RTP RED / `-red` | Carry previous Opus payloads; `-red-depth` sets the number of prior frames. |
| Enable DRED / `-dred` | Set DRED duration in tagged builds with compatible model blobs. |
| DRED duration | Set a maximum of 0–104 redundant frames in 10 ms increments. At zero expected loss, the encoder may omit DRED payloads. |
| `-all-recovery` | Enable FEC, RED and DRED; select Hybrid unless `-profile` is explicit. |
| Live monitor | Play received audio; disabled by default. |
| Record WAV | Record received audio; enabled by default. `-record-dir` selects the directory. |
| Play last WAV | Play the most recent completed recording. |

Keep live monitoring off when speakers are near the microphone.

## Stats

The Gio stats panel and `-headless` JSON report include:

- live packet rate, drop percentage, delivered bitrate, and concealment
  milliseconds per second
- actual loss, DRED payload coverage, encoded/delivered/dropped bitrate
- emitted packet mode counts, so CELT/Hybrid/SILK runs are visible
- FEC recovery attempts, FEC output frames, FEC fallbacks, and receiver
  PLC/DRED loss-path frames
- RED recovery attempts, output frames, fallbacks, and redundant payload bytes
- DRED recovery attempts, DRED output frames, DRED fallbacks, and DRED payload
  coverage so packet carriage and audible recovery are visible separately
- received and concealed audio duration, latest decoded RMS/peak, and headless
  source-reference SNR/correlation metrics for total and lost samples
- `ReferenceIntelligibility` and `LossIntelligibility`, a pure Go STOI-style
  third-octave envelope correlation score for total and lost samples
- `ResilienceScore` and `RecoverySummary`, a compact recovery-health summary
  based on loss, DRED payload coverage, FEC use, concealment, and errors

## Notes

- The example defaults to mono 48 kHz, 20 ms frames for its DRED scenarios.
  Those demo settings do not represent the full feature or architecture parity
  matrix; see the main README for the current validation scope.
- Live playback is off by default to avoid a microphone feedback loop.
- WAV recording is on by default so loss-recovery comparisons can be replayed
  after capture.

## Tests

From this module directory:

```sh
go test -tags gopus_webrtc_headless ./...
go test -tags 'gopus_dred gopus_webrtc_headless' ./...
```

The `gopus_webrtc_headless` tag excludes the Gio entry point for tests; cgo is
still required by the audio bridge. These example tests and local demo settings
do not establish the full codec feature/architecture parity matrix. See the
[validation reference](../../reports/validation.md#coverage) for that evidence.
