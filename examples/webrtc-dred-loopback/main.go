package main

import (
	"flag"
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type uiState struct {
	theme *material.Theme
	list  layout.List

	startButton widget.Clickable
	stopButton  widget.Clickable
	playButton  widget.Clickable

	lossSlider     widget.Float
	bitrateSlider  widget.Float
	durationSlider widget.Float

	fecSwitch    widget.Bool
	dredSwitch   widget.Bool
	liveSwitch   widget.Bool
	recordSwitch widget.Bool

	encoderBlob widget.Editor
	decoderBlob widget.Editor

	recordDir string
	engine    *engine
	cfg       engineConfig
	logLines  []string
}

func main() {
	var (
		encoderBlob = flag.String("encoder-dnn", "", "encoder DNN blob path for DRED")
		decoderBlob = flag.String("decoder-dnn", "", "decoder DNN blob path for DRED")
		exportDNN   = flag.Bool("export-dnn", false, "export compatible DRED DNN blobs from the pinned libopus helper build")
		dnnDir      = flag.String("dnn-dir", "dnn", "directory for -export-dnn output")
		recordDir   = flag.String("record-dir", "recordings", "directory for captured WAV files")
		headless    = flag.Bool("headless", false, "run a terminal loopback test instead of opening the Gio UI")
		source      = flag.String("source", "tone", "headless source: tone or mic")
		duration    = flag.Duration("duration", 5*time.Second, "headless run duration")
		loss        = flag.Int("loss", 15, "headless RTP loss percentage")
		lossSeed    = flag.Uint64("loss-seed", 1, "headless deterministic RTP loss seed")
		bitrate     = flag.Int("bitrate", 48000, "headless encoder bitrate")
		fec         = flag.Bool("fec", false, "enable Opus in-band FEC")
		dred        = flag.Bool("dred", dredControlsAvailable(), "enable DRED when available")
	)
	flag.Parse()

	if *exportDNN {
		exported, err := exportDNNBlobs(*dnnDir)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("encoder DNN: %s\n", exported.EncoderPath)
		fmt.Printf("decoder DNN: %s\n", exported.DecoderPath)
		if *encoderBlob == "" {
			*encoderBlob = exported.EncoderPath
		}
		if *decoderBlob == "" {
			*decoderBlob = exported.DecoderPath
		}
		if !*headless {
			return
		}
	}

	if *headless {
		cfg := defaultEngineConfig(*recordDir, *encoderBlob, *decoderBlob)
		cfg.LossPercent = *loss
		cfg.ExpectedLoss = *loss
		cfg.Bitrate = *bitrate
		cfg.LossSeed = *lossSeed
		cfg.FEC = *fec
		cfg.DRED = *dred
		stats, err := runHeadless(cfg, *source, *duration)
		if err != nil {
			log.Fatal(err)
		}
		if err := printStatsJSON(stats); err != nil {
			log.Fatal(err)
		}
		return
	}

	go func() {
		w := new(app.Window)
		w.Option(app.Title("gopus WebRTC DRED loopback"), app.Size(unit.Dp(980), unit.Dp(760)))
		ui := newUI(*recordDir, *encoderBlob, *decoderBlob)
		if err := ui.loop(w); err != nil {
			log.Print(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

func newUI(recordDir, encBlob, decBlob string) *uiState {
	cfg := defaultEngineConfig(recordDir, encBlob, decBlob)
	ui := &uiState{
		theme:     material.NewTheme(),
		recordDir: recordDir,
		cfg:       cfg,
		logLines:  []string{dredBuildStatus()},
	}
	ui.list.Axis = layout.Vertical
	ui.list.Gap = 8
	ui.lossSlider.Value = float32(cfg.LossPercent) / 60
	ui.bitrateSlider.Value = float32(cfg.Bitrate-16000) / float32(96000-16000)
	ui.durationSlider.Value = float32(cfg.DREDDuration) / 104
	ui.fecSwitch.Value = cfg.FEC
	ui.dredSwitch.Value = cfg.DRED
	ui.liveSwitch.Value = cfg.LivePlayback
	ui.recordSwitch.Value = cfg.RecordWAV
	ui.encoderBlob.SingleLine = true
	ui.encoderBlob.Submit = true
	ui.encoderBlob.InputHint = key.HintText
	ui.encoderBlob.SetText(encBlob)
	ui.decoderBlob.SingleLine = true
	ui.decoderBlob.Submit = true
	ui.decoderBlob.InputHint = key.HintText
	ui.decoderBlob.SetText(decBlob)
	return ui
}

func (ui *uiState) loop(w *app.Window) error {
	var ops op.Ops
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				w.Invalidate()
			}
		}
	}()
	defer close(done)

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			if ui.engine != nil {
				ui.engine.close()
			}
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			ui.handleFrame(gtx)
			ui.Layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (ui *uiState) handleFrame(gtx layout.Context) {
	if ui.startButton.Clicked(gtx) {
		ui.start()
	}
	if ui.stopButton.Clicked(gtx) {
		ui.stop()
	}
	if ui.playButton.Clicked(gtx) {
		ui.playLastRecording()
	}

	cfg := ui.currentConfig()
	if ui.engine != nil && cfg != ui.cfg {
		if err := ui.engine.UpdateConfig(cfg); err != nil {
			ui.addLog("update failed: " + err.Error())
		}
		ui.cfg = cfg
	}
}

func (ui *uiState) currentConfig() engineConfig {
	cfg := ui.cfg
	cfg.LossPercent = sliderInt(ui.lossSlider.Value, 0, 60, 1)
	cfg.ExpectedLoss = cfg.LossPercent
	cfg.Bitrate = sliderInt(ui.bitrateSlider.Value, 16000, 96000, 1000)
	cfg.FEC = ui.fecSwitch.Value
	cfg.DRED = ui.dredSwitch.Value
	cfg.DREDDuration = sliderInt(ui.durationSlider.Value, 0, 104, 1)
	cfg.EncoderBlobPath = strings.TrimSpace(ui.encoderBlob.Text())
	cfg.DecoderBlobPath = strings.TrimSpace(ui.decoderBlob.Text())
	cfg.LivePlayback = ui.liveSwitch.Value
	cfg.RecordWAV = ui.recordSwitch.Value
	cfg.RecordDir = ui.recordDir
	return cfg
}

func (ui *uiState) start() {
	if ui.engine != nil {
		return
	}
	cfg := ui.currentConfig()
	e, err := startEngine(cfg)
	if err != nil {
		ui.addLog("start failed: " + err.Error())
		return
	}
	ui.engine = e
	ui.cfg = cfg
	ui.addLog("started WebRTC microphone loopback")
}

func (ui *uiState) stop() {
	if ui.engine == nil {
		return
	}
	ui.engine.close()
	stats := ui.engine.Stats()
	ui.addLog("stopped")
	if stats.LastRecording != "" {
		ui.addLog("recorded " + stats.LastRecording)
	}
	ui.engine = nil
}

func (ui *uiState) playLastRecording() {
	var path string
	if ui.engine != nil {
		path = ui.engine.Stats().LastRecording
	} else {
		path = latestRecording(ui.recordDir)
	}
	if path == "" {
		ui.addLog("no recording found")
		return
	}
	if ui.engine != nil && ui.recordSwitch.Value {
		ui.addLog("stop recording before playing the current file")
		return
	}
	ui.addLog("playing " + path)
	go func() {
		if err := playWAVFile(path); err != nil {
			log.Print(err)
		}
	}()
}

func (ui *uiState) addLog(line string) {
	ui.logLines = append(ui.logLines, time.Now().Format("15:04:05")+"  "+line)
	if len(ui.logLines) > 8 {
		ui.logLines = ui.logLines[len(ui.logLines)-8:]
	}
}

func (ui *uiState) Layout(gtx layout.Context) layout.Dimensions {
	inset := layout.UniformInset(unit.Dp(16))
	return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		items := []layout.Widget{
			ui.header,
			ui.transport,
			ui.lossControls,
			ui.dredControls,
			ui.audioControls,
			ui.pathControls,
			ui.statsView,
			ui.logView,
		}
		return ui.list.Layout(gtx, len(items), func(gtx layout.Context, index int) layout.Dimensions {
			return items[index](gtx)
		})
	})
}

func (ui *uiState) header(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: 4}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			title := material.H5(ui.theme, "gopus WebRTC DRED loopback")
			title.Color = color.NRGBA{R: 31, G: 45, B: 61, A: 255}
			return title.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body2(ui.theme, "Pion RTP loopback, microphone input, receiver-side decode, optional WAV capture and monitor output.").Layout(gtx)
		}),
	)
}

