package proxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/models"
)

// SOCKS5 UDP-relay tunables.
const (
	// udpAssociateIdleTimeout bounds how long a UDP association with no
	// traffic stays open before the relay socket is torn down.
	udpAssociateIdleTimeout = 60 * time.Second
	// dnsUpstreamTimeout caps a single upstream DNS resolution (plain or DoH).
	dnsUpstreamTimeout = 5 * time.Second
	// dnsPort is the only UDP destination port the relay forwards; every other
	// UDP flow (e.g. QUIC on 443) is dropped so it can't bypass the MITM path.
	dnsPort = 53
	// maxConcurrentDNS bounds in-flight resolutions per association so one slow
	// upstream can't stall the read loop or spawn unbounded goroutines. A
	// browser fires many parallel DNS queries, so this must be > 1.
	maxConcurrentDNS = 256
	// dnsDatagramSize is the read buffer per relayed datagram. DNS-over-UDP is
	// bounded by the EDNS0 advertised size (queries here set 1232); 4 KiB
	// comfortably covers the SOCKS header plus an EDNS response.
	dnsDatagramSize = 4096
)

var errShortUDPPacket = errors.New("socks5: short UDP relay packet")

// serveSocksUDPAssociate handles a SOCKS5 UDP ASSOCIATE request (RFC 1928).
// It binds a loopback UDP relay socket, tells the client where to send
// datagrams, and then services the DNS queries the client relays through it.
//
// This exists for the Android TUN path: tun2socks forwards every captured UDP
// flow to the SOCKS proxy via UDP ASSOCIATE, and DNS is UDP — without this the
// client can never resolve a hostname, so no TCP CONNECT (and thus no
// filtering) ever happens. Only DNS (port 53) is relayed; other UDP is dropped
// on purpose so QUIC and friends fall back to TCP/TLS the engine can inspect.
//
// The TCP control connection stays open for the association's lifetime; when
// the client closes it, or the relay goes idle, the relay is torn down (RFC
// 1928: "a UDP association terminates when the TCP connection ... terminates").
func (e *Engine) serveSocksUDPAssociate(conn net.Conn, clientIP string) {
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		writeSocksReply(conn, repGeneralFailure)
		return
	}
	defer relay.Close()

	bound := relay.LocalAddr().(*net.UDPAddr)
	if err := writeSocksReplyUDP(conn, bound.IP, bound.Port); err != nil {
		return
	}

	// The client keeps the TCP control connection open for as long as it wants
	// the association; when it closes, tear down the relay to unblock the loop.
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		relay.Close()
	}()

	e.relaySocksUDP(relay, clientIP)
}

// relaySocksUDP reads SOCKS-encapsulated UDP datagrams from the client and
// answers DNS queries, dropping everything else. Each query is resolved in its
// own goroutine (bounded by maxConcurrentDNS) so a slow upstream can't stall
// the read loop — a browser fires many DNS lookups in parallel. Returns when
// the relay socket is closed (control connection gone) or goes idle.
func (e *Engine) relaySocksUDP(relay *net.UDPConn, clientIP string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentDNS)
	defer wg.Wait()

	for {
		buf := make([]byte, dnsDatagramSize)
		_ = relay.SetReadDeadline(time.Now().Add(udpAssociateIdleTimeout))
		n, clientAddr, err := relay.ReadFromUDP(buf)
		if err != nil {
			return // deadline exceeded or relay closed
		}

		rawAddr, host, port, payload, err := decodeSocksUDPPacket(buf[:n])
		if err != nil {
			continue
		}
		// Only DNS is relayed. Dropping other UDP (notably QUIC on 443) keeps
		// it from tunnelling around the MITM pipeline.
		if port != dnsPort {
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(rawAddr, payload []byte, clientAddr *net.UDPAddr, host string, port int) {
			defer wg.Done()
			defer func() { <-sem }()

			resp := e.resolveDNS(payload, host, port, clientIP)
			if resp == nil {
				return
			}
			// net.UDPConn writes are safe for concurrent use; loopback sends
			// don't block, so no per-write deadline is needed.
			_, _ = relay.WriteToUDP(encodeSocksUDPPacket(rawAddr, resp), clientAddr)
		}(rawAddr, payload, clientAddr, host, port)
	}
}

