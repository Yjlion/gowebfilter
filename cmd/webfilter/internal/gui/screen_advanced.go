package gui

import (
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/widget"
)

// advancedScreen holds the settings sections that most users never touch:
// proxy (HTTP Basic / SOCKS5) client authentication, the upstream proxy
// chain, and tun2socks whole-OS capture. It is a second view over the
// settingsScreen's signals and merge base — same form, same save/reload —
// so editing on either tab and saving on the other never loses anything.
type advancedScreen struct {
	u    *ui
	sets *settingsScreen
}

func newAdvancedScreen(u *ui, sets *settingsScreen) *advancedScreen {
	return &advancedScreen{u: u, sets: sets}
}

// reload delegates to the shared settings state.
func (s *advancedScreen) reload() { s.sets.reload() }

func (s *advancedScreen) build() widget.Widget {
	st := s.sets

	form := primitives.VBox(
		st.lockNotice(),
		st.restartNotice(),

		sectionTitle("Proxy Authentication"),
		fieldLabel("Require clients to present a username and password before using the proxy (HTTP Basic for regular listeners, username/password for SOCKS5)."),
		st.cb("Require proxy authentication", st.paEnabled),
		fieldLabel("Username"), st.tf(st.paUsername, "proxyuser"),
		fieldLabel("New password (leave empty to keep current)"), st.tfPassword(st.paNewPassword, ""),

		sectionTitle("Upstream proxy"),
		fieldLabel("Upstream proxy (host:port, empty = direct)"), st.tf(st.upstream, ""),
		fieldLabel("Upstream auth (user:pass)"), st.tf(st.upstreamAuth, ""),

		sectionTitle("tun2socks (whole-OS capture)"),
		st.cb("Enabled", st.tunEnabled),
		fieldLabel("Proxy target (empty = local SOCKS5 listener)"), st.tf(st.tunTarget, "127.0.0.1:1080"),
		fieldLabel("DNS servers (comma-separated)"), st.tf(st.tunDNS, "1.1.1.3"),
		st.cb("Install routes automatically", st.tunRoutes),
		fieldLabel("Bypass CIDRs"), st.tf(st.tunBypass, "192.168.0.0/16"),
		fieldLabel("TUN device name"), st.tf(st.tunDevice, "webfilter-tun"),
		fieldLabel("Bind interface (empty = default route)"), st.tf(st.tunIface, ""),
		fieldLabel("TUN address"), st.tf(st.tunAddr, "198.18.0.1"),
		fieldLabel("TUN gateway"), st.tf(st.tunGateway, "198.18.0.1"),
		fieldLabel("TUN netmask"), st.tf(st.tunNetmask, "255.254.0.0"),

		st.saveRow(),
	).Padding(16).Gap(8).MaxWidthValue(760)

	return newScrollBox(form)
}
