//go:build !gopus_extra_controls

package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

type decoderOSCEFields struct{}

type streamOSCEFields struct{}

func (d *Decoder) setOSCEModelState(_ dnnblob.DecoderModelState) {}
