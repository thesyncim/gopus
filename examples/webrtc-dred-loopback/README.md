# WebRTC DRED Loopback

Desktop Gio example for sending microphone audio through an in-process Pion
WebRTC RTP link, injecting configurable packet loss, decoding at the receiver,
and recording or monitoring the recovered audio.

The WebRTC, RTP, and Opus paths are pure Go. Desktop microphone/speaker access
uses `malgo` as the OS audio-device bridge.

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

For a terminal smoke test that exercises the WebRTC/RTP/loss/decode loop and
prints JSON stats:

```bash
go run -tags gopus_dred . \
  -headless -duration 6s -loss 30 -loss-seed 1 -profile dred \
  -encoder-dnn dnn/encoder-dred.blob \
  -decoder-dnn dnn/decoder-dred.blob
```

The encoder blob must satisfy the encoder DNN control surface. The decoder blob
must satisfy the decoder DNN control surface and include the DRED decoder family
if receiver-side cached DRED recovery should be exercised.

## Controls

- `RTP loss`: drops outgoing RTP packets while preserving sequence gaps so the
  receiver exercises loss recovery.
- `Bitrate`: updates the live gopus encoder.
- `In-band FEC`: toggles ordinary Opus FEC independently of DRED. Single
  missing packets are recovered with `DecodeWithFEC(nextPacket, pcm, true)`.
- `Enable DRED`: arms `SetDREDDuration` when built with `-tags gopus_dred`.
- `Depth`: DRED depth in 2.5 ms units. The loss slider also updates the
  encoder expected-loss control; at 0% expected loss the encoder may not spend
  bits on DRED payloads even when the DRED toggle is on.
- `-profile dred`: the default headless/UI profile uses low-delay fullband CELT
  so the current 48 kHz DRED neural loss path is actually exercised.
- `-profile voice`: uses the speech-oriented SILK wideband profile for ordinary
  Opus/FEC checks. In the current decoder, 48 kHz SILK packets can carry DRED
  payloads, but their loss path falls back to ordinary PLC.
- `Live monitor`: plays decoded receiver audio through the speakers. Leave this
  off when speakers are near the microphone.
- `Record WAV`: writes decoded receiver audio under `recordings/`.
- `Play last WAV`: plays the most recent completed recording.
- `DREDPackets` in headless stats counts encoded Opus packets that carried a
  parseable DRED extension payload.

## Stats

The Gio stats panel and `-headless` JSON report include:

- live packet rate, drop percentage, delivered bitrate, and concealment
  milliseconds per second
- actual loss, DRED payload coverage, encoded/delivered/dropped bitrate
- emitted packet mode counts, so CELT/Hybrid/SILK runs are visible
- FEC recovery attempts, FEC output frames, FEC fallbacks, and receiver
  PLC/DRED loss-path frames
- received and concealed audio duration, latest decoded RMS/peak, and headless
  tone-reference SNR/correlation metrics for total and lost samples
- `ResilienceScore` and `RecoverySummary`, a compact recovery-health summary
  based on loss, DRED payload coverage, FEC use, concealment, and errors

## Notes

- The example uses mono 48 kHz, 20 ms frames to stay on the strongest DRED
  parity seams.
- Live playback is off by default to avoid a microphone feedback loop.
- WAV recording is on by default so loss-recovery comparisons can be replayed
  after capture.
