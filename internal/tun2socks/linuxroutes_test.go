package tun2socks

import (
	"context"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

// recordingRunner captures the exact command lines configureLinux/
// unconfigureLinux would run. The commandRunner seam has existed since the
// first version of this package for precisely this purpose and had no callers;
// these are the riskiest lines in the repo (they rewrite a live host's
// routing), so they are worth pinning argument-for-argument.
type recordingRunner struct {
	cmds []string
	fail map[string]error
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) error {
	line := name + " " + strings.Join(args, " ")
	r.cmds = append(r.cmds, line)
	return r.fail[line]
}

// after returns the commands recorded from index i onward, which is how each
// test skips the pre-clean configureLinux runs before doing its real work.
func (r *recordingRunner) after(i int) []string { return r.cmds[i:] }

func linuxCfg() models.Tun2SocksConfig {
	cfg := models.NewTun2SocksConfig()
	cfg.Enabled = true
	return cfg
}

func wantCmds(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ran %d commands, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d:\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
}

// The teardown sequence is the whole safety story: it must drop the rules
// before the table they select, and it must never mention the main table.
var wantTeardown = []string{
	"ip rule del pref 9100",
	"ip rule del pref 9000",
	"ip route flush table 8888",
	"ip link del webfilter-tun",
}

func TestConfigureLinuxUsesPolicyRoutingNotTheMainTable(t *testing.T) {
	r := &recordingRunner{}
	if err := configureLinux(context.Background(), linuxCfg(), r); err != nil {
		t.Fatalf("configureLinux() = %v", err)
	}

	// Every run starts by reclaiming a previous run's leftovers, because
	// `ip rule add` is additive and would otherwise stack duplicates.
	wantCmds(t, r.cmds[:len(wantTeardown)], wantTeardown)

	wantCmds(t, r.after(len(wantTeardown)), []string{
		"ip tuntap add mode tun dev webfilter-tun",
		"ip addr replace 198.18.0.1/15 dev webfilter-tun",
		"ip link set dev webfilter-tun up",
		"ip route replace default via 198.18.0.1 dev webfilter-tun table 8888",
		"ip route replace throw 127.0.0.0/8 table 8888",
		"ip route replace throw 10.0.0.0/8 table 8888",
		"ip route replace throw 172.16.0.0/12 table 8888",
		"ip route replace throw 192.168.0.0/16 table 8888",
		"ip rule add pref 9000 fwmark 0x5745 lookup main",
		"ip rule add pref 9100 lookup 8888",
	})

	// The regression that motivated all of this: the old implementation ran
	// `ip route replace default ... metric 1` with no table, which displaced
	// the host's real default route and was never put back.
	for _, cmd := range r.cmds {
		if strings.HasPrefix(cmd, "ip route replace default") && !strings.Contains(cmd, "table 8888") {
			t.Errorf("configureLinux wrote a default route outside its private table: %s", cmd)
		}
	}
}

func TestConfigureLinuxHonoursTunNetmask(t *testing.T) {
	cases := []struct {
		netmask string
		want    string
	}{
		{"255.254.0.0", "198.18.0.1/15"}, // the documented default
		{"255.255.255.0", "198.18.0.1/24"},
		{"255.255.0.0", "198.18.0.1/16"},
		{"", "198.18.0.1/15"},         // UnmarshalJSON re-defaults this, but be safe
		{"nonsense", "198.18.0.1/15"}, // unparseable falls back to the old constant
	}
	for _, tc := range cases {
		t.Run(tc.netmask, func(t *testing.T) {
			cfg := linuxCfg()
			cfg.TunNetmask = tc.netmask
			r := &recordingRunner{}
			if err := configureLinux(context.Background(), cfg, r); err != nil {
				t.Fatalf("configureLinux() = %v", err)
			}
			want := "ip addr replace " + tc.want + " dev webfilter-tun"
			var found bool
			for _, cmd := range r.cmds {
				if cmd == want {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q among %v", want, r.cmds)
			}
		})
	}
}

// bypass_cidrs was accepted and validated by every previous version and
// applied by none of them, so an empty list and a populated one both need
// pinning.
func TestConfigureLinuxAppliesBypassCIDRs(t *testing.T) {
	cfg := linuxCfg()
	cfg.BypassCIDRs = []string{"192.168.12.0/24"}
	r := &recordingRunner{}
	if err := configureLinux(context.Background(), cfg, r); err != nil {
		t.Fatalf("configureLinux() = %v", err)
	}
	wantCmds(t, r.after(len(wantTeardown)), []string{
		"ip tuntap add mode tun dev webfilter-tun",
		"ip addr replace 198.18.0.1/15 dev webfilter-tun",
		"ip link set dev webfilter-tun up",
		"ip route replace default via 198.18.0.1 dev webfilter-tun table 8888",
		"ip route replace throw 192.168.12.0/24 table 8888",
		"ip rule add pref 9000 fwmark 0x5745 lookup main",
		"ip rule add pref 9100 lookup 8888",
	})

	cfg.BypassCIDRs = nil
	r2 := &recordingRunner{}
	if err := configureLinux(context.Background(), cfg, r2); err != nil {
		t.Fatalf("configureLinux() with no bypass CIDRs = %v", err)
	}
	for _, cmd := range r2.cmds {
		if strings.Contains(cmd, "throw") {
			t.Errorf("emitted a throw route for an empty bypass list: %s", cmd)
		}
	}
}

func TestUnconfigureLinuxIsTheInverse(t *testing.T) {
	r := &recordingRunner{}
	unconfigureLinux(context.Background(), linuxCfg(), r)
	wantCmds(t, r.cmds, wantTeardown)
}

// Teardown runs on the shutdown path, where most commands fail for the
// ordinary reason that the state is already gone. It must push through.
func TestUnconfigureLinuxIgnoresFailures(t *testing.T) {
	r := &recordingRunner{fail: map[string]error{
		"ip rule del pref 9100":     context.DeadlineExceeded,
		"ip route flush table 8888": context.DeadlineExceeded,
		"ip link del webfilter-tun": context.DeadlineExceeded,
	}}
	unconfigureLinux(context.Background(), linuxCfg(), r)
	wantCmds(t, r.cmds, wantTeardown)
}

// A configure that dies partway must not leave a half-built device behind, so
// the error has to propagate rather than be swallowed the way the old
// `_ = runner.Run(... tuntap add ...)` did.
func TestConfigureLinuxPropagatesFailure(t *testing.T) {
	r := &recordingRunner{fail: map[string]error{
		"ip link set dev webfilter-tun up": context.DeadlineExceeded,
	}}
	if err := configureLinux(context.Background(), linuxCfg(), r); err == nil {
		t.Fatal("configureLinux() = nil, want the underlying `ip` failure")
	}
	for _, cmd := range r.cmds {
		if strings.HasPrefix(cmd, "ip rule add") {
			t.Errorf("installed a routing rule after an earlier step failed: %s", cmd)
		}
	}
}
