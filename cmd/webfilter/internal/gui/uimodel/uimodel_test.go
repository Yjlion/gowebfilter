package uimodel

import (
	"errors"
	"testing"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
	"github.com/yjlion/gowebfilter/internal/models"
)

func TestSettingsFormRoundTripPreservesHiddenFields(t *testing.T) {
	base := models.NewGlobalSettings()
	base.CertDir = "/srv/certs"          // not exposed by the form
	base.PasswordHash = "$scrypt$secret" // ditto
	base.MgmtHostname = "filter.lan"

	form := LoadSettingsForm(base)
	form.MgmtPort = "9000"
	form.LogRetentionDays = "14"
	form.ProxyListen = "0.0.0.0:8080\nsocks5@127.0.0.1:1080"

	out, err := form.Apply(base)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.MgmtPort != 9000 || out.LogRetentionDays != 14 {
		t.Errorf("parsed fields wrong: port=%d retention=%d", out.MgmtPort, out.LogRetentionDays)
	}
	if len(out.ProxyListen) != 2 || out.ProxyListen[1] != "socks5@127.0.0.1:1080" {
		t.Errorf("proxy_listen = %v", out.ProxyListen)
	}
	if out.CertDir != "/srv/certs" || out.PasswordHash != "$scrypt$secret" || out.MgmtHostname != "filter.lan" {
		t.Errorf("hidden fields disturbed: %+v", out)
	}
}

// TestSettingsFormAdvancedFields covers the Advanced tab's fields: proxy
// auth and the full tun2socks config must survive a Load -> Apply round trip
// (the password never travels through the merged document — it goes to the
// server as new_proxy_auth_password separately).
func TestSettingsFormAdvancedFields(t *testing.T) {
	base := models.NewGlobalSettings()
	base.ProxyAuthEnabled = true
	base.ProxyAuthUsername = "proxyuser"
	base.ProxyAuthPasswordHash = "$scrypt$proxysecret" // not exposed by the form
	base.Tun2Socks.DeviceName = "custom-tun"
	base.Tun2Socks.TunAddress = "198.18.5.1"

	form := LoadSettingsForm(base)
	if !form.ProxyAuthEnabled || form.ProxyAuthUsername != "proxyuser" {
		t.Fatalf("proxy auth not loaded: %+v", form)
	}
	if form.NewProxyAuthPassword != "" {
		t.Fatalf("password material must never load into the form")
	}
	if form.Tun2SocksDeviceName != "custom-tun" || form.Tun2SocksTunAddress != "198.18.5.1" {
		t.Fatalf("tun device fields not loaded: %+v", form)
	}

	form.ProxyAuthUsername = " renamed "
	form.Tun2SocksInterfaceName = "eth0"
	form.Tun2SocksTunGateway = "198.18.5.2"
	form.Tun2SocksTunNetmask = "255.255.0.0"

	out, err := form.Apply(base)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ProxyAuthUsername != "renamed" || !out.ProxyAuthEnabled {
		t.Errorf("proxy auth after apply: enabled=%v username=%q", out.ProxyAuthEnabled, out.ProxyAuthUsername)
	}
	if out.ProxyAuthPasswordHash != "$scrypt$proxysecret" {
		t.Errorf("stored hash disturbed by Apply")
	}
	if out.Tun2Socks.DeviceName != "custom-tun" || out.Tun2Socks.InterfaceName != "eth0" ||
		out.Tun2Socks.TunAddress != "198.18.5.1" || out.Tun2Socks.TunGateway != "198.18.5.2" ||
		out.Tun2Socks.TunNetmask != "255.255.0.0" {
		t.Errorf("tun2socks after apply: %+v", out.Tun2Socks)
	}
}

func TestSettingsFormValidation(t *testing.T) {
	base := models.NewGlobalSettings()
	cases := []struct {
		name   string
		mutate func(*SettingsForm)
	}{
		{"bad port", func(f *SettingsForm) { f.MgmtPort = "eight thousand" }},
		{"port out of range", func(f *SettingsForm) { f.MgmtPort = "70000" }},
		{"negative retention", func(f *SettingsForm) { f.LogRetentionDays = "-1" }},
		{"no listeners", func(f *SettingsForm) { f.ProxyListen = "  \n " }},
	}
	for _, tc := range cases {
		form := LoadSettingsForm(base)
		tc.mutate(&form)
		if _, err := form.Apply(base); err == nil {
			t.Errorf("%s: Apply succeeded, want error", tc.name)
		}
	}
}

func TestSplitLinesAcceptsCommasAndNewlines(t *testing.T) {
	got := SplitLines(" a.example ,\nb.example\n\n , ")
	if len(got) != 2 || got[0] != "a.example" || got[1] != "b.example" {
		t.Errorf("SplitLines = %v", got)
	}
	if out := SplitLines(""); len(out) != 0 {
		t.Errorf("SplitLines(\"\") = %v, want empty", out)
	}
}

func TestLogRowClipboardLine(t *testing.T) {
	r := LogRow{Time: "12:00:00", Client: "192.168.1.5", Action: "blocked", Target: "ads.example", Detail: "url_filter"}
	want := "12:00:00\t192.168.1.5\tblocked\tads.example\turl_filter"
	if got := r.ClipboardLine(); got != want {
		t.Errorf("ClipboardLine = %q, want %q", got, want)
	}
}

