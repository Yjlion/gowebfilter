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

// Listener is a bound proxy_listen entry tagged with its mode, so Serve can
// dispatch SOCKS5 connections to the SOCKS handshake and everything else to
// the HTTP-proxy path. It embeds net.Listener so existing call sites (Addr,
// Close) keep working via promotion.
type Listener struct {
	net.Listener
	Mode string
}

// Listen binds a listener for every "regular"- and "socks5"-mode
// proxy_listen entry in e.Settings. Other modes (transparent, dns, tun,
// local, upstream, reverse, wireguard) are recognized by models.ParseListen
// but not yet implemented by this engine; Listen logs a warning and skips
// them rather than failing the whole engine over one unsupported entry.
// Split out from Run so tests can discover the actual bound port when a
// settings fixture asks for an ephemeral one (port 0).
func (e *Engine) Listen() ([]Listener, error) {
	var listeners []Listener
	for _, entry := range e.Settings.ProxyListen {
		mode, host, port := models.ParseListen(entry)
		if mode != "regular" && mode != "socks5" {
			slog.Warn("proxy_listen mode not yet implemented, skipping", "entry", entry, "mode", mode)
			continue
		}
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return nil, fmt.Errorf("listen %s: %w", addr, err)
		}
		listeners = append(listeners, Listener{Listener: ln, Mode: mode})
	}
	if len(listeners) == 0 {
		return nil, fmt.Errorf("no supported proxy_listen entries configured")
	}
	return listeners, nil
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
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept on %s: %w", ln.Addr(), err)
		}
		connID := e.connSeq.Add(1)
		if ln.Mode == "socks5" {
			go e.serveSocksConn(conn, connID)
		} else {
			go e.serveConn(conn, connID)
		}
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
