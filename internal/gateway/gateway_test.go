package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

type recordingRunner struct {
	cmds    []string
	stdin   []string
	fail    map[string]error
	failAll error
}

func (r *recordingRunner) Run(ctx context.Context, stdin, name string, args ...string) error {
	line := name + " " + strings.Join(args, " ")
	r.cmds = append(r.cmds, line)
	r.stdin = append(r.stdin, stdin)
	if r.failAll != nil {
		return r.failAll
	}
	return r.fail[line]
}

// fakeProcSys builds a /proc/sys tree with the given keys, so sysctl handling
// can be exercised without a kernel to damage.
func fakeProcSys(t *testing.T, values map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for key, v := range values {
		p := filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(key, ".", "/")))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readSysctl(t *testing.T, root, key string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(key, ".", "/"))))
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return strings.TrimSpace(string(b))
}

func enabledSettings() models.GlobalSettings {
	s := models.NewGlobalSettings()
	s.Gateway = models.NewGatewayConfig()
	s.Gateway.Enabled = true
	s.Gateway.Interface = "enp0s3"
	return s
}

// A disabled config must not touch the firewall or the kernel: gateway mode is
// off by default and the overwhelming majority of runs never want it.
func TestStartIsANoOpWhenDisabled(t *testing.T) {
	s := models.NewGlobalSettings()
	r := &recordingRunner{}
	m := NewManagerWithRunner(s, "0.0.0.0:40001", r, t.TempDir())
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	if len(r.cmds) != 0 {
		t.Errorf("ran commands with gateway disabled: %v", r.cmds)
	}
}

// The whole point of saving sysctls is that a host is left exactly as it was
// found - ip_forward in particular may have been deliberately off.
func TestSysctlsAreRestoredOnShutdown(t *testing.T) {
	root := fakeProcSys(t, map[string]string{
		"net.ipv4.ip_forward":                 "0",
		"net.ipv4.conf.all.send_redirects":    "1",
		"net.ipv4.conf.enp0s3.send_redirects": "1",
	})
	m := NewManagerWithRunner(enabledSettings(), "0.0.0.0:40001", &recordingRunner{}, root)

	if err := m.applySysctls(m.settings.Gateway); err != nil {
		t.Fatalf("applySysctls() = %v", err)
	}
	if got := readSysctl(t, root, "net.ipv4.ip_forward"); got != "1" {
		t.Errorf("ip_forward = %q, want 1", got)
	}
	// Without this the kernel tells each client to bypass the gateway, and the
	// client obeys - the classic "transparent proxy works, then stops".
	if got := readSysctl(t, root, "net.ipv4.conf.enp0s3.send_redirects"); got != "0" {
		t.Errorf("enp0s3.send_redirects = %q, want 0", got)
	}

	m.Shutdown()
	if got := readSysctl(t, root, "net.ipv4.ip_forward"); got != "0" {
		t.Errorf("ip_forward = %q after shutdown, want the original 0", got)
	}
	if got := readSysctl(t, root, "net.ipv4.conf.all.send_redirects"); got != "1" {
		t.Errorf("all.send_redirects = %q after shutdown, want the original 1", got)
	}
}

// A sysctl that was already at the wanted value must still be recorded, or a
// later restore would skip it - and a key that does not exist (an interface
// named in config but absent) must not fail the whole start.
func TestSysctlSaverHandlesNoopsAndMissingKeys(t *testing.T) {
	root := fakeProcSys(t, map[string]string{"net.ipv4.ip_forward": "1"})
	s := newSysctlSaver(sysctlRoot(root))

	if err := s.set("net.ipv4.ip_forward", "1"); err != nil {
		t.Fatalf("set() = %v", err)
	}
	if err := s.set("net.ipv4.conf.nosuchif.send_redirects", "0"); err != nil {
		t.Fatalf("set() on a missing key = %v, want it skipped", err)
	}
	if got := s.changed(); len(got) != 1 || got[0] != "net.ipv4.ip_forward" {
		t.Errorf("changed() = %v, want just ip_forward", got)
	}
}

func TestShutdownDeletesOnlyOurTable(t *testing.T) {
	r := &recordingRunner{}
	m := NewManagerWithRunner(enabledSettings(), "0.0.0.0:40001", r, t.TempDir())
	m.Shutdown()

	if len(r.cmds) != 1 || r.cmds[0] != "nft delete table ip webfilter" {
		t.Fatalf("Shutdown() ran %v, want exactly the webfilter table delete", r.cmds)
	}
}

// Shutdown runs on the stopping path where a missing table is the ordinary
// case, so it must push through rather than give up.
func TestShutdownIgnoresFailures(t *testing.T) {
	r := &recordingRunner{failAll: context.DeadlineExceeded}
	m := NewManagerWithRunner(enabledSettings(), "0.0.0.0:40001", r, t.TempDir())
	m.Shutdown() // must not panic or block
	if st := m.Status(); st.Active {
		t.Error("Status().Active is true after Shutdown")
	}
}

func TestPortOfAddr(t *testing.T) {
	if _, err := portOfAddr(""); err == nil {
		t.Error("portOfAddr(\"\") = nil error, want a failure")
	}
	if _, err := portOfAddr("0.0.0.0:0"); err == nil {
		t.Error("port 0 accepted; the rules would name a port nothing serves")
	}
	got, err := portOfAddr("0.0.0.0:40001")
	if err != nil || got != 40001 {
		t.Errorf("portOfAddr() = %d, %v; want 40001, nil", got, err)
	}
}
