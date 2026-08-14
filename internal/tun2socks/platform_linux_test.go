//go:build linux

package tun2socks

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDescribeRoutePrivilege(t *testing.T) {
	cases := []struct {
		name    string
		euid    int
		caps    capState
		wantOK  bool
		wantHas string
	}{
		{
			name:    "root needs no capabilities",
			euid:    0,
			caps:    capState{},
			wantOK:  true,
			wantHas: "root",
		},
		{
			name:    "both capabilities",
			euid:    1000,
			caps:    capState{netAdmin: true, netRaw: true},
			wantOK:  true,
			wantHas: "CAP_NET_ADMIN",
		},
		{
			// Capture still runs; only --interface (SO_BINDTODEVICE) fails,
			// so this must not be treated as a blocking failure.
			name:    "net_admin without net_raw still works",
			euid:    1000,
			caps:    capState{netAdmin: true},
			wantOK:  true,
			wantHas: "no CAP_NET_RAW",
		},
		{
			name:    "no privileges at all",
			euid:    1000,
			caps:    capState{},
			wantOK:  false,
			wantHas: "AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW",
		},
		{
			// CAP_NET_RAW alone cannot create a TUN device or set routes.
			name:    "net_raw alone is not enough",
			euid:    1000,
			caps:    capState{netRaw: true},
			wantOK:  false,
			wantHas: "AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW",
		},
		{
			name:    "probe failure is reported alongside the remedy",
			euid:    1000,
			caps:    capState{err: errors.New("boom")},
			wantOK:  false,
			wantHas: "capability probe failed: boom",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, detail := describeRoutePrivilege(tc.euid, tc.caps)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (detail %q)", ok, tc.wantOK, detail)
			}
			if !strings.Contains(detail, tc.wantHas) {
				t.Fatalf("detail = %q, want it to contain %q", detail, tc.wantHas)
			}
		})
	}
}

// TestEffectiveCapsProbeSucceeds guards the probe itself: a capget that always
// errored would silently disable TUN capture for every non-root-but-capable
// deployment, which is exactly the configuration this check exists to support.
func TestEffectiveCapsProbeSucceeds(t *testing.T) {
	caps := effectiveCaps()
	if caps.err != nil {
		t.Fatalf("effectiveCaps() failed: %v", caps.err)
	}

	// An unprivileged test process should also be denied end-to-end. Guarded
	// so the suite still passes when run as root or with capabilities granted.
	if os.Geteuid() != 0 && !caps.netAdmin {
		if ok, detail := hasRoutePrivileges(); ok {
			t.Fatalf("hasRoutePrivileges() = true for an unprivileged process (detail %q)", detail)
		}
	}
}
