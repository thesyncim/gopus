# Official Test Vector Decode Performance

This report compares `gopus` with the pinned C reference, libopus 1.6.1, on the same RFC 8251 `.bit` vectors.

Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `3` runs of at least `200ms` each.

Environment: `darwin/arm64`, `go1.26.0`, CPU `Apple M4 Max`.

## Summary

| Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Float32 | 27.42 | 19.20 | 1.43x | 759.8x | 1084.9x | 0 |
| Int16 | 30.28 | 19.47 | 1.56x | 687.9x | 1070.0x | 0 |

## Per-Vector Detail

| Path | Vector | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus ns/packet | libopus ns/packet | gopus realtime | libopus realtime | gopus allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Float32 | testvector01 | 40.87 | 28.30 | 1.44x | 26940 | 18653 | 509.7x | 736.1x | 0 |
| Float32 | testvector02 | 11.66 | 7.69 | 1.52x | 11820 | 7794 | 1787.0x | 2710.1x | 0 |
| Float32 | testvector03 | 13.90 | 9.68 | 1.44x | 14148 | 9850 | 1498.7x | 2152.4x | 0 |
| Float32 | testvector04 | 15.39 | 10.86 | 1.42x | 15548 | 10976 | 1354.0x | 1917.9x | 0 |
| Float32 | testvector05 | 30.71 | 21.59 | 1.42x | 19664 | 13823 | 678.3x | 965.0x | 0 |
| Float32 | testvector06 | 32.84 | 22.82 | 1.44x | 21023 | 14611 | 634.4x | 912.8x | 0 |
| Float32 | testvector07 | 27.51 | 17.92 | 1.54x | 7131 | 4645 | 757.3x | 1162.7x | 0 |
| Float32 | testvector08 | 20.68 | 14.96 | 1.38x | 21724 | 15722 | 1007.6x | 1392.2x | 0 |
| Float32 | testvector09 | 42.45 | 29.71 | 1.43x | 42029 | 29409 | 490.7x | 701.3x | 0 |
| Float32 | testvector10 | 43.53 | 29.09 | 1.50x | 34980 | 23380 | 478.6x | 716.1x | 0 |
| Float32 | testvector11 | 27.86 | 21.48 | 1.30x | 72606 | 55972 | 747.7x | 969.9x | 0 |
| Float32 | testvector12 | 12.64 | 8.48 | 1.49x | 12130 | 8144 | 1648.8x | 2455.9x | 0 |
| Int16 | testvector01 | 42.72 | 28.64 | 1.49x | 28158 | 18873 | 487.6x | 727.5x | 0 |
| Int16 | testvector02 | 15.05 | 7.77 | 1.94x | 15260 | 7875 | 1384.1x | 2682.2x | 0 |
| Int16 | testvector03 | 17.34 | 9.91 | 1.75x | 17648 | 10089 | 1201.4x | 2101.6x | 0 |
| Int16 | testvector04 | 18.78 | 10.97 | 1.71x | 18981 | 11083 | 1109.1x | 1899.4x | 0 |
| Int16 | testvector05 | 33.86 | 21.80 | 1.55x | 21680 | 13958 | 615.2x | 955.6x | 0 |
| Int16 | testvector06 | 36.15 | 23.11 | 1.56x | 23141 | 14792 | 576.3x | 901.6x | 0 |
| Int16 | testvector07 | 31.00 | 18.03 | 1.72x | 8036 | 4675 | 672.0x | 1155.2x | 0 |
| Int16 | testvector08 | 23.97 | 15.12 | 1.59x | 25179 | 15884 | 869.3x | 1378.0x | 0 |
| Int16 | testvector09 | 45.54 | 29.68 | 1.53x | 45083 | 29383 | 457.5x | 701.9x | 0 |
| Int16 | testvector10 | 46.37 | 29.32 | 1.58x | 37259 | 23563 | 449.3x | 710.5x | 0 |
| Int16 | testvector11 | 30.56 | 21.62 | 1.41x | 79627 | 56324 | 681.8x | 963.8x | 0 |
| Int16 | testvector12 | 15.54 | 8.53 | 1.82x | 14919 | 8186 | 1340.6x | 2443.1x | 0 |

## Reproduce

```sh
make bench-testvectors-compare
```

For raw Go benchmark rows, run:

```sh
make bench-testvectors
```