func (ui *uiState) transport(gtx layout.Context) layout.Dimensions {
	return ui.panel(gtx, "Transport", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Gap: 8, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(material.Button(ui.theme, &ui.startButton, "Start").Layout),
			layout.Rigid(material.Button(ui.theme, &ui.stopButton, "Stop").Layout),
			layout.Rigid(material.Button(ui.theme, &ui.playButton, "Play last WAV").Layout),
		)
	})
}

func (ui *uiState) lossControls(gtx layout.Context) layout.Dimensions {
	return ui.panel(gtx, "Loss and Opus", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: 8}.Layout(gtx,
			layout.Rigid(ui.sliderRow("RTP loss", &ui.lossSlider, fmt.Sprintf("%d%%", sliderInt(ui.lossSlider.Value, 0, 60, 1)))),
			layout.Rigid(ui.sliderRow("Bitrate", &ui.bitrateSlider, fmt.Sprintf("%d kbps", sliderInt(ui.bitrateSlider.Value, 16000, 96000, 1000)/1000))),
			layout.Rigid(ui.switchRow("In-band FEC", &ui.fecSwitch)),
		)
	})
}

func (ui *uiState) dredControls(gtx layout.Context) layout.Dimensions {
	return ui.panel(gtx, "DRED", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: 8}.Layout(gtx,
			layout.Rigid(ui.switchRow("Enable DRED", &ui.dredSwitch)),
			layout.Rigid(ui.sliderRow("Depth", &ui.durationSlider, fmt.Sprintf("%d x 2.5 ms", sliderInt(ui.durationSlider.Value, 0, 104, 1)))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Body2(ui.theme, dredBuildStatus()).Layout(gtx)
			}),
		)
	})
}

