# Official Test Vector Decode Performance

Baseline: pinned libopus 1.6.1. This report measures `gopus` against that baseline on the same RFC 8251 `.bit` vectors.

Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `3` runs for each minimum run time: `200ms`, `1s`, `5s`. `gopus/libopus` is the speed ratio against the libopus baseline; values above `1.00x` mean `gopus` is slower.

Measured on `darwin/arm64` with `go1.26.0` on `Apple M4 Max`.

The `gopus` rows are built with Go PGO profile `default.pgo`.

## Summary

| Run time | Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 200ms | Float32 | 19.46 | 19.44 | 1.00x | 1070.6x | 1071.4x | 0 |
| 200ms | Int16 | 20.04 | 19.57 | 1.02x | 1039.7x | 1064.5x | 0 |
| 1s | Float32 | 19.69 | 19.27 | 1.02x | 1058.1x | 1081.1x | 0 |
| 1s | Int16 | 20.08 | 19.48 | 1.03x | 1037.5x | 1069.3x | 0 |
| 5s | Float32 | 19.78 | 19.36 | 1.02x | 1053.2x | 1076.2x | 0 |
| 5s | Int16 | 20.07 | 19.52 | 1.03x | 1037.9x | 1067.5x | 0 |

## Reproduce

```sh
GOWORK=off go run -pgo=default.pgo ./tools/testvectorbenchcmp -cases=aggregate -paths=all -benchtimes=200ms,1s,5s -count=3 -gopus-pgo=default.pgo -format=markdown
```

For raw Go benchmark rows, run:

```sh
make bench-testvectors
```
