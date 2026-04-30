# Official Test Vector Decode Performance

Baseline: pinned libopus 1.6.1. This report measures `gopus` against that baseline on the same RFC 8251 `.bit` vectors.

Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `3` runs for each minimum run time: `200ms`, `1s`, `5s`. `gopus/libopus` is the speed ratio against the libopus baseline; values above `1.00x` mean `gopus` is slower.

Measured on `darwin/arm64` with `go1.26.0` on `Apple M4 Max`.

## Summary

| Run time | Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 200ms | Float32 | 25.18 | 19.02 | 1.32x | 827.5x | 1095.4x | 0 |
| 200ms | Int16 | 25.83 | 19.22 | 1.34x | 806.6x | 1083.8x | 0 |
| 1s | Float32 | 24.72 | 19.14 | 1.29x | 842.6x | 1088.3x | 0 |
| 1s | Int16 | 25.82 | 19.30 | 1.34x | 806.9x | 1079.7x | 0 |
| 5s | Float32 | 24.69 | 19.08 | 1.29x | 843.9x | 1091.8x | 0 |
| 5s | Int16 | 25.69 | 19.26 | 1.33x | 811.1x | 1081.8x | 0 |

## Reproduce

```sh
GOWORK=off go run ./tools/testvectorbenchcmp -cases=aggregate -paths=all -benchtimes=200ms,1s,5s -count=3 -format=markdown
```

For raw Go benchmark rows, run:

```sh
make bench-testvectors
```
