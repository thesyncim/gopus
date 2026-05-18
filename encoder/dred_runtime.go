//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const maxDREDPCM16k = 1920

type dredEncoderModels struct {
	encoder *rdovae.EncoderModel
	pitch   *lpcnetplc.PitchDNNModel
}

func (m dredEncoderModels) loaded() bool {
	return m.encoder != nil && m.pitch != nil
}

type dredEncoderRuntime struct {
	generator           internaldred.LatentGenerator
	resampleMem         [internaldred.ResamplingOrder + 1]float32
	scaledPCM16k        [maxDREDPCM16k]float32
	latentsBuffer       [internaldred.MaxFrames * rdovae.LatentDim]float32
	stateBuffer         [internaldred.MaxFrames * rdovae.StateDim]float32
	packetSnapshot      dredEncoderPacketSnapshot
	activity            [internaldred.ActivityHistorySize]byte
	latestLatents       [rdovae.LatentDim]float32
	latestState         [rdovae.StateDim]float32
	latentsFill         int
	dredOffset          int
	latentOffset        int
	lastExtraDREDOffset int
	payload             [internaldred.MaxDataSize]byte
	emitted             int
}

type dredEncoderPacketSnapshot struct {
	valid               bool
	latentsBuffer       [internaldred.MaxFrames * rdovae.LatentDim]float32
	stateBuffer         [internaldred.MaxFrames * rdovae.StateDim]float32
	activity            [internaldred.ActivityHistorySize]byte
	latentsFill         int
	dredOffset          int
	latentOffset        int
	lastExtraDREDOffset int
}

type dredEncoderExtras struct {
	duration int
	models   dredEncoderModels
	runtime  *dredEncoderRuntime
}

func (e *Encoder) ensureDREDExtras() *dredEncoderExtras {
	if !extsupport.DREDRuntime {
		return nil
	}
	if e.dred == nil {
		e.dred = &dredEncoderExtras{}
	}
	return e.dred
}

func (e *Encoder) pruneDREDExtrasIfDormant() {
	if e.dred == nil {
		return
	}
	if e.dred.duration == 0 && !e.dred.models.loaded() {
		e.dred = nil
	}
}

func (e *Encoder) dredModelsLoaded() bool {
	return extsupport.DREDRuntime && e.dred != nil && e.dred.models.loaded()
}

func (e *Encoder) dredEncodingActive() bool {
	return extsupport.DREDRuntime && e.dnnBlob != nil && e.dred != nil && e.dred.duration > 0 && e.dred.models.loaded()
}

func (e *Encoder) resetDREDControls() {
	if !extsupport.DREDRuntime || e.dred == nil {
		return
	}
	e.dred.duration = 0
	e.dred.runtime = nil
	e.pruneDREDExtrasIfDormant()
}

func (e *Encoder) clearDREDRuntime() {
	if extsupport.DREDRuntime && e.dred != nil {
		e.dred.runtime = nil
	}
}

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob for optional extension
// paths. A nil blob clears the retained model.
func (e *Encoder) SetDNNBlob(blob *dnnblob.Blob) {
	e.dnnBlob = blob
	if e.dred != nil {
		e.dred.models = dredEncoderModels{}
		e.dred.runtime = nil
	}
	if !extsupport.DREDRuntime {
		e.pruneDREDExtrasIfDormant()
		return
	}
	if blob == nil {
		e.pruneDREDExtrasIfDormant()
		return
	}
	encModel, err := rdovae.LoadEncoder(blob)
	if err != nil {
		e.pruneDREDExtrasIfDormant()
		return
	}
	pitchModel, err := lpcnetplc.LoadPitchDNNModel(blob)
	if err != nil {
		e.pruneDREDExtrasIfDormant()
		return
	}
	extra := e.ensureDREDExtras()
	if extra == nil {
		return
	}
	extra.models.encoder = encModel
	extra.models.pitch = pitchModel
}

func (e *Encoder) ensureActiveDREDRuntime() *dredEncoderRuntime {
	if !e.dredEncodingActive() {
		return nil
	}
	if e.channels != 1 && e.channels != 2 {
		return nil
	}
	if e.dred.runtime != nil {
		return e.dred.runtime
	}
	runtime := &dredEncoderRuntime{}
	if err := runtime.generator.SetDNNBlob(e.dnnBlob); err != nil {
		return nil
	}
	e.dred.runtime = runtime
	return runtime
}

