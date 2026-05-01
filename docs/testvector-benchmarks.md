# Official Test Vector Decode Performance

Baseline: pinned libopus 1.6.1. This report measures `gopus` against that baseline on the same RFC 8251 `.bit` vectors.

Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `3` runs for each minimum run time: `200ms`, `1s`, `5s`. `gopus/libopus` is the speed ratio against the libopus baseline; values above `1.00x` mean `gopus` is slower.

Measured on `darwin/arm64` with `go1.26.0` on `Apple M4 Max`.

## Summary

| Run time | Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 200ms | Float32 | 19.87 | 18.13 | 1.10x | 1048.7x | 1149.0x | 0 |
| 200ms | Int16 | 20.05 | 18.51 | 1.08x | 1039.3x | 1125.7x | 0 |
| 1s | Float32 | 19.93 | 18.29 | 1.09x | 1045.3x | 1138.8x | 0 |
| 1s | Int16 | 20.17 | 18.31 | 1.10x | 1032.7x | 1137.7x | 0 |
| 5s | Float32 | 19.98 | 18.30 | 1.09x | 1042.5x | 1138.4x | 0 |
| 5s | Int16 | 20.30 | 18.55 | 1.09x | 1026.3x | 1123.0x | 0 |

## Reproduce

```sh
GOWORK=off go run ./tools/testvectorbenchcmp -cases=aggregate -paths=all -benchtimes=200ms,1s,5s -count=3 -format=markdown
```

For raw Go benchmark rows, run:

```sh
make bench-testvectors
```
