package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// NewTransport builds the http.Transport used to fetch every upstream
// response - both plain-HTTP forward requests and MITM'd inner requests
// (which by the time they reach here are just ordinary absolute-URI
// requests with scheme https). HTTP/2 is deliberately never attempted:
// see HANDOFF.md's documented h2-over-MITM out-of-scope decision (ALPN on
// the client-facing MITM handshake below also only ever advertises
// http/1.1). Each Engine gets its own instance (rather than one shared
// package-level Transport) so tests can inject a custom TLSClientConfig
// (e.g. to trust an httptest.NewTLSServer's certificate) without any
// cross-test/cross-engine leakage.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// hopByHopHeaders are meaningful only between a client and its immediate
// proxy, never end-to-end (RFC 7230 §6.1), and must be stripped in both
// directions before forwarding.
var hopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func hostOnlyOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// serveConn handles one accepted client TCP connection: CONNECT starts a
// blind-splice or MITM tunnel; anything else is a plain-HTTP forward
// request (with its own keep-alive request loop).
func (e *Engine) serveConn(conn net.Conn, connID uint64) {
	defer conn.Close()
	clientIP := hostOnlyOf(conn.RemoteAddr().String())
	proxySockName := hostOnlyOf(conn.LocalAddr().String())

	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		e.handleConnect(conn, req, connID, clientIP, proxySockName)
		return
	}

	e.serveRequestLoop(conn, reader, req, connID, clientIP, proxySockName, "")
}

// handleConnect processes one CONNECT request: an auth gate, then either a
// blind-splice passthrough (for policy-aggregated MITM-excluded hosts) or
// full TLS interception using a leaf certificate issued by the runtime CA.
func (e *Engine) handleConnect(conn net.Conn, req *http.Request, connID uint64, clientIP, proxySockName string) {
	targetHost := req.Host
	if _, _, err := net.SplitHostPort(targetHost); err != nil {
		targetHost = net.JoinHostPort(targetHost, "443")
	}
	hostOnly, _, _ := net.SplitHostPort(targetHost)

	var gate ConnectGate
	if e.Pipeline != nil {
		gate = e.Pipeline.ConnectGateAddon()
	}
	if gate != nil {
		if !gate.AuthorizeConnect(req, connID) {
			write407(conn)
			return
		}
		defer gate.ClientDisconnected(connID)
	}

	if e.Runtime != nil && e.Runtime.ShouldBypassMitm(hostOnly) {
		blindSplice(conn, targetHost)
		return
	}
	if e.Runtime == nil {
		blindSplice(conn, targetHost)
		return
	}

	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	tlsConn := tls.Server(conn, &tls.Config{
		// Fall back to the CONNECT target when the ClientHello carries no
		// SNI - true for IP-literal HTTPS targets, since crypto/tls (like
		// every TLS client) never sends SNI for a literal IP address per
		// RFC 6066. LeafIssuer.GetCertificate alone would reject those.
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = hostOnly
			}
			return e.Runtime.LeafIssuer.CertificateFor(name)
		},
		NextProtos: []string{"http/1.1"},
	})
	defer tlsConn.Close()

	tr := bufio.NewReader(tlsConn)
	first, err := http.ReadRequest(tr)
	if err != nil {
		return
	}
	e.serveRequestLoop(tlsConn, tr, first, connID, clientIP, proxySockName, targetHost)
}

// blindSplice dials targetHost and copies bytes in both directions
// without decrypting anything - used for MITM-excluded hosts. Blocks
// until the tunnel closes so the caller's connection cleanup doesn't race
// the copy goroutines.
func blindSplice(conn net.Conn, targetHost string) {
	destConn, err := net.DialTimeout("tcp", targetHost, 10*time.Second)
	if err != nil {
		writeErrorResponse(conn, http.StatusServiceUnavailable, "proxy: "+err.Error())
		return
	}
	defer destConn.Close()
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		io.Copy(destConn, conn)
		destConn.Close()
		close(done)
	}()
	io.Copy(conn, destConn)
	conn.Close()
	<-done
}

// serveRequestLoop handles first (already read) and every subsequent
// keep-alive request on the same connection, in HTTP/1.1 request/response
// order. tunnelHost is set only for MITM'd connections (the CONNECT
// target, e.g. "example.com:443"); empty for plain-HTTP forwarding, where
// each request already carries an absolute-URI request-target.
func (e *Engine) serveRequestLoop(w io.Writer, reader *bufio.Reader, first *http.Request, connID uint64, clientIP, proxySockName, tunnelHost string) {
	req := first
	for {
		e.handleOneRequest(w, req, connID, clientIP, proxySockName, tunnelHost)
		if req.Close {
			return
		}
		next, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		req = next
	}
}

func (e *Engine) handleOneRequest(w io.Writer, req *http.Request, connID uint64, clientIP, proxySockName, tunnelHost string) {
	if tunnelHost != "" {
		req.URL.Scheme = "https"
		if req.URL.Host == "" {
			host := req.Host
			if host == "" {
				host = tunnelHost
			}
			req.URL.Host = host
		}
	} else if !req.URL.IsAbs() {
		writeErrorResponse(w, http.StatusBadRequest, "proxy: request-target must be an absolute URI")
		return
	}
	req.RequestURI = ""

	fc := &FlowContext{
		Runtime:       e.Runtime,
		ClientIP:      clientIP,
		ClientConnID:  connID,
		ProxySockName: proxySockName,
		Request:       req,
	}

	if e.Pipeline != nil {
		e.Pipeline.RunRequest(fc)
	}

	if fc.Response == nil {
		for _, h := range hopByHopHeaders {
			req.Header.Del(h)
		}
		resp, err := e.Transport.RoundTrip(req)
		if err != nil {
			if e.Pipeline != nil {
				e.Pipeline.RunError(fc)
			}
			writeErrorResponse(w, http.StatusBadGateway, "proxy: "+err.Error())
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fc.Response = resp
		fc.ResponseBody = body
	}

	if e.Pipeline != nil {
		e.Pipeline.RunResponse(fc)
	}

	writeFlowResponse(w, fc)
}

func write407(conn net.Conn) {
	body := "Proxy Authentication Required"
	fmt.Fprintf(conn, "HTTP/1.1 407 Proxy Authentication Required\r\n"+
		"Proxy-Authenticate: Basic realm=\"WebFilter Proxy\"\r\n"+
		"Content-Type: text/plain\r\n"+
		"Proxy-Connection: close\r\n"+
		"Content-Length: %d\r\n\r\n%s", len(body), body)
}

func writeErrorResponse(w io.Writer, status int, msg string) {
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\n"+
		"Content-Type: text/plain\r\n"+
		"Connection: close\r\n"+
		"Content-Length: %d\r\n\r\n%s", status, http.StatusText(status), len(msg), msg)
}

func writeFlowResponse(w io.Writer, fc *FlowContext) {
	resp := fc.Response
	if resp == nil {
		writeErrorResponse(w, http.StatusBadGateway, "proxy: no response")
		return
	}
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	for _, h := range hopByHopHeaders {
		resp.Header.Del(h)
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(fc.ResponseBody)))

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	resp.Header.Write(w)
	io.WriteString(w, "\r\n")
	w.Write(fc.ResponseBody)
}
