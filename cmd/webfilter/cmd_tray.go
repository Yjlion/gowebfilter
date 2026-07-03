package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

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
	if err := config.BootstrapRuntimeFiles(settingsPath); err != nil {
		return err
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return err
	}
	mgmtURL := "http://" + net.JoinHostPort(loopbackHost(settings.MgmtHost), fmt.Sprint(settings.MgmtPort))
	settingsDir := filepath.Dir(settingsPath)

	tray := systray.New()
	menu := systray.NewMenu()
	menu.Add("Open Management UI", func() { _ = openTarget(mgmtURL) })
	menu.Add("Open Config Folder", func() { _ = openTarget(defaultPath(settingsDir)) })
	menu.Add("Open Certificates Folder", func() { _ = openTarget(defaultPath(settings.CertDir)) })
	menu.AddSeparator()
	addServiceItems(menu)
	menu.AddSeparator()
	menu.Add("Quit Tray", func() {
		tray.Remove()
		os.Exit(0)
	})

	tray.SetIcon(defaultTrayIcon()).
		SetTooltip("WebFilter Proxy").
		SetMenu(menu).
		OnClick(func() { _ = openTarget(mgmtURL) }).
		Show()
	return tray.Run()
}

func loopbackHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return host
	}
}

func defaultPath(path string) string {
	if path != "" {
		return path
	}
	wd, _ := os.Getwd()
	return wd
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

func defaultTrayIcon() []byte {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAIElEQVR42mP8z8Dwn4ECwESJ5lEDRg0YNWDUgFEDBgA1eQIfYay0UQAAAABJRU5ErkJggg==")
	return data
}
