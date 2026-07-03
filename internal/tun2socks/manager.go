package tun2socks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"strings"

	tunengine "github.com/xjasonlyu/tun2socks/v2/engine"

	"github.com/yjlion/gowebfilter/internal/models"
)

type Manager struct {
	settings models.GlobalSettings
	run      commandRunner
}

type StartupSkippedError struct {
	Reason string
}

func (e StartupSkippedError) Error() string {
	return e.Reason
}

func IsStartupSkipped(err error) bool {
	var skipped StartupSkippedError
	return errors.As(err, &skipped)
}

func NewManager(settings models.GlobalSettings) *Manager {
	return &Manager{settings: settings, run: osCommandRunner{}}
}

func NewManagerWithRunner(settings models.GlobalSettings, runner commandRunner) *Manager {
	if runner == nil {
		runner = osCommandRunner{}
	}
	return &Manager{settings: settings, run: runner}
}

func (m *Manager) Start(ctx context.Context) error {
	cfg := m.settings.Tun2Socks
	if !cfg.Enabled {
		return nil
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return StartupSkippedError{Reason: fmt.Sprintf("tun2socks is not supported on %s yet", runtime.GOOS)}
	}
	if ok, detail := hasRoutePrivileges(); !ok {
		return StartupSkippedError{Reason: fmt.Sprintf("tun2socks disabled for this run because administrator/root privileges are unavailable: %s", detail)}
	}
	if err := checkPlatformPrerequisites(); err != nil {
		return StartupSkippedError{Reason: fmt.Sprintf("tun2socks disabled for this run because a platform prerequisite is missing: %v", err)}
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	proxy, err := selectProxy(m.settings)
	if err != nil {
		return err
	}
	interfaceName := cfg.InterfaceName
	if interfaceName == "" {
		interfaceName = proxy.InterfaceName
	}
	if cfg.AutoRoutes && runtime.GOOS == "linux" {
		if err := configurePlatform(ctx, cfg, m.run); err != nil {
			return err
		}
	}

	key := &tunengine.Key{
		Device:    deviceName(cfg),
		Proxy:     proxy.URL,
		Interface: interfaceName,
		LogLevel:  "info",
	}
	tunengine.Insert(key)
	go func() {
		<-ctx.Done()
		tunengine.Stop()
	}()
	slog.Info("starting tun2socks", "device", key.Device, "proxy", key.Proxy, "interface", key.Interface)
	tunengine.Start()
	if cfg.AutoRoutes && runtime.GOOS == "windows" {
		if err := configurePlatform(ctx, cfg, m.run); err != nil {
			tunengine.Stop()
			return err
		}
	}
	return nil
}

func ValidateConfig(cfg models.Tun2SocksConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.DeviceName) == "" {
		return errors.New("tun2socks device_name is required")
	}
	if cfg.ProxyTarget != "" {
		hostPort := cfg.ProxyTarget
		if i := strings.Index(hostPort, "://"); i >= 0 {
			hostPort = hostPort[i+3:]
		}
		host, port, err := net.SplitHostPort(hostPort)
		if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return fmt.Errorf("tun2socks proxy_target must be host:port or scheme://host:port")
		}
	}
	if net.ParseIP(cfg.TunAddress) == nil {
		return fmt.Errorf("tun2socks tun_address must be an IP address")
	}
	if net.ParseIP(cfg.TunGateway) == nil {
		return fmt.Errorf("tun2socks tun_gateway must be an IP address")
	}
	for _, dns := range cfg.DNSServers {
		if net.ParseIP(dns) == nil {
			return fmt.Errorf("tun2socks dns_servers contains invalid IP %q", dns)
		}
	}
	for _, cidr := range cfg.BypassCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("tun2socks bypass_cidrs contains invalid CIDR %q", cidr)
		}
	}
	return nil
}

type proxySelection struct {
	URL           string
	InterfaceName string
}

func proxyURL(settings models.GlobalSettings) (string, error) {
	proxy, err := selectProxy(settings)
	if err != nil {
		return "", err
	}
	return proxy.URL, nil
}

func selectProxy(settings models.GlobalSettings) (proxySelection, error) {
	cfg := settings.Tun2Socks
	if cfg.ProxyTarget != "" {
		if strings.Contains(cfg.ProxyTarget, "://") {
			if !isLoopbackTarget(cfg.ProxyTarget) || strings.HasPrefix(strings.ToLower(cfg.ProxyTarget), "socks5://") {
				return proxySelection{URL: cfg.ProxyTarget, InterfaceName: cfg.InterfaceName}, nil
			}
		} else if !isLoopbackTarget(cfg.ProxyTarget) {
			return proxySelection{URL: "socks5://" + cfg.ProxyTarget, InterfaceName: cfg.InterfaceName}, nil
		}
	}
	if port := settings.PrimarySocks5Port(); port != 0 {
		return proxySelection{URL: "socks5://" + net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), InterfaceName: cfg.InterfaceName}, nil
	}
	iface, host, err := nonLoopbackInterfaceIP(cfg.InterfaceName)
	if err != nil {
		return proxySelection{}, fmt.Errorf("tun2socks needs a non-loopback proxy target; set tun2socks.proxy_target to http://<this-host-lan-ip>:%d or set interface_name: %w", settings.PrimaryProxyPort(), err)
	}
	return proxySelection{
		URL:           "http://" + net.JoinHostPort(host, fmt.Sprint(settings.PrimaryProxyPort())),
		InterfaceName: iface,
	}, nil
}

func isLoopbackTarget(target string) bool {
	hostPort := target
	if i := strings.Index(hostPort, "://"); i >= 0 {
		hostPort = hostPort[i+3:]
	}
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return true
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip == nil || ip.IsLoopback()
}

func nonLoopbackInterfaceIP(name string) (string, string, error) {
	if strings.TrimSpace(name) != "" {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			return "", "", err
		}
		ip, err := firstInterfaceIPv4(*iface)
		return iface.Name, ip, err
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", "", err
	}
	if ifaceName, ip, err := defaultRouteInterfaceIP(ifaces); err == nil {
		return ifaceName, ip, nil
	}
	var fallbackIface string
	var fallbackIP string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if ip, err := firstInterfaceIPv4(iface); err == nil {
			if looksVirtualInterface(iface.Name) {
				if fallbackIP == "" {
					fallbackIface = iface.Name
					fallbackIP = ip
				}
				continue
			}
			return iface.Name, ip, nil
		}
	}
	if fallbackIP != "" {
		return fallbackIface, fallbackIP, nil
	}
	return "", "", errors.New("no up non-loopback IPv4 interface found")
}

func looksVirtualInterface(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{
		"virtualbox",
		"vmware",
		"hyper-v",
		"vethernet",
		"wsl",
		"docker",
		"container",
		"loopback",
		"bluetooth",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func firstInterfaceIPv4(iface net.Interface) (string, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("interface %s has no non-loopback IPv4 address", iface.Name)
}

func deviceName(cfg models.Tun2SocksConfig) string {
	if runtime.GOOS == "windows" {
		return "tun://" + cfg.DeviceName
	}
	return cfg.DeviceName
}
