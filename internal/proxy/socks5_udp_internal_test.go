package proxy

import (
	"net"
	"strconv"
	"testing"

	"github.com/miekg/dns"
)

// startFakeUDPDNS runs a plain UDP DNS server on loopback answering every query
// via reply, returning its address. Stands in for the resolver the mobile
// VpnService points the client at.
func startFakeUDPDNS(t *testing.T, reply func(*dns.Msg) *dns.Msg) string {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen fake dns: %v", err)
	}
	srv := &dns.Server{
		PacketConn: pc,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			_ = w.WriteMsg(reply(r))
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

// TestResolveDNSPlainForward covers the DoH-disabled path: resolveDNS must
// forward the query verbatim to the addressed resolver and relay its answer
// back with the transaction id preserved. (The port-53 gating and the full
// relay round-trip are covered end-to-end by the external-package tests; this
// exercises the plain-forward branch without needing to bind port 53.)
func TestResolveDNSPlainForward(t *testing.T) {
	dnsAddr := startFakeUDPDNS(t, func(r *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(r)
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.IPv4(1, 2, 3, 4),
		})
		return resp
	})
	host, portStr, _ := net.SplitHostPort(dnsAddr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	wire, _ := q.Pack()

	// No Runtime → GetPolicy is skipped, policy is nil, plain-forward branch.
	eng := &Engine{}
	respWire := eng.resolveDNS(wire, host, port, "127.0.0.1")
	if respWire == nil {
		t.Fatal("resolveDNS returned nil (plain forward failed)")
	}
	resp := new(dns.Msg)
	if err := resp.Unpack(respWire); err != nil {
		t.Fatalf("unpack response: %v", err)
	}
	if resp.Id != q.Id {
		t.Errorf("response id = %d, want %d (transaction id must be preserved)", resp.Id, q.Id)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || a.A.String() != "1.2.3.4" {
		t.Errorf("answer = %v, want A 1.2.3.4", resp.Answer[0])
	}
}

// TestDecodeSocksUDPPacket checks the request parser against a hand-built
// IPv4 SOCKS UDP datagram and rejects a fragmented one.
func TestDecodeSocksUDPPacket(t *testing.T) {
	// RSV RSV FRAG ATYP=IPv4 1.1.1.1 :53 "payload"
	pkt := []byte{0x00, 0x00, 0x00, 0x01, 1, 1, 1, 1, 0x00, 0x35}
	pkt = append(pkt, []byte("payload")...)

	raw, host, port, payload, err := decodeSocksUDPPacket(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if host != "1.1.1.1" || port != 53 {
		t.Errorf("host:port = %s:%d, want 1.1.1.1:53", host, port)
	}
	if string(payload) != "payload" {
		t.Errorf("payload = %q, want payload", payload)
	}
	if len(raw) != 1+net.IPv4len+2 {
		t.Errorf("raw addr len = %d, want 7", len(raw))
	}

	frag := append([]byte(nil), pkt...)
	frag[2] = 0x01 // fragmented
	if _, _, _, _, err := decodeSocksUDPPacket(frag); err == nil {
		t.Error("expected fragmented packet to be rejected")
	}
}
