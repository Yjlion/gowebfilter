// Package proxy implements the forward-proxy listener(s) a client's
// browser/OS proxy setting points at: plain HTTP forwarding, CONNECT
// blind-splice passthrough for MITM-excluded hosts, full TLS interception
// (MITM) for everything else, and a SOCKS5 listener (RFC 1928/1929) that
// tunnels into the same interception path - with every request/response
// run through the ordered addon Pipeline. Deliberately a hand-rolled
// net.Listener + crypto/tls.Config.GetCertificate implementation rather
// than a raw net/http.Server - MITM interception needs to own the
// connection down to the TCP/TLS layer (see HANDOFF.md's architecture
// notes on why elazarl/goproxy wasn't used).
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

// Engine owns the forward-proxy listeners derived from settings.json's
// proxy_listen entries, plus the shared Runtime and addon Pipeline every
// connection is processed through.
type Engine struct {
	SettingsPath string
	Settings     models.GlobalSettings

	// Runtime and Pipeline are nil-safe for Listen()-only use (as in
	// engine_test.go's mode-skipping tests); Serve()/handleConn require
	// both to be set for anything beyond a plain 502 passthrough.
	Runtime  *state.Runtime
	Pipeline *Pipeline
	// Transport fetches every upstream response. Defaulted by NewEngine;
	// exported so tests can inject a custom TLSClientConfig.
	Transport *http.Transport

	connSeq atomic.Uint64
}

// NewEngine loads settings.json once. Runtime/Pipeline must be assigned by
// the caller (see cmd/webfilter/runners.go) before Run/Serve is called.
func NewEngine(settingsPath string) (*Engine, error) {
	s, err := config.LoadSettings(settingsPath)
	if err != nil {
		return nil, err
	}
	return &Engine{SettingsPath: settingsPath, Settings: s, Transport: NewTransport()}, nil
}

// Listener is a bound proxy_listen entry tagged with its base mode and
// whether it is TLS-wrapped, so Serve can dispatch SOCKS4/SOCKS5 connections
// to their respective handshakes and everything else to the HTTP-proxy path,
// terminating TLS first for the wrapped variants. It embeds net.Listener so
// existing call sites (Addr, Close) keep working via promotion.
type Listener struct {
	net.Listener
	Mode string // base protocol: regular, socks4, socks5
	TLS  bool   // accepted connections are TLS-terminated before dispatch
}

// servedModes are the base proxy_listen modes this engine actually binds and
// serves. Other modes (transparent, dns, tun, local, upstream, reverse,
// wireguard) are recognized by models.ParseListenSpec but not yet implemented
// here; Listen logs a warning and skips them.
var servedModes = map[string]bool{"regular": true, "socks4": true, "socks5": true}

// Listen binds a listener for every served-mode proxy_listen entry in
// e.Settings (optionally TLS-wrapped). It logs a warning and skips
// unimplemented modes rather than failing the whole engine over one
// unsupported entry. Split out from Run so tests can discover the actual
// bound port when a settings fixture asks for an ephemeral one (port 0).
func (e *Engine) Listen() ([]Listener, error) {
	var listeners []Listener
	for _, entry := range e.Settings.ProxyListen {
		spec := models.ParseListenSpec(entry)
		if !servedModes[spec.Mode] {
			slog.Warn("proxy_listen mode not yet implemented, skipping", "entry", entry, "mode", spec.Mode)
			continue
		}
		addr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return nil, fmt.Errorf("listen %s: %w", addr, err)
		}
		listeners = append(listeners, Listener{Listener: ln, Mode: spec.Mode, TLS: spec.TLS})
	}
	if len(listeners) == 0 {
		return nil, fmt.Errorf("no supported proxy_listen entries configured")
	}
	return listeners, nil
}

// proxyTLSConfig is the server-side TLS config for TLS-wrapped listeners
// (https@ / tls@ / tls+<base>@). The proxy presents a leaf issued on the fly
// by the runtime CA for the SNI the client sent (falling back to a fixed name
// for SNI-less clients), so a client that already trusts the CA for MITM also
// trusts the proxy endpoint itself. Only http/1.1 is advertised, matching the
// rest of the engine's no-h2 stance.
func (e *Engine) proxyTLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" && hello.Conn != nil {
				// SNI-less clients (notably those pointed at an IP-literal
				// proxy endpoint, for which no TLS client sends SNI) get a
				// leaf for the address they actually connected to, so
				// hostname/IP verification against it still passes.
				name = hostOnlyOf(hello.Conn.LocalAddr().String())
			}
			if name == "" {
				name = "webfilter-proxy"
			}
			return e.Runtime.LeafIssuer.CertificateFor(name)
		},
		NextProtos: []string{"http/1.1"},
	}
}

// Serve accepts connections on every listener until ctx is cancelled or
// one of them fails, closing every listener before returning. It takes
// ownership of listeners.
func (e *Engine) Serve(ctx context.Context, listeners []Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(listeners))
	var wg sync.WaitGroup
	for _, ln := range listeners {
		wg.Add(1)
		go func(ln Listener) {
			defer wg.Done()
			slog.Info("proxy listening", "addr", ln.Addr().String(), "mode", ln.Mode)
			if err := e.acceptLoop(ctx, ln); err != nil {
				errCh <- err
				cancel()
			}
		}(ln)
	}

	go func() {
		<-ctx.Done()
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		return err
	}
	return nil
}

func (e *Engine) acceptLoop(ctx context.Context, ln Listener) error {
	// A TLS-wrapped listener needs the runtime CA to mint its own endpoint
	// leaf; build the config once and reuse it across accepted connections.
	var tlsCfg *tls.Config
	if ln.TLS {
		if e.Runtime == nil {
			return fmt.Errorf("proxy_listen TLS mode %q on %s requires a runtime CA", ln.Mode, ln.Addr())
		}
		tlsCfg = e.proxyTLSConfig()
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept on %s: %w", ln.Addr(), err)
		}
		connID := e.connSeq.Add(1)
		go e.dispatchConn(conn, connID, ln.Mode, tlsCfg)
	}
}

// dispatchConn terminates TLS (when tlsCfg is non-nil) and then hands the
// connection to the handshake for its base mode. The TLS handshake runs here,
// in the per-connection goroutine, so a slow or failed handshake never blocks
// the accept loop.
func (e *Engine) dispatchConn(conn net.Conn, connID uint64, mode string, tlsCfg *tls.Config) {
	if tlsCfg != nil {
		tc := tls.Server(conn, tlsCfg)
		if err := tc.Handshake(); err != nil {
			_ = tc.Close()
			return
		}
		conn = tc
	}
	switch mode {
	case "socks5":
		e.serveSocksConn(conn, connID)
	case "socks4":
		e.serveSocks4Conn(conn, connID)
	default:
		e.serveConn(conn, connID)
	}
}

// Run is Listen followed by Serve - the normal production entry point.
func (e *Engine) Run(ctx context.Context) error {
	listeners, err := e.Listen()
	if err != nil {
		return err
	}
	if e.Runtime != nil {
		e.Runtime.Start(ctx)
	}
	return e.Serve(ctx, listeners)
}
