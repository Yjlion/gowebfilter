package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/yjlion/gowebfilter/internal/classify/image"
	"github.com/yjlion/gowebfilter/internal/classify/text"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

// errNotImplemented marks subcommands whose real implementation lands in a
// later phase of the port (see the project plan's phased build order); it
// keeps the full CLI surface visible and buildable from Phase 0 onward.
func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not implemented yet", what)
}

// buildProxyEngine wires a state.Runtime and the full addon pipeline into
// a ready-to-run proxy.Engine, in the exact registration order
// proxy/main.py uses in the Python original: management access and proxy
// auth gate management/API traffic first, then policy routing and MITM
// control, then the request-side filters (URL, DOH, safesearch), then the
// response-side filters (QUIC-blocking, YouTube, text/image
// classification), with request logging last so it observes the final
// decision.
func buildProxyEngine(settingsPath string) (*proxy.Engine, *state.Runtime, error) {
	rt, err := state.New(settingsPath)
	if err != nil {
		return nil, nil, err
	}

	authGate := addons.NewProxyAuthGate(rt)
	pipeline := proxy.NewPipeline([]proxy.Addon{
		addons.ManagementAccess{},
		authGate,
		addons.PolicyRouter{},
		addons.MitmControl{},
		addons.UrlFilter{},
		addons.QuicBlocker{},
		addons.DohFilter{},
		addons.SafeSearch{},
		addons.YouTubeFilter{},
		addons.TextClassifier{Scorer: loadTextScorer(rt.Settings.TextClassifierModelPath)},
		addons.ImageClassifier{Detector: loadImageDetector()},
		addons.RequestLogger{},
	})

	eng := &proxy.Engine{
		SettingsPath: settingsPath,
		Settings:     rt.Settings,
		Runtime:      rt,
		Pipeline:     pipeline,
		Transport:    proxy.NewTransport(),
	}
	return eng, rt, nil
}

// runProxy starts only the forward-proxy engine (no management server).
func runProxy(ctx context.Context, settingsPath string) error {
	eng, rt, err := buildProxyEngine(settingsPath)
	if err != nil {
		return fmt.Errorf("start proxy engine: %w", err)
	}
	defer rt.Logs.Close()
	return eng.Run(ctx)
}

// runMgmt starts only the management HTTP server (API + embedded UI).
func runMgmt(ctx context.Context, settingsPath string) error {
	srv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		return fmt.Errorf("start management server: %w", err)
	}
	defer srv.Logs.Close()
	return serveMgmt(ctx, srv)
}

func serveMgmt(ctx context.Context, srv *mgmtapi.Server) error {
	addr := net.JoinHostPort(srv.Settings().MgmtHost, itoa(srv.Settings().MgmtPort))
	slog.Info("management server listening", "addr", addr)

	httpSrv := &http.Server{Addr: addr, Handler: srv.Router()}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Close()
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// runProxyAndMgmt is `webfilter run`: starts the proxy engine and the
// management server as two goroutines in one process (see HANDOFF.md's
// process-model note), sharing nothing but the filesystem except for one
// in-process wire-up: a CA re-import via the management API clears the
// proxy's leaf-certificate cache immediately (mgmtapi.Server.OnCARotated),
// rather than requiring a restart, since both run in the same address
// space here. If either component fails, the other is cancelled too so
// `run` doesn't limp along half-up. Takes a bare context (rather than a
// *cobra.Command) so the Windows service handler can drive it directly,
// cancelling ctx when the SCM delivers a stop/shutdown control.
func runProxyAndMgmt(ctx context.Context, settingsPath string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eng, rt, err := buildProxyEngine(settingsPath)
	if err != nil {
		return fmt.Errorf("start proxy engine: %w", err)
	}
	defer rt.Logs.Close()

	mgmtSrv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		return fmt.Errorf("start management server: %w", err)
	}
	defer mgmtSrv.Logs.Close()
	mgmtSrv.OnCARotated = rt.LeafIssuer.Clear

	errCh := make(chan error, 2)
	go func() { errCh <- eng.Run(ctx) }()
	go func() { errCh <- serveMgmt(ctx, mgmtSrv) }()

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}

// loadTextScorer loads the ONNX-backed text ML stage, if configured. A
// missing path means keyword-only (addons.MLScorer nil); a configured path
// that fails to load logs a warning and still falls back to keyword-only
// rather than aborting startup - the ML stage is defense-in-depth on top of
// the keyword pre-filter, never the only line of defense.
//
// modelPath is a directory (model.onnx + vocab.txt + config.json - see
// internal/classify/text's package doc), not a single file, which is a
// breaking change from this project's earlier TF-IDF JSON-sidecar format.
// If the configured path is instead a ".json" file (the old format), warn
// with that specific, more actionable message rather than a generic load
// error.
func loadTextScorer(modelPath string) addons.MLScorer {
	if modelPath == "" {
		return nil
	}
	if strings.HasSuffix(modelPath, ".json") {
		slog.Warn("text_classifier: text_classifier_model_path points at a .json file, but the ML stage now expects a model directory (model.onnx+vocab.txt+config.json, see internal/classify/text) - falling back to keyword-only",
			"path", modelPath)
		return nil
	}
	m, err := text.Load(modelPath)
	if err != nil {
		slog.Warn("text_classifier: failed to load ML model, falling back to keyword-only", "path", modelPath, "err", err)
		return nil
	}
	slog.Info("text_classifier: loaded ML model", "path", modelPath)
	return m
}

// loadImageDetector loads the embedded NSFW image detector
// (internal/classify/image - GantMan/nsfw_model, MIT-licensed, no CGO, no
// model download needed). It can only fail on a corrupt build, in which
// case it logs a warning and falls back to passthrough rather than
// aborting startup.
func loadImageDetector() addons.ImageDetector {
	d, err := image.New()
	if err != nil {
		slog.Warn("image_classifier: failed to load embedded NSFW detector, falling back to passthrough", "err", err)
		return nil
	}
	return d
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
