# Frozen opusdec inputs

These 11 Ogg files reproduce all 11 scenario names and 11 of the 18 hashes in
`expected_pcm.json`. The other seven hash aliases remain in the archived PCM
file. The test does not claim to replay those aliases.

`expected_pcm.json` is a byte-for-byte copy of
`internal/celt/testdata/opusdec_crossval_fixture_linux_amd64.json` at
`9056116d76671601da7a0e7916bd5c2d21880631`. Its SHA256 is
`61290ea23a385524b31ff43ee289fe94181df4a19712ef0da67788a5760b03c2`.
The original fixture path remains the output of platform fixture generation;
this archived copy defines historical decoder expectations.

The unmodified capture manifest has SHA256
`a4ad202c90010efc05c4a138a197b04336b4a8d4766362f04e7623bb4040b3f0`.
[Recovery run 36274171857](https://github.com/thesyncim/gopus/actions/runs/36274171857)
uses the recorded encoder commit, Go version and native AMD64 features and
requires every emitted Ogg hash to match an existing entry.

The recorded decoder packages are libopus `1.4-1build1`, opus-tools
`0.2-1build3`, and libopusfile `0.12-4build3`, as shown by the producer runs in
the manifest. The legacy JSON `libopus_version: 1.6.1` field is hardcoded and
does not describe that producer. These files test historical opusdec
compatibility; matched live libopus 1.6.1 oracles establish codec parity.
