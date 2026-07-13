package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gogpu/systray"
	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/config"
)

func newTrayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tray",
		Short: "Run an optional desktop system tray controller",
	}
	f := addConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runTray(f.settingsPath)
	}
	return cmd
}

func runTray(settingsPath string) error {
	hideOwnConsoleWindow()

	if err := config.BootstrapRuntimeFiles(settingsPath); err != nil {
		return err
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return err
	}
	mgmtAddr := net.JoinHostPort(loopbackHost(settings.MgmtHost), fmt.Sprint(settings.MgmtPort))
	mgmtURL := "http://" + mgmtAddr

	tray := systray.New()
	menu := systray.NewMenu()
	menu.Add("Open Management UI", func() { _ = openTarget(mgmtURL) })
	// The native UI runs as a separate process so closing its window never
	// takes down the tray-hosted engine; it finds the tray's mgmt port
	// reachable and attaches over loopback HTTP.
	menu.Add("Open Native UI", func() { _ = launchSelf("gui", "--settings", settingsPath) })

	// If nothing is already listening on the mgmt port - no Windows service
	// running, no separately-started `webfilter run`/`mgmt` - "Open
	// Management UI" would just open a dead link, and the tray would be
	// pure chrome around nothing. So the tray runs the proxy + management
	// server itself in-process in that case, same as `webfilter run`.
	// Skipped when something's already there so the tray doesn't fight an
	// existing service/process for the ports. Never cancelled: the tray has
	// no quit menu item, so the embedded server runs for the tray's
	// lifetime.
	if !mgmtReachable(mgmtAddr) {
		ctx := context.Background()
		go func() {
			if err := runProxyAndMgmt(ctx, settingsPath); err != nil {
				tray.ShowNotification("WebFilter Proxy failed to start", err.Error())
			}
		}()
	}

	tray.SetIcon(defaultTrayIcon()).
		SetTooltip("WebFilter Proxy").
		SetMenu(menu).
		OnClick(func() { _ = openTarget(mgmtURL) }).
		Show()
	return tray.Run()
}

// mgmtReachable does a quick TCP dial to see whether something (a Windows
// service, or a separately-running `webfilter run`/`mgmt`) is already
// serving the management API, so the tray knows whether it needs to start
// its own copy of the proxy + management server.
func mgmtReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func loopbackHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return host
	}
}

// launchSelf spawns this same binary with the given arguments, detached -
// used by the tray to open the native GUI as an independent process.
func launchSelf(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return exec.Command(exe, args...).Start()
}

func openTarget(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// defaultTrayIcon is a 64x64 globe (blue disc with meridian/parallel grid
// lines), matching the "web filter proxy" branding better than an arbitrary
// solid color block. Regenerate with scripts/gen_tray_icon.go if it needs
// tweaking.
func defaultTrayIcon() []byte {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAYAAACqaXHeAAAFSUlEQVR4nORbXWxURRSebSEkCuk+KKniT8NqjbGxaxASEwJrbE3qi2vxoW+0sS9EG0laX4ASAtYXa1JjlBdL17e+sF1ekGQ1rAkYQyHdJQbjwiYVUtlgSG5FjErMJWfpvcydnZk7M3fuz+5+T/tz75xzvnPmzN+ZNtTiaHkC1vktoLIt31E6Fk/OJ46nqufmkvfu3IrfvVFK/v/vX3H4/+cv3q491zvxvfHo073F9Zs2G527R4pDlX2F3kmjmLjUv+qnfjE/Gn3v4Mln0eFq+vJ03/DfN39JWkaS6Bk75fjOem70v6/2o6OdudmpPb/p1lUrAdnTi7tnbu7Yf3tpIY0bA4aS33ngvbvw7cXU4Fvbf9ClsxYCwPBPy11HfvzwsRT+u2WojPEWaO+QERKLxTzr76kBCPWrW9+doXncgorxbu/iv48eynZ56RrKBIwnynvz6eszpek34jQlSUVljRdpA/9PNRqUhsH+k1vmzgxcyfCMx6FqvEy7pmmaKu1LEQBD2vapX5d+P/vlsJt3WRndC2htkiSAjjJtChMADQ8NPlMghzU373rxvoqMrRf7DBkShAiQNd4P77u1rUqCEAEqnhd9RhSy8oAEkXZdCYCEJ2O8n94XkSGbGLkEwFAHCY8lgAed3vciG2zgPcskACY5MM4jCa8G4X1ZWdPXns/U1iYMMCcPu2ZjC6wZXpCGyoI1Y2RNlKg/wtx+8vRKgWyEJYgU6Ef4i8jhOYa1iKLuB8DCBqEVLVPZIMFzyjsDrxZoDq/LAeD9P6+dp67qeAijW8jKBNvI3+oiANbzCC24CmKREkSkkHsEFtwIeWAbcnQDR0hAtvxptX0ZufTnRkmCFnjLZ2cEHK6m0fgWT0KCyhUy8hwRAzZOoc+t/xw54PJ037B+VaMF0kabAFg8wJQXKYR4mF1CVLYVKRcOdCfxhZJNQOlYPMl6SRRBDpVeZOG22gTMJ46nNOjVEMBttQmonpuri4BmBW6rPQrcu3MrjtB5qYbI/hdWLpAdgR7Y+njts03A3RslagTIGBX0dFnGAbhuuK32ROilD3ImrxGRTZCwCFDZoLFWh+tYDzfC4kcErBWjaZomkNBG/tFKABJsAprF4zKACLC7QPuGjUbP2Km4FQlkRDQqQW45wE6Cr838cdbaB8CTSyONAjyQttQlwfWbNhvkPgCSzLBh5hEZ8nefaMtZn20COneOFG8vobSK0CgPgzSArQidqH22k+BQZV9Bt4JRBW6rTUDvpFEMTaOAgdtqE5C41L/6yBMvOkiI8r6AF93wyjPHjtDLE99lUMT3AbzKrlWcYXBuix/tzOlRK8IgbKw7KCCPxJphVxhhOpNHZHUEiByL0QRFYVfYzTG04zHq2aA1K5QxKsiVpGrRJe2AlHo8/lH38hFWIyyEkQh1yKQejkKY7JqN5XrGHuaCRlgc8RwldTyO1o7JFv/pKNJqARslCSrXB1gYT5T3nhm4klHpc35FiEqtEq+KlFsj9Fml+5snX38/E8VwF9XJrYTWtUosv2dlBJ8ih1kv5IdsoTrB+ez11I5PykIkBFUfwIJsAbUQAbB4kCFB5hmdUKkeF64VliHBjyhwS66qpfNS1eJAwuLBF155M/tUhibYTTFVyMiQvTegdF8AEuPEc1eHeQoGVSnq9dKE8r1BGCJHD2W7aIqQ8BIFou2q3hjRdmlqrQ6vhiDvDHm9OKX12hxZnd30t8ZYgHXE1x8PLlvfvdwbhFHnwoHuh8fZGozG4QsBOCrb8h2syws4Ee0bNhr4wguHbqMjBXMNYevRsrgfAAD//00m8yVeboPdAAAAAElFTkSuQmCC")
	return data
}