func (ui *uiState) audioControls(gtx layout.Context) layout.Dimensions {
	return ui.panel(gtx, "Audio", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: 8}.Layout(gtx,
			layout.Rigid(ui.switchRow("Live monitor", &ui.liveSwitch)),
			layout.Rigid(ui.switchRow("Record WAV", &ui.recordSwitch)),
		)
	})
}

func (ui *uiState) pathControls(gtx layout.Context) layout.Dimensions {
	return ui.panel(gtx, "DNN blobs", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: 8}.Layout(gtx,
			layout.Rigid(ui.editorRow("Encoder", &ui.encoderBlob, "encoder DNN blob path")),
			layout.Rigid(ui.editorRow("Decoder", &ui.decoderBlob, "decoder DNN blob path")),
		)
	})
}

func (ui *uiState) statsView(gtx layout.Context) layout.Dimensions {
	stats := engineStats{State: "idle", DREDStatus: dredBuildStatus()}
	if ui.engine != nil {
		stats = ui.engine.Stats()
	}
	lines := []string{
		fmt.Sprintf("recovery score: %d/100 - %s", stats.ResilienceScore, stats.RecoverySummary),
		fmt.Sprintf("state: %s", stats.State),
		fmt.Sprintf("dred: %s", stats.DREDStatus),
		fmt.Sprintf("live: %.1f pkt/s, %.1f%% drop, %.1f kbps delivered, %.0f ms/s concealed", stats.CurrentPacketsPerSecond, stats.CurrentDropPercent, stats.CurrentDeliveredKbps, stats.CurrentConcealMSPerSecond),
		fmt.Sprintf("coverage: actual loss=%.1f%% configured=%d%% expected=%d%% dred=%.1f%%", stats.ActualLossPercent, stats.LossPercent, stats.ExpectedLoss, stats.DREDCoveragePercent),
		fmt.Sprintf("audio: received=%.2fs concealed=%.2fs total=%.2fs rms=%.3f peak=%.3f", stats.ReceivedAudioMS/1000, stats.ConcealedAudioMS/1000, stats.TotalAudioMS/1000, stats.LastRMS, stats.LastPeak),
		fmt.Sprintf("packets: sent=%d dropped=%d received=%d concealed=%d dred=%d", stats.PacketsSent, stats.PacketsDropped, stats.PacketsReceived, stats.ConcealedFrames, stats.DREDPackets),
		fmt.Sprintf("recovery: fec=%d/%d fallback=%d plc-or-dred=%d", stats.FECFrames, stats.FECRecoveryAttempts, stats.FECFallbackFrames, stats.LossPathFrames),
		fmt.Sprintf("bitrate: encoded=%.1f kbps delivered=%.1f kbps dropped=%.1f kbps last=%d B", stats.EncodedKbps, stats.DeliveredKbps, stats.DroppedKbps, stats.LastPacketBytes),
		fmt.Sprintf("errors: encode=%d decode=%d mic underruns=%d", stats.EncodeErrors, stats.DecodeErrors, stats.MicUnderruns),
	}
	if stats.LastRecording != "" {
		lines = append(lines, "wav: "+stats.LastRecording)
	}
	return ui.panel(gtx, "Stats", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: 4}.Layout(gtx, labelChildren(ui.theme, lines)...)
	})
}

