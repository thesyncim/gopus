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
  -headless -duration 6s -loss 30 \
  -encoder-dnn dnn/encoder-dred.blob \
  -decoder-dnn dnn/decoder-dred.blob
```

The encoder blob must satisfy the encoder DNN control surface. The decoder blob
must satisfy the decoder DNN control surface and include the DRED decoder family
if receiver-side cached DRED recovery should be exercised.

## Controls

- `RTP loss`: drops outgoing RTP packets while preserving sequence gaps so the
  receiver calls `Decode(nil, pcm)` for concealment.
- `Bitrate`: updates the live gopus encoder.
- `In-band FEC`: toggles ordinary Opus FEC independently of DRED.
- `Enable DRED`: arms `SetDREDDuration` when built with `-tags gopus_dred`.
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
- received and concealed audio duration, plus latest decoded RMS/peak
- `MindBlownScore` and `MindBlown`, a demo-friendly resilience summary that
  reacts to loss, DRED coverage, concealment, and encode/decode errors

## Notes

- The example uses mono 48 kHz, 20 ms frames to stay on the strongest DRED
  parity seams.
- Live playback is off by default to avoid a microphone feedback loop.
- WAV recording is on by default so loss-recovery comparisons can be replayed
  after capture.
