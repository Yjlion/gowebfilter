package gui

import (
	"sync"

	"github.com/gogpu/ui/core/checkbox"
	"github.com/gogpu/ui/core/collapsible"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/uimodel"
	"github.com/yjlion/gowebfilter/internal/models"
)

// settingsScreen edits GlobalSettings through string/bool signals mirroring
// uimodel.SettingsForm; unlike policies the widget tree is static, so the
// signals are created once and (re)filled on every reload.
type settingsScreen struct {
	u *ui

	mu   sync.Mutex
	base models.GlobalSettings // last fetched full settings (merge base)

	proxyListen  state.Signal[string]
	mgmtHost     state.Signal[string]
	mgmtPort     state.Signal[string]
	uiLanguage   state.Signal[string]
	logBlocks    state.Signal[bool]
	logRequests  state.Signal[bool]
	logRetention state.Signal[string]
	authEnabled  state.Signal[bool]
	newPassword  state.Signal[string]
	upstream     state.Signal[string]
	upstreamAuth state.Signal[string]
	pacProxyHost state.Signal[string]
	pacHosts     state.Signal[string]
	pacIPs       state.Signal[string]
	disableTray  state.Signal[bool]
	tunEnabled   state.Signal[bool]
	tunTarget    state.Signal[string]
	tunDNS       state.Signal[string]
	tunRoutes    state.Signal[bool]
	tunBypass    state.Signal[string]

	saveErr state.Signal[string]
	saveMsg state.Signal[string]
	locked  state.Signal[bool]
}

func newSettingsScreen(u *ui) *settingsScreen {
	return &settingsScreen{
		u:            u,
		proxyListen:  state.NewSignal(""),
		mgmtHost:     state.NewSignal(""),
		mgmtPort:     state.NewSignal(""),
		uiLanguage:   state.NewSignal(""),
		logBlocks:    state.NewSignal(false),
		logRequests:  state.NewSignal(false),
		logRetention: state.NewSignal(""),
		authEnabled:  state.NewSignal(false),
		newPassword:  state.NewSignal(""),
		upstream:     state.NewSignal(""),
		upstreamAuth: state.NewSignal(""),
		pacProxyHost: state.NewSignal(""),
		pacHosts:     state.NewSignal(""),
		pacIPs:       state.NewSignal(""),
		disableTray:  state.NewSignal(false),
		tunEnabled:   state.NewSignal(false),
		tunTarget:    state.NewSignal(""),
		tunDNS:       state.NewSignal(""),
		tunRoutes:    state.NewSignal(false),
		tunBypass:    state.NewSignal(""),
		saveErr:      state.NewSignal(""),
		saveMsg:      state.NewSignal(""),
		locked:       state.NewSignal(false),
	}
}

// reload fetches settings and refills the form signals.
func (s *settingsScreen) reload() {
	cur, err := s.u.opts.Client.Settings()
	if err != nil {
		if !s.u.handleAuthErr(err) {
			s.saveErr.Set(err.Error())
		}
		s.u.redraw()
		return
	}
	s.mu.Lock()
	s.base = cur
	s.mu.Unlock()

	f := uimodel.LoadSettingsForm(cur)
	s.proxyListen.Set(f.ProxyListen)
	s.mgmtHost.Set(f.MgmtHost)
	s.mgmtPort.Set(f.MgmtPort)
	s.uiLanguage.Set(f.UILanguage)
	s.logBlocks.Set(f.LogBlocks)
	s.logRequests.Set(f.LogRequests)
	s.logRetention.Set(f.LogRetentionDays)
	s.authEnabled.Set(f.AuthEnabled)
	s.newPassword.Set("")
	s.upstream.Set(f.UpstreamProxy)
	s.upstreamAuth.Set(f.UpstreamAuth)
	s.pacProxyHost.Set(f.PacProxyHost)
	s.pacHosts.Set(f.PacDirectHosts)
	s.pacIPs.Set(f.PacDirectIPs)
	s.disableTray.Set(f.DisableTray)
	s.tunEnabled.Set(f.Tun2SocksEnabled)
	s.tunTarget.Set(f.Tun2SocksProxyTarget)
	s.tunDNS.Set(f.Tun2SocksDNSServers)
	s.tunRoutes.Set(f.Tun2SocksAutoRoutes)
	s.tunBypass.Set(f.Tun2SocksBypassCIDRs)
	s.saveErr.Set("")
	s.u.redraw()
}

func (s *settingsScreen) form() uimodel.SettingsForm {
	return uimodel.SettingsForm{
		ProxyListen:          s.proxyListen.Get(),
		MgmtHost:             s.mgmtHost.Get(),
		MgmtPort:             s.mgmtPort.Get(),
		UILanguage:           s.uiLanguage.Get(),
		LogBlocks:            s.logBlocks.Get(),
		LogRequests:          s.logRequests.Get(),
		LogRetentionDays:     s.logRetention.Get(),
		AuthEnabled:          s.authEnabled.Get(),
		NewPassword:          s.newPassword.Get(),
		UpstreamProxy:        s.upstream.Get(),
		UpstreamAuth:         s.upstreamAuth.Get(),
		PacProxyHost:         s.pacProxyHost.Get(),
		PacDirectHosts:       s.pacHosts.Get(),
		PacDirectIPs:         s.pacIPs.Get(),
		DisableTray:          s.disableTray.Get(),
		Tun2SocksEnabled:     s.tunEnabled.Get(),
		Tun2SocksProxyTarget: s.tunTarget.Get(),
		Tun2SocksDNSServers:  s.tunDNS.Get(),
		Tun2SocksAutoRoutes:  s.tunRoutes.Get(),
		Tun2SocksBypassCIDRs: s.tunBypass.Get(),
	}
}