func (ui *uiState) logView(gtx layout.Context) layout.Dimensions {
	return ui.panel(gtx, "Log", func(gtx layout.Context) layout.Dimensions {
		if len(ui.logLines) == 0 {
			return material.Body2(ui.theme, "").Layout(gtx)
		}
		return layout.Flex{Axis: layout.Vertical, Gap: 4}.Layout(gtx, labelChildren(ui.theme, ui.logLines)...)
	})
}

func (ui *uiState) panel(gtx layout.Context, title string, body layout.Widget) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: 8}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := material.H6(ui.theme, title)
				label.TextSize = unit.Sp(16)
				return label.Layout(gtx)
			}),
			layout.Rigid(body),
		)
	})
}

func (ui *uiState) sliderRow(label string, slider *widget.Float, value string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Gap: 12, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(120))
				return material.Body1(ui.theme, label).Layout(gtx)
			}),
			layout.Flexed(1, material.Slider(ui.theme, slider).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(84))
				return material.Body1(ui.theme, value).Layout(gtx)
			}),
		)
	}
}

func (ui *uiState) switchRow(label string, sw *widget.Bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Gap: 12, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(120))
				return material.Body1(ui.theme, label).Layout(gtx)
			}),
			layout.Rigid(material.Switch(ui.theme, sw, label).Layout),
		)
	}
}

func (ui *uiState) editorRow(label string, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Gap: 12, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(120))
				return material.Body1(ui.theme, label).Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				style := material.Editor(ui.theme, ed, hint)
				return style.Layout(gtx)
			}),
		)
	}
}

func labelChildren(th *material.Theme, lines []string) []layout.FlexChild {
	children := make([]layout.FlexChild, 0, len(lines))
	for _, line := range lines {
		text := line
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body2(th, text).Layout(gtx)
		}))
	}
	return children
}

func sliderInt(v float32, min, max, step int) int {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	n := min + int(float32(max-min)*v+0.5)
	if step > 1 {
		n = (n / step) * step
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func latestRecording(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".wav") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) {
			newest = filepath.Join(dir, ent.Name())
			newestTime = info.ModTime()
		}
	}
	return newest
}
