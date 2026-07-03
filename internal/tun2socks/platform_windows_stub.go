//go:build !windows

package tun2socks

import (
	"context"
	"fmt"

	"github.com/yjlion/gowebfilter/internal/models"
)

func configureWindows(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	return fmt.Errorf("windows route setup is unavailable on this platform")
}