func (s *settingsScreen) save() {
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()

	merged, err := s.form().Apply(base)
	if err != nil {
		s.saveErr.Set(err.Error())
		s.saveMsg.Set("")
		s.u.redraw()
		return
	}
	newPassword := s.newPassword.Get()

	go func() {
		saved, err := s.u.opts.Client.UpdateSettings(merged, newPassword)
		if err != nil {
			if !s.u.handleAuthErr(err) {
				if isManagedLocked(err) {
					s.locked.Set(true)
					s.saveErr.Set("Settings are managed by your organization and cannot be changed here.")
				} else {
					s.saveErr.Set(err.Error())
				}
			}
			s.saveMsg.Set("")
			s.u.redraw()
			return
		}
		s.mu.Lock()
		s.base = saved
		s.mu.Unlock()
		s.newPassword.Set("")
		s.saveErr.Set("")
		s.saveMsg.Set("Saved.")
		s.u.restartNeeded.Set(true) // settings need an engine restart, always
		s.u.redraw()
	}()
}

func (s *settingsScreen) build() widget.Widget {
	tf := func(sig state.Signal[string], placeholder string) widget.Widget {
		return textfield.New(
			textfield.ValueSignal(sig),
			textfield.Placeholder(placeholder),
			textfield.PainterOpt(material3.TextFieldPainter{Theme: s.u.m3}),
			textfield.DisabledFn(s.locked.Get),
		)
	}
	cb := func(label string, sig state.Signal[bool]) widget.Widget {
		return checkbox.New(
			checkbox.LabelOpt(label),
			checkbox.CheckedSignal(sig),
			checkbox.PainterOpt(material3.CheckboxPainter{Theme: s.u.m3}),
			checkbox.DisabledFn(s.locked.Get),
		)
	}

	tunSection := collapsible.New(
		collapsible.Title("tun2socks (whole-OS capture)"),
		collapsible.Content(primitives.VBox(
			cb("Enabled", s.tunEnabled),
			fieldLabel("Proxy target (empty = local SOCKS5 listener)"), tf(s.tunTarget, "127.0.0.1:1080"),
			fieldLabel("DNS servers (comma-separated)"), tf(s.tunDNS, "1.1.1.3"),
			cb("Install routes automatically", s.tunRoutes),
			fieldLabel("Bypass CIDRs"), tf(s.tunBypass, "192.168.0.0/16"),
		).Gap(6).Padding(8)),
		collapsible.Expanded(false),
	)

	form := primitives.VBox(
		errorText(func() string {
			if s.locked.Get() {
				return "Settings are managed by your organization (read-only)."
			}
			return ""
		}),
		noticeText(func() string {
			if s.u.restartNeeded.Get() {
				return "Saved settings take effect after an engine restart (see Dashboard)."
			}
			return ""
		}),

		sectionTitle("Listeners"),
		fieldLabel("Proxy listeners (comma-separated: host:port, regular@/socks4@/socks5@host:port; TLS: https@/tls@host:port)"),
		tf(s.proxyListen, "0.0.0.0:8080"),

		sectionTitle("Management API"),
		fieldLabel("Host (empty = all interfaces)"), tf(s.mgmtHost, "0.0.0.0"),
		fieldLabel("Port"), tf(s.mgmtPort, "8000"),
		fieldLabel("UI language (e.g. en, de)"), tf(s.uiLanguage, "en"),
		cb("Disable system tray on Windows `run`", s.disableTray),

		sectionTitle("Logging"),
		cb("Log blocked requests", s.logBlocks),
		cb("Log all requests", s.logRequests),
		fieldLabel("Retention (days)"), tf(s.logRetention, "30"),

		sectionTitle("Authentication"),
		cb("Require password for the management UI", s.authEnabled),
		fieldLabel("New password (leave empty to keep current)"), tf(s.newPassword, ""),

		sectionTitle("Upstream proxy"),
		fieldLabel("Upstream proxy (host:port, empty = direct)"), tf(s.upstream, ""),
		fieldLabel("Upstream auth (user:pass)"), tf(s.upstreamAuth, ""),

		sectionTitle("PAC / WPAD"),
		fieldLabel("Proxy host advertised in proxy.pac"), tf(s.pacProxyHost, ""),
		fieldLabel("Direct hosts"), tf(s.pacHosts, "*.lan"),
		fieldLabel("Direct IPs/CIDRs"), tf(s.pacIPs, "192.168.0.0/16"),

		tunSection,

		primitives.HBox(
			s.u.btn("Save", s.save),
			s.u.btnOutlined("Reload", func() { go s.reload() }),
			errorText(s.saveErr.Get),
			noticeText(s.saveMsg.Get),
		).Gap(8).CrossAlign(primitives.CrossAxisCenter),

		fieldLabel("Certificates, categories, analytics, backup and tools live in the Web UI."),
	).Padding(16).Gap(8).MaxWidthValue(760)

	return newScrollBox(form)
}
