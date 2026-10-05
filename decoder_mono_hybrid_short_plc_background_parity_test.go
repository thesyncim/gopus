package gopus

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMonoHybridShortPLCBackgroundMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	packetHex := []string{
		"5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
		"40822e685173fe9a9996eb0122e11bebfebeee6dd11997f75d87fe319556ab5da14072",
		"70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13ef63a92",
		"",
	}
	formats := []uint32{
		libopustest.DecodeDiffFormatInt24,
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	frameSizes := []uint32{5760, 5760, 5760, 240}
	cases := make([]libopustest.DecodeDiffCase, len(packetHex))
	for i, raw := range packetHex {
		packet, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, Format: formats[i], FrameSize: frameSizes[i]}
	}
	want, err := libopustest.ProbeDecodeSequence(48000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono-to-stereo Hybrid short PLC", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	const maxSamples = 5760 * 2
	floatPCM := make([]float32, maxSamples)
	int16PCM := make([]int16, maxSamples)
	int24PCM := make([]int32, maxSamples)
	for step, c := range cases {
		var n int
		switch c.Format {
		case libopustest.DecodeDiffFormatFloat32:
			n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
		case libopustest.DecodeDiffFormatInt16:
			n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
		default:
			n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
		}
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got := dec.FinalRange(); got != want[step].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", step, got, want[step].FinalRange)
		}
		for sample := 0; sample < n*2; sample++ {
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				got := math.Float32bits(floatPCM[sample])
				want := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
				if got != want {
					t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, want)
				}
			case libopustest.DecodeDiffFormatInt16:
				got := int16PCM[sample]
				want := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:]))
				if got != want {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, want)
				}
			default:
				got := int24PCM[sample]
				want := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:]))
				if got != want {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, want)
				}
			}
		}
	}
	decodeSequence := func() {
		dec.Reset()
		for step, c := range cases {
			var n int
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
			case libopustest.DecodeDiffFormatInt16:
				n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
			default:
				n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
			}
			if err != nil || int32(n) != want[step].Code {
				panic("decode sequence changed during allocation check")
			}
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(10, decodeSequence); allocs != 0 {
		t.Fatalf("warm decode sequence allocations=%g", allocs)
	}
}

func TestMonoCELTToStereoEnergyHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	packetHex := []string{
		"c3029fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a9",
		"f47e0540971e6dfb0379e8868ecd4a9a820822b3a59547b25a61a29495cddfc3b3df03e1202ee2dc4289afb89d15f98f21bc3058291fdac32d01fbfeab9f8c29760a03e58192bff8b6f23029af1cbebf92bf50a78a4a7103b9f1ba2b96dbbd8e1c84a4ec087cdd2c43241a427c0000000000000000000000152e5b82dbe453852650da096955445bd9796955445bd97aabe78b74598aabe78b7459881f0c24f07c3093dc70c71c304d171445c003de3ef15b905d8a4a36bae0141d87e9ace16b87dfeee40ec135ffd00300000f946e9a8fd3d163151bafc50e924a4e9d",
	}
	cases := make([]libopustest.DecodeDiffCase, len(packetHex))
	for i, raw := range packetHex {
		packet, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		cases[i] = libopustest.DecodeDiffCase{
			Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960,
		}
	}
	want, err := libopustest.ProbeDecodeSequence(8000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono-to-stereo CELT sequence", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(8000, 2))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 960*2)
	wantCounts := [...]int32{40, 80}
	for step := range cases {
		if want[step].Code != wantCounts[step] {
			t.Fatalf("C step%d returned %d samples, want %d", step, want[step].Code, wantCounts[step])
		}
		n, err := dec.Decode(cases[step].Packet, pcm)
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
			t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
		}
		for sample, got := range pcm[:n*2] {
			if expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:]); math.Float32bits(got) != expected {
				t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, math.Float32bits(got), expected)
			}
		}
	}
	decodeSequence := func() {
		dec.Reset()
		for step, c := range cases {
			n, err := dec.Decode(c.Packet, pcm)
			if err != nil || int32(n) != want[step].Code {
				panic("decode sequence changed during allocation check")
			}
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(10, decodeSequence); allocs != 0 {
		t.Fatalf("warm decode sequence allocations=%g", allocs)
	}
}

func TestMonoHybridPLCAfterRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const seed = 364
	prime := mustDecodeHex(t,
		"f3069fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d"+
			"81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e0000000000000"+
			"00000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d0"+
			"4598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1"+
			"fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c8"+
			"5ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd"+
			"46548fd8cfe354b535e00000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd587"+
			"86fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f6"+
			"5e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c"+
			"43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16"+
			"e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b61"+
			"39a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d"+
			"81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e0000000000000"+
			"00000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d0"+
			"4598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1"+
			"fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c8"+
			"5ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd"+
			"46548fd8cfe354b535e00000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd587"+
			"86fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f6"+
			"5e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c"+
			"43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16"+
			"e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b61"+
			"39a9",
	)
	mutant := mustDecodeHex(t,
		"819ecc3c1abc39107aac7ce5ce18793519666ce6d2503fd70b3adf02a51812d57da03d547d792672cc9f7f808d0be1b4"+
			"cb109a8ff8a5c3d7442690fb306450df69e14aa80d030a7671a2c2da3496d21c540e38a611329fcf9e364a4a50bf8e0b"+
			"97187cd5c4e27a5e6d1420e967fa3dedcf2c0c9ebb89002f9ead28000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000000000000000000000000000000000000000000000000000000000000000000000000000036749914e1"+
			"499436820b31dda2decbc62a4a7cefdd97a77596e8b3157b56c7182e3b8fa6bd1487f550f61fa22e0512fd2fd5641acf"+
			"ae94416cfc213656ba83d23cb0a3da8bbeec13ee1fd52bf0234c173baba139c99e8dec9f5df96dcb21c68073d31d3056"+
			"bbf06a651845f7aa47b3bcee2f6f3bdbd242873470de6cda4a0605f7abcff78fcb09a158aab56423d075d6f55028996d"+
			"9533513151df8592b280dac28205816e8488d94b5d7cc90615a4530698cae50a3103df86d3290c40012d528fc0822827"+
			"f81a5cbec3bae89183a1bbbaeb7e92548400f8175afb855026afc6e99594504d6eb31c3534f6c35b87d1018c9ea5f770"+
			"503f20ab0e952059b294a8ed4d9e59817703c96683cff1eec8be4206f25706a463f268586a6c4e4d2dd15387c95a5da2"+
			"3022bbb472ef4ff9d8dd2f3e7b13a5825bea03d1291644ae6b8830462124a501e32490a5cea2e09dadfed26215a9a033"+
			"8511a5230231aac06e4406daa8acf7a72db28f2d675220ff1cdb3860e40c72741ab481f103e0fdfaeee27f3019a63626"+
			"b03d73724783707a98998b26c3fdc9",
	)
	recovery := mustDecodeHex(t,
		"f89ecc3c1abc39107aac7c6f0f14812ad56396763f3811fbbcdeb4484cc2341f57f4397aebf1ae9cd2861f0b00c65fd6"+
			"d1bc1ade33026844ea9e43cb87cd45b73eedcafdd10e92bae665c74952f8d9e7b0d52290a322b6e7f9f08c2015ec7760"+
			"e37b614827315ae6237d4ac567edabc589879eb69ae19d328617ed4e3a42650257780886508000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000021762d14e1499436820b31dda2decbc315253e77eecbd3bacb74598abdab638c171d"+
			"c7d35e8a43faa87b0fd117028815c48573e2ee0ec143f26fc4896a9696ed5817e22fc65ce0ff2b1d5295f810d341739e"+
			"b5a139c9",
	)
	packets := [][]byte{prime, mutant, recovery, nil, prime}
	formats := [...]uint32{
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	frameSizes := [...]uint32{960, 960, 960, 40, 960}
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, Format: formats[i], FrameSize: frameSizes[i]}
	}
	want, err := libopustest.ProbeDecodeSequence(8000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono Hybrid PLC recovery", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(8000, 2))
	if err != nil {
		t.Fatal(err)
	}
	const maxSamples = 960 * 2
	floatPCM := make([]float32, maxSamples)
	int16PCM := make([]int16, maxSamples)
	int24PCM := make([]int32, maxSamples)
	for step, c := range cases {
		var n int
		switch c.Format {
		case libopustest.DecodeDiffFormatFloat32:
			n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
		case libopustest.DecodeDiffFormatInt16:
			n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
		default:
			n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
		}
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
			t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
		}
		for sample := 0; sample < n*2; sample++ {
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				got := math.Float32bits(floatPCM[sample])
				if expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:]); got != expected {
					t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
				}
			case libopustest.DecodeDiffFormatInt16:
				got := int16PCM[sample]
				if expected := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:])); got != expected {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
				}
			default:
				got := int24PCM[sample]
				if expected := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:])); got != expected {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
				}
			}
		}
	}
}
