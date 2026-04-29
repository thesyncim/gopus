# Official Test Vector Decode Performance

This report compares `gopus` with the pinned C reference, libopus 1.6.1, on the same RFC 8251 `.bit` vectors.

Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `3` runs of at least `200ms` each.

Environment: `darwin/arm64`, `go1.26.0`, CPU `Apple M4 Max`.

## Summary

| Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Float32 | 27.48 | 19.05 | 1.44x | 758.1x | 1093.7x | 0 |
| Int16 | 29.24 | 19.40 | 1.51x | 712.5x | 1074.1x | 0 |

## Per-Vector Detail

| Path | Vector | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus ns/packet | libopus ns/packet | gopus realtime | libopus realtime | gopus allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Float32 | testvector01 | 39.41 | 28.95 | 1.36x | 25973 | 19080 | 528.7x | 719.6x | 0 |
| Float32 | testvector02 | 11.40 | 7.86 | 1.45x | 11557 | 7970 | 1827.6x | 2650.2x | 0 |
| Float32 | testvector03 | 13.54 | 9.98 | 1.36x | 13776 | 10156 | 1539.1x | 2087.6x | 0 |
| Float32 | testvector04 | 15.47 | 10.96 | 1.41x | 15635 | 11072 | 1346.4x | 1901.4x | 0 |
| Float32 | testvector05 | 31.10 | 21.82 | 1.43x | 19914 | 13973 | 669.8x | 954.6x | 0 |
| Float32 | testvector06 | 32.84 | 23.22 | 1.41x | 21021 | 14867 | 634.5x | 897.1x | 0 |
| Float32 | testvector07 | 27.57 | 17.91 | 1.54x | 7146 | 4643 | 755.6x | 1163.0x | 0 |
| Float32 | testvector08 | 20.97 | 15.07 | 1.39x | 22035 | 15834 | 993.3x | 1382.3x | 0 |
| Float32 | testvector09 | 42.66 | 29.98 | 1.42x | 42234 | 29681 | 488.3x | 694.9x | 0 |
| Float32 | testvector10 | 43.67 | 29.70 | 1.47x | 35092 | 23870 | 477.1x | 701.4x | 0 |
| Float32 | testvector11 | 27.91 | 21.85 | 1.28x | 72731 | 56946 | 746.4x | 953.3x | 0 |
| Float32 | testvector12 | 12.49 | 8.57 | 1.46x | 11986 | 8230 | 1668.6x | 2430.2x | 0 |
| Int16 | testvector01 | 40.87 | 28.68 | 1.43x | 26934 | 18899 | 509.8x | 726.5x | 0 |
| Int16 | testvector02 | 13.00 | 7.77 | 1.67x | 13178 | 7879 | 1602.9x | 2680.7x | 0 |
| Int16 | testvector03 | 15.23 | 9.93 | 1.53x | 15499 | 10106 | 1368.0x | 2097.9x | 0 |
| Int16 | testvector04 | 16.81 | 11.37 | 1.48x | 16982 | 11493 | 1239.6x | 1831.7x | 0 |
| Int16 | testvector05 | 32.19 | 22.28 | 1.44x | 20606 | 14261 | 647.3x | 935.3x | 0 |
| Int16 | testvector06 | 34.40 | 23.39 | 1.47x | 22021 | 14975 | 605.6x | 890.6x | 0 |
| Int16 | testvector07 | 29.41 | 18.31 | 1.61x | 7623 | 4746 | 708.4x | 1137.7x | 0 |
| Int16 | testvector08 | 22.55 | 15.11 | 1.49x | 23693 | 15875 | 923.8x | 1378.8x | 0 |
| Int16 | testvector09 | 43.62 | 29.78 | 1.46x | 43181 | 29477 | 477.6x | 699.7x | 0 |
| Int16 | testvector10 | 46.69 | 30.24 | 1.54x | 37520 | 24297 | 446.2x | 689.0x | 0 |
| Int16 | testvector11 | 30.22 | 21.93 | 1.38x | 78757 | 57148 | 689.3x | 949.9x | 0 |
| Int16 | testvector12 | 14.44 | 8.44 | 1.71x | 13858 | 8104 | 1443.2x | 2468.0x | 0 |

## Reproduce

```sh
make bench-testvectors-compare
```

For raw Go benchmark rows, run:

```sh
make bench-testvectors
```
