//go:build !gopus_qext

package gopus

type encoderHD96kFields struct{}

func (e *Encoder) is96kHz() bool { return false }

func (e *Encoder) apiFrameSize() int { return int(e.frameSize) }

func (e *Encoder) checkAndDownsample96k(_ []float32) ([]float32, int, error) {
	panic("checkAndDownsample96k called without gopus_qext build tag")
}

func init96kEncoder(_ *Encoder) {}

