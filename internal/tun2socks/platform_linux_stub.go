//go:build !linux

package tun2socks

import (
	"context"
	"fmt"

	"github.com/yjlion/gowebfilter/internal/models"
)

func configureLinux(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	return fmt.Errorf("linux route setup is unavailable on this platform")
}