// resolveDNS answers one DNS query. When the applicable policy enables DoH
// filtering and the queried name is in scope, it resolves through the policy's
// DoH server (a filtering resolver, which returns NXDOMAIN/sinkhole for blocked
// names); otherwise it forwards the query verbatim to the resolver the client
// addressed (the VpnService-configured DNS server on the mobile path). Returns
// nil when resolution fails, so the relay simply drops the datagram and the
// client retries — fail-open, matching doh_filter's behaviour.
func (e *Engine) resolveDNS(query []byte, dstHost string, dstPort int, clientIP string) []byte {
	var policy *models.Policy
	if e.Runtime != nil {
		policy = e.Runtime.GetPolicy(clientIP)
	}

	name := dnsQuestionName(query)
	if policy != nil && policy.Doh.Enabled && dnsShouldFilter(name, policy.Doh) {
		if resp := forwardDoh(query, policy.Doh.Server); resp != nil {
			if dnsResponseBlocked(resp) {
				e.logDNSBlock(name, policy, clientIP)
			}
			return resp
		}
		// DoH resolver unreachable: fall back to plain DNS rather than break
		// browsing entirely. Fail-open matches doh_filter's behaviour on
		// lookup failure (filtering is best-effort during a resolver outage).
		slog.Warn("doh_filter: DoH resolve failed, falling back to plain DNS", "host", name, "server", policy.Doh.Server)
	}

	upstream := net.JoinHostPort(dstHost, strconv.Itoa(dstPort))
	return forwardPlainDNS(query, upstream)
}

// dnsShouldFilter mirrors doh_filter.go's dohShouldFilter: include_only wins,
// then exclude, else everything is in scope. An unparseable name is out of
// scope (resolved plainly) rather than force-filtered.
func dnsShouldFilter(host string, cfg models.DohConfig) bool {
	if host == "" {
		return false
	}
	if len(cfg.IncludeOnly) > 0 {
		return DomainInList(host, cfg.IncludeOnly)
	}
	if len(cfg.Exclude) > 0 {
		return !DomainInList(host, cfg.Exclude)
	}
	return true
}

func (e *Engine) logDNSBlock(name string, policy *models.Policy, clientIP string) {
	if e.Runtime == nil || e.Runtime.Logs == nil {
		return
	}
	slog.Info("doh_filter: blocked", "host", name, "server", policy.Doh.Server, "via", "dns")
	_ = e.Runtime.Logs.LogBlock(logstore.BlockEntry{
		TS:        time.Now().Unix(),
		Domain:    name,
		URL:       "dns://" + name,
		Reason:    "Domain blocked by DNS policy (" + policy.Doh.Server + ")",
		Component: "doh",
		Policy:    policy.Name,
		ClientIP:  clientIP,
	})
}

// proxyDohClient POSTs DNS wireformat queries to a DoH endpoint. Like
// doh_filter's client it never routes through a configured system proxy (which
// could be this proxy itself) — resolutions must go out directly.
var proxyDohClient = &http.Client{
	Timeout:   dnsUpstreamTimeout,
	Transport: &http.Transport{Proxy: nil},
}

// forwardDoh relays a raw DNS query wire to an RFC 8484 DoH server and returns
// the raw response wire (which preserves the client's transaction ID). Returns
// nil on any failure.
func forwardDoh(query []byte, server string) []byte {
	req, err := http.NewRequest(http.MethodPost, server, bytes.NewReader(query))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := proxyDohClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil
	}
	return body
}

// forwardPlainDNS relays a raw DNS query to a plain UDP resolver and returns
// the raw response. Returns nil on any failure.
func forwardPlainDNS(query []byte, server string) []byte {
	conn, err := net.DialTimeout("udp", server, dnsUpstreamTimeout)
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(dnsUpstreamTimeout))
	if _, err := conn.Write(query); err != nil {
		return nil
	}
	buf := make([]byte, 64*1024)
	n, err := conn.Read(buf)
	if err != nil {
		return nil
	}
	return append([]byte(nil), buf[:n]...)
}

// dnsQuestionName returns the lowercased, dot-trimmed first question name of a
// DNS query wire, or "" if it can't be parsed.
func dnsQuestionName(query []byte) string {
	msg := new(dns.Msg)
	if err := msg.Unpack(query); err != nil || len(msg.Question) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(msg.Question[0].Name, "."))
}

// dnsResponseBlocked reports whether a DNS response wire signals a filtered
// domain: NXDOMAIN, an RFC 8914 Extended DNS Error block info-code, or a
// sinkhole/block-page answer IP — the conventions filtering resolvers use.
// Mirrors doh_filter.go's classifyDoh (block-detection half).
func dnsResponseBlocked(resp []byte) bool {
	msg := new(dns.Msg)
	if err := msg.Unpack(resp); err != nil {
		return false
	}
	if msg.Rcode == dns.RcodeNameError {
		return true
	}
	if opt := msg.IsEdns0(); opt != nil {
		for _, o := range opt.Option {
			if ede, ok := o.(*dns.EDNS0_EDE); ok && edeBlockCodes[ede.InfoCode] {
				return true
			}
		}
	}
	for _, rr := range msg.Answer {
		var addr string
		switch v := rr.(type) {
		case *dns.A:
			addr = v.A.String()
		case *dns.AAAA:
			addr = v.AAAA.String()
		default:
			continue
		}
		if ip := net.ParseIP(addr); ip != nil && dnsBlockAddrs[ip.String()] {
			return true
		}
	}
	return false
}

