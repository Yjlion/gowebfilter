package tun2socks

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
)

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		cmdline := name + " " + strings.Join(args, " ")
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s: %w", cmdline, err)
		}
		return fmt.Errorf("%s: %w: %s", cmdline, err, msg)
	}
	return nil
}

func configurePlatform(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	switch runtime.GOOS {
	case "windows":
		return configureWindows(ctx, cfg, runner)
	case "linux":
		return configureLinux(ctx, cfg, runner)
	default:
		return fmt.Errorf("tun2socks route setup is not supported on %s", runtime.GOOS)
	}
}
