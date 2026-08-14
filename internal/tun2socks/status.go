package tun2socks

import (
	"runtime"

	"github.com/yjlion/gowebfilter/internal/models"
)

// Status is what GET /api/status and GET /api/tun2socks/status report about
// TUN capture. It covers three separable questions the UI has to answer
// distinctly: is it configured, is the binary installed, and is it running.
type Status struct {
	Configured    bool   `json:"configured"`
	Enabled       bool   `json:"enabled"`
	Running       bool   `json:"running"`
	Supported     bool   `json:"supported"`
	Platform      string `json:"platform"`
	DeviceName    string `json:"device_name"`
	InterfaceName string `json:"interface_name"`
	AutoRoutes    bool   `json:"auto_routes"`
	Privilege     string `json:"privilege"`
	PrivilegeOK   bool   `json:"privilege_ok"`
	LastError     string `json:"last_error,omitempty"`

	// SocksAddr is the dedicated SOCKS5 listener captured traffic is funnelled
	// into. Read-only by design - see Supervisor.socksAddr.
	SocksAddr string `json:"socks_addr"`

	// Binary state. The external process is a prerequisite the user may have to
	// act on, so its absence is reported as data rather than an error string.
	BinaryPresent bool   `json:"binary_present"`
	BinaryPath    string `json:"binary_path,omitempty"`
	BinaryVersion string `json:"binary_version,omitempty"`
	BinarySource  string `json:"binary_source,omitempty"`
	BinarySHA256  string `json:"binary_sha256,omitempty"`
	Downloaded    string `json:"downloaded,omitempty"`

	// Live process state; zero unless this process is supervising tun2socks.
	PID      int `json:"pid,omitempty"`
	Restarts int `json:"restarts,omitempty"`
}

// Inspect reports the parts of Status that can be determined from settings and
// the filesystem alone, with no running supervisor. Standalone `webfilter mgmt`
// answers from here; `webfilter run` layers live process state on top via
// Supervisor.Status.
func Inspect(settings models.GlobalSettings) Status {
	cfg := settings.Tun2Socks
	privOK, priv := hasRoutePrivileges()
	supported := runtime.GOOS == "windows" || runtime.GOOS == "linux"
	prereqErr := checkPlatformPrerequisites()

	status := Status{
		Configured:    true,
		Enabled:       cfg.Enabled,
		Supported:     supported,
		Platform:      runtime.GOOS,
		DeviceName:    cfg.DeviceName,
		InterfaceName: cfg.InterfaceName,
		AutoRoutes:    cfg.AutoRoutes,
		Privilege:     priv,
		PrivilegeOK:   privOK,
	}

	if bin, source, err := Resolve(); err == nil {
		status.BinaryPresent = true
		status.BinaryPath = bin
		status.BinarySource = source
		status.BinaryVersion = Version(bin)
		if meta, ok := readInstallMeta(bin); ok {
			status.BinarySHA256 = meta.SHA256
			status.Downloaded = meta.Downloaded
			if status.BinaryVersion == "" {
				status.BinaryVersion = meta.Version
			}
		}
	}

	// Report the first blocking problem, most fundamental first, so the UI has
	// one actionable message rather than a list the user has to triage.
	switch {
	case !supported:
		status.LastError = "tun2socks is only wired for Windows and Linux in this build."
	case !status.BinaryPresent:
		status.LastError = "The tun2socks binary is not installed. Use Download tun2socks below, or run `webfilter tun2socks download`."
	case cfg.Enabled && !privOK:
		status.LastError = "Administrator (Windows) or root / CAP_NET_ADMIN (Linux) is required to configure the TUN device and routes. Under systemd, add AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW to the unit - see packaging/README.md."
	case cfg.Enabled && prereqErr != nil:
		status.LastError = prereqErr.Error()
	}
	return status
}
