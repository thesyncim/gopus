package multistream

import (
	"encoding/hex"
	"fmt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"testing"
)

// Concealment advances the two API-channel histories independently even after
// a mono packet. CELT_SET_CHANNELS preserves those histories during recovery.
func TestMultistreamCELTChannelRecoveryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	mono, err := hex.DecodeString("f87de653c754da8fd5b84b5c152311f42e2736250011668e86ffe7531d2559002d1069c04ba89ce0191fa84b411482b75a8d02fdc36760a2d58341a47247422ebdaad83ec8e520711ffaccd38509bac00000000000000000000000000000000000056be55a555117d558557cf16e8b3101f0c24f72c33861766a8be92409a7147be6532762112640002dc15097392136b2b9382e1e0705696039660141a53754")
	if err != nil {
		t.Fatal(err)
	}
	stereo := encodeModeSwitchSingleStreamPackets(t, 2, 960, []encoder.Mode{encoder.ModeCELT})[0]
	if stereo[0]&4 == 0 {
		t.Fatal("recovery packet must be stereo")
	}
	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, losses := range []int{0, 1, 2, 6, 15} {
			t.Run(fmt.Sprintf("fs%d/losses%d", rate, losses), func(t *testing.T) {
				packets := make([][]byte, losses+3)
				packets[0] = mono
				packets[len(packets)-2] = stereo
				packets[len(packets)-1] = stereo
				assertMultistreamSequenceFormatsMatchSelectedLibopus(t, rate, 2, packets)
			})
		}
	}
}
