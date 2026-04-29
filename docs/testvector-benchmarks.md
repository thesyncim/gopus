# Official Test Vector Decode Performance

Baseline: pinned libopus 1.6.1. This report measures `gopus` against that baseline on the same RFC 8251 `.bit` vectors.

Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `3` runs for each minimum run time: `200ms`, `1s`, `5s`. `gopus/libopus` is the speed ratio against the libopus baseline; values above `1.00x` mean `gopus` is slower.

Measured on `darwin/arm64` with `go1.26.0` on `Apple M4 Max`.

## Summary

| Run time | Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 200ms | Float32 | 23.60 | 18.36 | 1.29x | 882.6x | 1134.8x | 0 |
| 200ms | Int16 | 24.36 | 18.52 | 1.32x | 855.2x | 1125.1x | 0 |
| 1s | Float32 | 23.56 | 18.49 | 1.27x | 884.2x | 1126.5x | 0 |
| 1s | Int16 | 24.43 | 18.51 | 1.32x | 852.7x | 1125.2x | 0 |
| 5s | Float32 | 23.67 | 18.44 | 1.28x | 880.1x | 1130.0x | 0 |
| 5s | Int16 | 24.44 | 18.53 | 1.32x | 852.5x | 1124.1x | 0 |

## Reproduce

```sh
GOWORK=off go run ./tools/testvectorbenchcmp -cases=aggregate -paths=all -benchtimes=200ms,1s,5s -count=3 -format=markdown
```

For raw Go benchmark rows, run:

```sh
make bench-testvectors
```
