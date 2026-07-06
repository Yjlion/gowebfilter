//go:build !android && !linux

package mobile

import "fmt"

// startTun is a no-op stub for non-Android/Linux hosts. The gomobile target
// is always android (linux kernel), so this branch exists only so the
// package's pure-Go logic (settings bootstrap, lifecycle) compiles and its
// unit tests run on a developer's macOS/Windows desktop. Attempting to
// actually start a TUN off-target is an error rather than a silent no-op.
func startTun(tunFd int) error {
	return fmt.Errorf("tun2socks TUN capture is only supported on android/linux (got a non-supported host build)")
}

func stopTun() {}