func (e *Encoder) processDREDLatents(framePCM []float64, extraDelay int) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil || len(framePCM) == 0 || len(framePCM)%e.channels != 0 {
		return 0
	}
	samples16k := e.convertDREDFrameTo16k(runtime, framePCM)
	if samples16k == 0 {
		return 0
	}
	extraDelay16k := extraDelay * 16000 / e.sampleRate
	emitted := runtime.generator.Process16k(e.dred.models.encoder, runtime.scaledPCM16k[:samples16k], extraDelay16k, func(latents, state []float32) {
		copy(runtime.latentsBuffer[rdovae.LatentDim:], runtime.latentsBuffer[:(internaldred.MaxFrames-1)*rdovae.LatentDim])
		copy(runtime.stateBuffer[rdovae.StateDim:], runtime.stateBuffer[:(internaldred.MaxFrames-1)*rdovae.StateDim])
		copy(runtime.latentsBuffer[:rdovae.LatentDim], latents[:rdovae.LatentDim])
		copy(runtime.stateBuffer[:rdovae.StateDim], state[:rdovae.StateDim])
		copy(runtime.latestLatents[:], latents[:rdovae.LatentDim])
		copy(runtime.latestState[:], state[:rdovae.StateDim])
		if runtime.latentsFill < internaldred.NumRedundancyFrames {
			runtime.latentsFill++
		}
		runtime.emitted++
	})
	runtime.dredOffset = runtime.generator.DREDOffset()
	runtime.latentOffset = runtime.generator.LatentOffset()
	internaldred.UpdateActivityHistory(&runtime.activity, len(framePCM)/e.channels, e.sampleRate, e.currentDREDActivity(framePCM))
	return emitted
}

func (e *Encoder) processDREDLatentsForPacket(framePCM []float64, frameSize, extraDelay int, mode Mode) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	if mode == ModeHybrid && frameSize > 960 && frameSize%960 == 0 {
		if e.channels <= 0 {
			return 0
		}
		frameSamples := frameSize * e.channels
		if frameSamples <= 0 || len(framePCM) < frameSamples {
			return 0
		}
		e.clearDREDPacketSnapshot()
		frameStride := 960 * e.channels
		emitted := 0
		for start := 0; start < frameSamples; start += frameStride {
			end := start + frameStride
			if end > frameSamples {
				return emitted
			}
			emitted += e.processDREDLatents(framePCM[start:end], extraDelay)
			if start == 0 {
				e.snapshotDREDPacketState()
			}
		}
		return emitted
	}
	return e.processDREDLatents(framePCM, extraDelay)
}

func (e *Encoder) snapshotDREDPacketState() {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.runtime == nil {
		return
	}
	runtime := e.dred.runtime
	snapshot := &runtime.packetSnapshot
	copy(snapshot.latentsBuffer[:], runtime.latentsBuffer[:])
	copy(snapshot.stateBuffer[:], runtime.stateBuffer[:])
	copy(snapshot.activity[:], runtime.activity[:])
	snapshot.latentsFill = runtime.latentsFill
	snapshot.dredOffset = runtime.dredOffset
	snapshot.latentOffset = runtime.latentOffset
	snapshot.lastExtraDREDOffset = runtime.lastExtraDREDOffset
	snapshot.valid = true
}

func (e *Encoder) clearDREDPacketSnapshot() {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.runtime == nil {
		return
	}
	e.dred.runtime.packetSnapshot.valid = false
}

func (e *Encoder) convertDREDFrameTo16k(runtime *dredEncoderRuntime, framePCM []float64) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	if runtime == nil || len(framePCM) == 0 || len(framePCM)%e.channels != 0 {
		return 0
	}
	frameSize16k := len(framePCM) / e.channels * 16000 / e.sampleRate
	if frameSize16k <= 0 || frameSize16k > len(runtime.scaledPCM16k) {
		return 0
	}

	input := framePCM
	out := 0
	for remaining16k := frameSize16k; remaining16k > 0; {
		processSize16k := internaldred.DFrameSize
		if processSize16k > remaining16k {
			processSize16k = remaining16k
		}
		processSize := processSize16k * e.sampleRate / 16000
		processSamples := processSize * e.channels
		if processSamples <= 0 || processSamples > len(input) {
			return 0
		}
		n := internaldred.ConvertTo16kMonoFloat64(runtime.scaledPCM16k[out:], &runtime.resampleMem, input[:processSamples], e.sampleRate, e.channels)
		if n != processSize16k {
			return 0
		}
		out += n
		// Match libopus dred_compute_latents() pcm advancement: it does
		// `pcm += process_size` regardless of channel count, which under-advances
		// the interleaved-stereo pointer by a factor of `channels` per iteration.
		// Faithfully replicate that here so the DRED encoder sees the same input
		// window libopus does on stereo 40 ms / 60 ms multi-iter Process16k calls.
		// See tmp_check/opus-1.6.1/dnn/dred_encoder.c:240.
		input = input[processSize:]
		remaining16k -= processSize16k
	}
	return out
}
