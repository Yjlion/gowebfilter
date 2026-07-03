package tun2socks

import (
	"runtime"

	"github.com/yjlion/gowebfilter/internal/models"
)

type Status struct {
	Configured       bool   `json:"configured"`
	Enabled          bool   `json:"enabled"`
	Running          bool   `json:"running"`
	Supported        bool   `json:"supported"`
	Platform         string `json:"platform"`
	DeviceName       string `json:"device_name"`
	InterfaceName    string `json:"interface_name"`
	ProxyTarget      string `json:"proxy_target"`
	AutoRoutes       bool   `json:"auto_routes"`
	Privilege        string `json:"privilege"`
	PrivilegeOK      bool   `json:"privilege_ok"`
	LastError        string `json:"last_error,omitempty"`
	RequiredProxy    string `json:"required_proxy"`
	RecommendedProxy string `json:"recommended_proxy"`
}

func Inspect(settings models.GlobalSettings) Status {
	cfg := settings.Tun2Socks
	privOK, priv := hasRoutePrivileges()
	supported := runtime.GOOS == "windows" || runtime.GOOS == "linux"
	proxy, proxyErr := proxyURL(settings)
	prereqErr := checkPlatformPrerequisites()
	status := Status{
		Configured:       true,
		Enabled:          cfg.Enabled,
		Supported:        supported,
		Platform:         runtime.GOOS,
		DeviceName:       cfg.DeviceName,
		InterfaceName:    cfg.InterfaceName,
		ProxyTarget:      cfg.ProxyTarget,
		AutoRoutes:       cfg.AutoRoutes,
		Privilege:        priv,
		PrivilegeOK:      privOK,
		RequiredProxy:    proxy,
		RecommendedProxy: "auto non-loopback HTTP proxy target",
	}
	if !supported {
		status.LastError = "tun2socks is only wired for Windows and Linux in this build."
	} else if cfg.Enabled && !privOK {
		status.LastError = "administrator/root privileges are required to configure the TUN device and routes."
	} else if cfg.Enabled && prereqErr != nil {
		status.LastError = prereqErr.Error()
	} else if cfg.Enabled && proxyErr != nil {
		status.LastError = proxyErr.Error()
	}
	return status
}
