package models

import (
	"strconv"
	"strings"
)

// KnownProxyModes are the proxy_listen mode prefixes this port supports.
// WireGuard is deliberately excluded (out of scope for this port per the
// project plan) but ParseListen still recognizes the literal string
// "wireguard" as a known-but-unsupported mode rather than misparsing it,
// so a settings.json carried over from the Python original doesn't error
// out - the caller decides whether to skip starting a listener for it.
var KnownProxyModes = []string{
	"regular", "transparent", "socks5", "upstream", "reverse", "dns", "tun", "local",
}

// unsupportedProxyModes are recognized but not started by this port.
var unsupportedProxyModes = map[string]bool{"wireguard": true}

func isKnownMode(m string) bool {
	for _, k := range KnownProxyModes {
		if k == m {
			return true
		}
	}
	return unsupportedProxyModes[m]
}

// SplitHostPort parses a "host:port" string, supporting plain IPv4/hostname
// (host:port), bracketed IPv6 ([::1]:port), and - for backward
// compatibility with settings.json files carried over from the Python
// original, which historically emitted IPv6 listen hosts without brackets
// - bare IPv6 (::1:port, port taken after the last colon). Returns port=0
// if unparseable.
func SplitHostPort(entry string) (host string, port int) {
	entry = trimSpace(entry)
	if entry == "" {
		return "", 0
	}
	if strings.HasPrefix(entry, "[") {
		// Bracketed IPv6: [addr]:port
		end := strings.Index(entry, "]")
		if end == -1 {
			return entry, 0
		}
		host = entry[1:end]
		rest := entry[end+1:]
		if strings.HasPrefix(rest, ":") {
			port, _ = strconv.Atoi(rest[1:])
		}
		return host, port
	}
	if strings.Count(entry, ":") > 1 {
		// Bare (unbracketed) IPv6 with a trailing :port - split on the
		// last colon, everything before it is the address.
		idx := strings.LastIndex(entry, ":")
		host = entry[:idx]
		port, _ = strconv.Atoi(entry[idx+1:])
		return host, port
	}
	// Plain host:port (IPv4 or hostname).
	idx := strings.LastIndex(entry, ":")
	if idx == -1 {
		return entry, 0
	}
	host = entry[:idx]
	port, _ = strconv.Atoi(entry[idx+1:])
	return host, port
}

// ParseListen parses a proxy_listen entry into (mode, host, port).
// Entries are either a bare "host:port" (mode defaults to "regular") or
// "mode@host:port" / bare "tun" / "local" (no address). Mirrors the
// Python original's parse_listen().
func ParseListen(entry string) (mode, host string, port int) {
	entry = trimSpace(entry)
	if entry == "" {
		return "regular", "", 0
	}
	if at := strings.Index(entry, "@"); at != -1 {
		candidate := entry[:at]
		if isKnownMode(candidate) {
			mode = candidate
			rest := entry[at+1:]
			if rest == "" {
				return mode, "", 0
			}
			host, port = SplitHostPort(rest)
			return mode, host, port
		}
	}
	if entry == "tun" || entry == "local" {
		return entry, "", 0
	}
	host, port = SplitHostPort(entry)
	return "regular", host, port
}
