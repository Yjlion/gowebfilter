package gateway

import (
	"os"
	"os/exec"
)

// nftSbinPaths are where nft actually lives. It is an sbin tool, and plenty of
// contexts that will run this - a login shell without sbin on PATH, cron, a
// minimal container - cannot find it by name alone. systemd's default PATH
// does include /usr/sbin, so the service itself is fine; these fallbacks are
// what make `webfilter gateway status` and the packaging probes agree with it.
var nftSbinPaths = []string{"/usr/sbin/nft", "/sbin/nft", "/usr/local/sbin/nft"}

// nftPath resolves the nftables CLI, preferring PATH and falling back to the
// standard sbin locations. Returns "" when it is genuinely not installed.
func nftPath() string {
	if p, err := exec.LookPath("nft"); err == nil {
		return p
	}
	for _, p := range nftSbinPaths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return ""
}