// edeBlockCodes are RFC 8914 Extended DNS Error info-codes meaning "blocked":
// Blocked(15), Censored(16), Filtered(17). Mirrors doh_filter.go.
var edeBlockCodes = map[uint16]bool{15: true, 16: true, 17: true}

// dnsBlockAddrs are sinkhole and provider block-page IPs. Mirrors
// doh_filter.go's blockAddrStrings.
var dnsBlockAddrs = func() map[string]bool {
	strs := []string{
		"0.0.0.0", "::", "127.0.0.1", // sinkholes
		"94.140.14.35", "94.140.14.36", // AdGuard block page (IPv4)
		"2a10:50c0::bad1:ff", "2a10:50c0::bad2:ff", // AdGuard block page (IPv6)
	}
	m := make(map[string]bool, len(strs))
	for _, s := range strs {
		if ip := net.ParseIP(s); ip != nil {
			m[ip.String()] = true
		}
	}
	return m
}()

// decodeSocksUDPPacket parses a SOCKS5 UDP request datagram (RFC 1928 §7):
// RSV(2) FRAG(1) ATYP DST.ADDR DST.PORT DATA. It returns the raw address
// header (ATYP+ADDR+PORT, reused verbatim when wrapping the reply), the parsed
// destination host/port, and the payload. Fragmented datagrams (FRAG != 0) are
// rejected — this relay doesn't reassemble.
func decodeSocksUDPPacket(pkt []byte) (rawAddr []byte, host string, port int, payload []byte, err error) {
	if len(pkt) < 5 {
		return nil, "", 0, nil, errShortUDPPacket
	}
	if pkt[0] != 0 || pkt[1] != 0 {
		return nil, "", 0, nil, errors.New("socks5: nonzero UDP RSV")
	}
	if pkt[2] != 0 {
		return nil, "", 0, nil, errors.New("socks5: fragmented UDP unsupported")
	}

	atyp := pkt[3]
	var addrLen int
	switch atyp {
	case atypIPv4:
		addrLen = 1 + net.IPv4len + 2
	case atypIPv6:
		addrLen = 1 + net.IPv6len + 2
	case atypDomain:
		dlen := int(pkt[4])
		addrLen = 1 + 1 + dlen + 2
	default:
		return nil, "", 0, nil, errUnsupportedAddrType
	}
	if len(pkt) < 3+addrLen {
		return nil, "", 0, nil, errShortUDPPacket
	}

	rawAddr = pkt[3 : 3+addrLen]
	payload = pkt[3+addrLen:]

	portBytes := rawAddr[addrLen-2:]
	port = int(binary.BigEndian.Uint16(portBytes))
	switch atyp {
	case atypIPv4:
		host = net.IP(rawAddr[1 : 1+net.IPv4len]).String()
	case atypIPv6:
		host = net.IP(rawAddr[1 : 1+net.IPv6len]).String()
	case atypDomain:
		host = string(rawAddr[2 : 2+int(rawAddr[1])])
	}
	return rawAddr, host, port, payload, nil
}

// encodeSocksUDPPacket wraps a DNS response payload in a SOCKS5 UDP reply
// datagram, echoing the request's address header so the client sees the reply
// as coming from the address it queried.
func encodeSocksUDPPacket(rawAddr, payload []byte) []byte {
	out := make([]byte, 0, 3+len(rawAddr)+len(payload))
	out = append(out, 0x00, 0x00, 0x00) // RSV, RSV, FRAG
	out = append(out, rawAddr...)
	out = append(out, payload...)
	return out
}

// writeSocksReplyUDP writes a SOCKS5 success reply whose BND.ADDR/BND.PORT is
// the UDP relay the client should send datagrams to.
func writeSocksReplyUDP(w io.Writer, ip net.IP, port int) error {
	reply := []byte{socksVersion, repSucceeded, 0x00}
	if ip4 := ip.To4(); ip4 != nil {
		reply = append(reply, atypIPv4)
		reply = append(reply, ip4...)
	} else {
		reply = append(reply, atypIPv6)
		reply = append(reply, ip.To16()...)
	}
	reply = binary.BigEndian.AppendUint16(reply, uint16(port))
	_, err := w.Write(reply)
	return err
}