func TestDefaultPolicyProtections(t *testing.T) {
	if CanDeletePolicy("default") || CanRenamePolicy("default") {
		t.Errorf("default policy must not be deletable or renamable")
	}
	if !CanDeletePolicy("kids") || !CanRenamePolicy("kids") {
		t.Errorf("non-default policies must be deletable and renamable")
	}
	if err := ValidatePolicyName("  "); err == nil {
		t.Errorf("empty name accepted")
	}
	if err := ValidatePolicyName("../escape"); err == nil {
		t.Errorf("path traversal name accepted")
	}
	if err := ValidatePolicyName("kids"); err != nil {
		t.Errorf("ValidatePolicyName(kids) = %v", err)
	}
}

func TestPolicySummaries(t *testing.T) {
	p := models.NewPolicy()
	if got := PolicySourceSummary(p); got != "catch-all" {
		t.Errorf("empty sources = %q, want catch-all", got)
	}
	p.SourceIPs = []string{"10.0.0.1", "10.0.0.2"}
	p.SourceMACs = []string{"aa:bb:cc:dd:ee:ff"}
	if got := PolicySourceSummary(p); got != "2 IPs, 1 MAC" {
		t.Errorf("summary = %q", got)
	}
	p.Inactive = true
	p.Schedule.Enabled = true
	p.Schedule.ActiveWindows = []models.TimeWindow{{}}
	if got := PolicyChips(p); got != "inactive · scheduled" {
		t.Errorf("chips = %q", got)
	}
}

func TestLogPollerDedup(t *testing.T) {
	p := NewLogPoller("blocks", 100)

	batch1 := []map[string]any{
		{"ts": float64(1700000002), "domain": "b.example", "client_ip": "10.0.0.2", "reason": "url_filter"},
		{"ts": float64(1700000001), "domain": "a.example", "client_ip": "10.0.0.1", "reason": "url_filter"},
	}
	if !p.Apply(batch1) {
		t.Fatalf("first Apply reported no change")
	}
	if p.Apply(batch1) {
		t.Errorf("identical Apply reported change")
	}

	batch2 := append([]map[string]any{
		{"ts": float64(1700000003), "domain": "c.example", "client_ip": "10.0.0.3", "reason": "text_classifier"},
	}, batch1...)
	if !p.Apply(batch2) {
		t.Errorf("new row not detected")
	}

	rows := p.Rows()
	if len(rows) != 3 || rows[0].Target != "c.example" || rows[0].Action != "blocked" {
		t.Errorf("rows = %+v", rows)
	}

	// Kind switch clears state so the next Apply always refreshes.
	p.SetKind("requests")
	if len(p.Rows()) != 0 {
		t.Errorf("rows survived kind switch")
	}
	if !p.Apply(nil) {
		// nil batch after reset differs from cleared signature -> change.
		t.Errorf("post-switch Apply reported no change")
	}
}

func TestFormatLogRowPerKind(t *testing.T) {
	req := FormatLogRow("requests", map[string]any{
		"ts": float64(1700000000), "client_ip": "10.0.0.5", "method": "GET",
		"host": "example.com", "path": "/x", "action": "ok", "component": "",
	})
	if req.Target != "GET example.com/x" || req.Action != "ok" {
		t.Errorf("requests row = %+v", req)
	}

	blk := FormatLogRow("blocks", map[string]any{
		"ts": float64(1700000000), "client_ip": "10.0.0.5",
		"domain": "bad.example", "reason": "keyword", "component": "url_filter",
	})
	if blk.Target != "bad.example" || blk.Detail != "keyword (url_filter)" {
		t.Errorf("blocks row = %+v", blk)
	}

	pc := FormatLogRow("policy_changes", map[string]any{
		"ts": float64(1700000000), "client_ip": "10.0.0.5",
		"action": "updated", "policy_name": "kids", "old_name": "children",
	})
	if pc.Target != "kids" || pc.Detail != "renamed from children" {
		t.Errorf("policy_changes row = %+v", pc)
	}

	if !blk.MatchesFilter("BAD.example") || blk.MatchesFilter("nomatch-xyz") {
		t.Errorf("MatchesFilter misbehaves")
	}
	if !blk.MatchesFilter("") {
		t.Errorf("empty filter must match")
	}
}

func TestStatusModel(t *testing.T) {
	var m StatusModel
	if got := m.RunningLabel(); got != "Connecting..." {
		t.Errorf("initial label = %q", got)
	}
	m.Set(mgmtclient.Status{
		ProxyRunning: true,
		ProxyListen:  []string{"0.0.0.0:8080"},
		MgmtPort:     8000,
		Tun2Socks:    map[string]any{"enabled": false},
	})
	if got := m.RunningLabel(); got != "Proxy running" {
		t.Errorf("label = %q", got)
	}
	if got := m.Tun2SocksLabel(); got != "tun2socks: disabled" {
		t.Errorf("tun label = %q", got)
	}
	m.SetError(errors.New("connection refused"))
	if got := m.ErrorLabel(); got == "" {
		t.Errorf("error not surfaced")
	}
	// Previous data must survive a failed poll.
	if got := m.RunningLabel(); got != "Proxy running" {
		t.Errorf("stale data lost on error: %q", got)
	}
}
