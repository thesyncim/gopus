# Libopus custom QEXT reference boundary

## Reproduced invalid read

Pinned libopus 1.6.1, float scalar, `CUSTOM_MODES` and `ENABLE_QEXT`,
accepts a 96 kHz, 2048-sample custom mode. Its stateful loss path reads before
the synthesis buffer. AddressSanitizer reports a read fault in
`celt_decode_lost`, `celt/celt_decoder.c:957`.

The mode has a 256-sample short transform, so `qext_scale` is 1 and
`decode_buffer_size` is 2048. With a 2048-sample frame, the source expression
`decode_buffer_size - max_period - N + extrapolation_offset + j` can be
negative. This is an invalid C reference result, not a portable PCM target.
The Go PLC path checks its history bounds and uses noise concealment when
periodic concealment cannot access the required history.

The received-frame comb filter also has no preceding sample history at
`out_syn = decode_mem + decode_buffer_size - N` for this geometry. The observed
2048-sample mono sequence differs on received frame 1 before its first loss;
the loss case has the independently confirmed sanitizer failure. No universal
byte-parity claim covers these results.

## Reproduction

The unmodified source is extracted from `tmp_check/opus-1.6.1.tar.gz` into
`/private/tmp/gopus-custom-qext-asan/opus-1.6.1`. The extracted and repository
`celt_decoder.c` both have SHA-256
`a53393c70ae917d39229f34bce2c16ada535807e095cad00a4580aee48c9bfdb`.

Configure with Clang and:

```sh
CFLAGS='-O1 -g -fsanitize=address -fno-omit-frame-pointer -ffp-contract=off -fno-vectorize -fno-slp-vectorize'
LDFLAGS='-fsanitize=address'
./configure --enable-custom-modes --enable-qext --disable-asm \
  --disable-rtcd --disable-intrinsics --enable-static --disable-shared
make -j4
```

Compile `tools/csrc/libopus_custom_stateful.c` against that instrumented
archive with `-DHAVE_CONFIG_H -fsanitize=address` and its matching headers.
Input uses `customSequenceInput` from the stateful Go oracle, mono, 200-byte
CBR frames, and loss at frames 3 and 4. The Go safety regression is
`TestQEXTFloatCustom2048SafeSequence`; the exact C sequence oracle covers the
other frame geometries.

Local evidence:

- Request: `/private/tmp/gopus-custom-qext-asan/request.bin`.
- Sanitizer output: `/private/tmp/gopus-custom-qext-asan/runtime.log`.
- Symbolized location: `celt_decode_lost (celt_decoder.c:957)`.

## Upstream status and parity scope

Checked on 2026-09-28 against upstream `main`, commit
[`503d81b138d76621aae4b12786e90de48aa8db3a`](https://github.com/xiph/opus/commit/503d81b138d76621aae4b12786e90de48aa8db3a),
dated 2026-09-11. The latest source retains the same
[history read](https://github.com/xiph/opus/blob/503d81b138d76621aae4b12786e90de48aa8db3a/celt/celt_decoder.c#L983),
2048-sample buffer, frame-size acceptance and scale selection. No fix is present
in these paths. The sanitizer reproduction above uses pinned 1.6.1, not a build
of current upstream.

Undefined C reads in this stateful 2048-sample geometry are an explicit
exception to byte/sample parity. Go preserves bounded history access and safe
concealment. First-frame encode/decode and steady-state allocation checks remain
covered; `TestQEXTFloatCustom2048SafeSequence` checks received frames, a loss
burst and recovery for both channel counts. The other custom-mode comparisons
retain exact packet, range, float PCM and int16 PCM checks.
