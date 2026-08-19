package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Gateway mode has to change two kernel knobs, and both must be put back
// exactly as they were found. Changing a host's forwarding behaviour and then
// leaving it changed is the same class of bug as the TUN routing that used to
// outlive the process (see internal/tun2socks) - worse here, because
// ip_forward is a security-relevant setting that an operator may have
// deliberately turned off.
//
// The values are read before they are written, so restore puts back what was
// actually there rather than assuming a default.

// sysctlRoot is /proc/sys, overridable so tests never touch the real kernel.
type sysctlRoot string

const procSys sysctlRoot = "/proc/sys"

func (r sysctlRoot) path(key string) string {
	return filepath.Join(string(r), filepath.FromSlash(strings.ReplaceAll(key, ".", "/")))
}

func (r sysctlRoot) read(key string) (string, error) {
	b, err := os.ReadFile(r.path(key))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (r sysctlRoot) write(key, value string) error {
	return os.WriteFile(r.path(key), []byte(value+"\n"), 0o644)
}

// sysctlSaver applies sysctl changes and remembers the previous values.
type sysctlSaver struct {
	root  sysctlRoot
	prior map[string]string
	order []string
}

func newSysctlSaver(root sysctlRoot) *sysctlSaver {
	return &sysctlSaver{root: root, prior: map[string]string{}}
}

// set records the current value of key (once) and writes the new one. A key
// that does not exist is skipped rather than failing: send_redirects is
// per-interface and the interface may be named in config but absent.
func (s *sysctlSaver) set(key, value string) error {
	cur, err := s.root.read(key)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sysctl %s: %w", key, err)
	}
	if _, seen := s.prior[key]; !seen {
		s.prior[key] = cur
		s.order = append(s.order, key)
	}
	if cur == value {
		return nil
	}
	if err := s.root.write(key, value); err != nil {
		return fmt.Errorf("write sysctl %s=%s: %w", key, value, err)
	}
	return nil
}

// restore puts every touched key back, in reverse order, and reports the first
// failure without giving up on the rest.
func (s *sysctlSaver) restore() error {
	var firstErr error
	for i := len(s.order) - 1; i >= 0; i-- {
		key := s.order[i]
		if err := s.root.write(key, s.prior[key]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.order = nil
	s.prior = map[string]string{}
	return firstErr
}

// changed reports the keys this saver modified, for logging and status.
func (s *sysctlSaver) changed() []string { return append([]string(nil), s.order...) }
