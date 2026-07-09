//go:build ignore

// patch_libc_seccomp.go makes android/amd64 (x86_64 emulator) builds survive
// Android's app seccomp policy.
//
// modernc.org/sqlite (used by internal/logstore) runs on modernc.org/libc, a
// Go translation of musl. Every syscall musl makes funnels through the generic
// dispatchers X__syscall0..6 and ___syscall_cp in libc's syscall_musl.go,
// which pass the raw syscall number straight to the kernel. On x86_64 musl
// uses the *legacy* path-based numbers — open(2), stat(4), lstat(6),
// access(21), unlink(87), ... — and Android's per-app seccomp filter does not
// whitelist those on x86_64, so the first sqlite open dies with
// SIGSYS/SYS_SECCOMP. arm64 is unaffected: it never had the legacy numbers, so
// musl already uses the *at family there.
//
// (Patching libc's own Xlstat/Xstat/... is NOT enough — those hand-written Go
// functions aren't even compiled into the amd64 build; the musl C code reaches
// the kernel through X__syscallN with a hardcoded number instead. The fix has
// to sit at the dispatcher.)
//
// This script gives that fix a reproducible home:
//
//  1. copies the go.mod-pinned modernc.org/libc out of the module cache into
//     ./third_party/libc-seccomp (gitignored),
//  2. routes syscall_musl.go's dispatchers through a new seccompSyscall shim
//     and drops in that shim: on amd64 it rewrites each legacy path-based
//     syscall to its *at-family equivalent (openat, newfstatat, unlinkat, ...)
//     — the same calls bionic/glibc make, allowed by every seccomp policy and
//     by any kernel since 2.6.16 — and on every other musl arch it is a plain
//     passthrough, so arm64/etc. are untouched,
//  3. points go.mod at the patched copy with a replace directive.
//
// The replacements are exact-match and counted: if a libc upgrade reshuffles
// syscall_musl.go, the script fails loudly instead of silently half-patching.
//
// Usage:
//
//	go run scripts/patch_libc_seccomp.go        # apply (idempotent)
//	go run scripts/patch_libc_seccomp.go -undo  # drop the replace + copy
//
// Run it before `gomobile bind` whenever the target list includes
// android/amd64 (android/README.md documents the full build;
// .github/workflows/android.yml runs it in CI). Do not commit the go.mod
// replace line it adds — undo before committing.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	modulePath = "modernc.org/libc"
	patchDir   = "third_party/libc-seccomp"
	targetFile = "syscall_musl.go"
)

// syscallReplacements rewrites syscall_musl.go so every dispatcher routes
// through seccompSyscall (defined by the remap files below) instead of calling
// golang.org/x/sys/unix directly. Order-independent; each pattern must occur
// exactly once. The last entry drops the now-unused unix import.
var syscallReplacements = [][2]string{
	{ // ___syscall_cp (cancellation-point path: open, read, write, ...)
		`r1, _, err := (unix.Syscall6(uintptr(n), uintptr(a), uintptr(b), uintptr(c), uintptr(d), uintptr(e), uintptr(f)))`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a), uintptr(b), uintptr(c), uintptr(d), uintptr(e), uintptr(f))`,
	},
	{ // X__syscall0 default
		`r1, _, err := unix.Syscall(uintptr(n), 0, 0, 0)`,
		`r1, _, err := seccompSyscall(uintptr(n), 0, 0, 0, 0, 0, 0)`,
	},
	{ // X__syscall1
		`r1, _, err := unix.Syscall(uintptr(n), uintptr(a1), 0, 0)`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a1), 0, 0, 0, 0, 0)`,
	},
	{ // X__syscall2
		`r1, _, err := unix.Syscall(uintptr(n), uintptr(a1), uintptr(a2), 0)`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a1), uintptr(a2), 0, 0, 0, 0)`,
	},
	{ // X__syscall3
		`r1, _, err := unix.Syscall(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3))`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), 0, 0, 0)`,
	},
	{ // X__syscall4
		`r1, _, err := unix.Syscall6(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), uintptr(a4), 0, 0)`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), uintptr(a4), 0, 0)`,
	},
	{ // X__syscall5
		`r1, _, err := unix.Syscall6(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), uintptr(a4), uintptr(a5), 0)`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), uintptr(a4), uintptr(a5), 0)`,
	},
	{ // X__syscall6
		`r1, _, err := unix.Syscall6(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), uintptr(a4), uintptr(a5), uintptr(a6))`,
		`r1, _, err := seccompSyscall(uintptr(n), uintptr(a1), uintptr(a2), uintptr(a3), uintptr(a4), uintptr(a5), uintptr(a6))`,
	},
	{ // seccompSyscall lives in its own files now; unix is unused here.
		"import (\n\t\"golang.org/x/sys/unix\"\n\t\"runtime\"\n)",
		"import (\n\t\"runtime\"\n)",
	},
}

// remapAmd64File is the x86_64 shim: it rewrites the legacy path-based
// syscalls Android's seccomp policy kills into their *at-family equivalents
// before issuing them. Everything else (and every already-modern call) falls
// through unchanged.
const remapAmd64File = `// Code generated by scripts/patch_libc_seccomp.go. DO NOT EDIT.
//go:build linux && amd64

package libc

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// seccompSyscall issues syscall n, first rewriting the legacy path-based
// x86_64 syscalls (open, stat, lstat, access, unlink, ...) — which Android's
// per-app seccomp filter kills — into the *at-family syscalls the kernel and
// every seccomp policy allow. musl routes all of its syscalls through the
// X__syscallN dispatchers, so intercepting here covers the whole surface.
func seccompSyscall(n, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, syscall.Errno) {
	at := unix.AT_FDCWD // untyped const -100; convert via a variable
	cwd := uintptr(at)
	nofollow := uintptr(unix.AT_SYMLINK_NOFOLLOW)
	switch n {
	case unix.SYS_OPEN: // open(path, flags, mode)
		return unix.Syscall6(unix.SYS_OPENAT, cwd, a1, a2, a3, 0, 0)
	case unix.SYS_STAT: // stat(path, buf)
		return unix.Syscall6(unix.SYS_NEWFSTATAT, cwd, a1, a2, 0, 0, 0)
	case unix.SYS_LSTAT: // lstat(path, buf)
		return unix.Syscall6(unix.SYS_NEWFSTATAT, cwd, a1, a2, nofollow, 0, 0)
	case unix.SYS_ACCESS: // access(path, mode)
		return unix.Syscall6(unix.SYS_FACCESSAT, cwd, a1, a2, 0, 0, 0)
	case unix.SYS_RENAME: // rename(old, new)
		return unix.Syscall6(unix.SYS_RENAMEAT, cwd, a1, cwd, a2, 0, 0)
	case unix.SYS_MKDIR: // mkdir(path, mode)
		return unix.Syscall6(unix.SYS_MKDIRAT, cwd, a1, a2, 0, 0, 0)
	case unix.SYS_RMDIR: // rmdir(path)
		return unix.Syscall6(unix.SYS_UNLINKAT, cwd, a1, uintptr(unix.AT_REMOVEDIR), 0, 0, 0)
	case unix.SYS_LINK: // link(old, new)
		return unix.Syscall6(unix.SYS_LINKAT, cwd, a1, cwd, a2, 0, 0)
	case unix.SYS_UNLINK: // unlink(path)
		return unix.Syscall6(unix.SYS_UNLINKAT, cwd, a1, 0, 0, 0, 0)
	case unix.SYS_SYMLINK: // symlink(target, linkpath)
		return unix.Syscall6(unix.SYS_SYMLINKAT, a1, cwd, a2, 0, 0, 0)
	case unix.SYS_READLINK: // readlink(path, buf, bufsiz)
		return unix.Syscall6(unix.SYS_READLINKAT, cwd, a1, a2, a3, 0, 0)
	case unix.SYS_CHMOD: // chmod(path, mode)
		return unix.Syscall6(unix.SYS_FCHMODAT, cwd, a1, a2, 0, 0, 0)
	case unix.SYS_CHOWN: // chown(path, owner, group)
		return unix.Syscall6(unix.SYS_FCHOWNAT, cwd, a1, a2, a3, 0, 0)
	case unix.SYS_LCHOWN: // lchown(path, owner, group)
		return unix.Syscall6(unix.SYS_FCHOWNAT, cwd, a1, a2, a3, nofollow, 0)
	case unix.SYS_MKNOD: // mknod(path, mode, dev)
		return unix.Syscall6(unix.SYS_MKNODAT, cwd, a1, a2, a3, 0, 0)
	default:
		return unix.Syscall6(n, a1, a2, a3, a4, a5, a6)
	}
}
`

// remapOtherFile is the shim for every non-amd64 musl arch: a plain
// passthrough. arm64/loong64/etc. never used the legacy numbers, so there is
// nothing to remap and the seccomp policy already accepts what musl issues.
const remapOtherFile = `// Code generated by scripts/patch_libc_seccomp.go. DO NOT EDIT.
//go:build linux && (arm64 || loong64 || ppc64le || s390x || riscv64 || 386 || arm)

package libc

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// seccompSyscall passes through unchanged: only x86_64's legacy path-based
// syscall numbers trip Android's seccomp filter, and no other arch has them.
func seccompSyscall(n, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, syscall.Errno) {
	return unix.Syscall6(n, a1, a2, a3, a4, a5, a6)
}
`

func main() {
	undo := flag.Bool("undo", false, "drop the go.mod replace and delete "+patchDir)
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(err)
	}

	if *undo {
		if err := run("go", "mod", "edit", "-dropreplace", modulePath); err != nil {
			fatal(err)
		}
		if err := os.RemoveAll(patchDir); err != nil {
			fatal(err)
		}
		fmt.Println("[patch-libc] replace dropped and", patchDir, "removed")
		return
	}

	if err := run("go", "mod", "download", modulePath); err != nil {
		fatal(fmt.Errorf("download %s: %w", modulePath, err))
	}
	out, err := exec.Command("go", "list", "-m", "-json", modulePath).Output()
	if err != nil {
		fatal(fmt.Errorf("locate %s in the module cache: %w", modulePath, err))
	}
	var mod struct{ Version, Dir string }
	if err := json.Unmarshal(out, &mod); err != nil || mod.Dir == "" {
		fatal(fmt.Errorf("unexpected `go list -m -json` output %q: %v", out, err))
	}
	version, srcDir := mod.Version, mod.Dir

	// Fresh copy every run so the script is idempotent across libc bumps.
	if err := os.RemoveAll(patchDir); err != nil {
		fatal(err)
	}
	if err := copyTree(srcDir, patchDir); err != nil {
		fatal(fmt.Errorf("copy module out of cache: %w", err))
	}

	if err := patchSyscallMusl(filepath.Join(patchDir, targetFile)); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "seccomp_remap_amd64.go"), []byte(remapAmd64File), 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "seccomp_remap_musl_other.go"), []byte(remapOtherFile), 0o644); err != nil {
		fatal(err)
	}

	if err := run("go", "mod", "edit", "-replace", modulePath+"=./"+patchDir); err != nil {
		fatal(err)
	}
	fmt.Printf("[patch-libc] %s %s copied to %s, dispatchers rerouted, remap shim added, go.mod replace added\n",
		modulePath, version, patchDir)
	fmt.Println("[patch-libc] remember: undo before committing (go run scripts/patch_libc_seccomp.go -undo)")
}

// patchSyscallMusl applies syscallReplacements to path, requiring each pattern
// exactly once.
func patchSyscallMusl(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, r := range syscallReplacements {
		old, new := []byte(r[0]), []byte(r[1])
		if n := bytes.Count(src, old); n != 1 {
			return fmt.Errorf("%s: pattern occurs %d times, want exactly 1 (libc layout changed upstream? pattern: %q)",
				path, n, r[0])
		}
		src = bytes.Replace(src, old, new, 1)
	}
	header := "// Code patched by scripts/patch_libc_seccomp.go: syscall dispatchers routed\n" +
		"// through seccompSyscall so Android's x86_64 app seccomp policy does not\n" +
		"// SIGSYS the process. Regenerate with `go run scripts/patch_libc_seccomp.go`.\n\n"
	return os.WriteFile(path, append([]byte(header), src...), 0o644)
}

// copyTree copies src into dst, making everything writable (module-cache
// files are read-only, which would break both patching and later cleanup on
// Windows).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // module cache has no symlinks worth preserving
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// repoRoot resolves the repository root so the script works from any cwd.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside the git repo? %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "[patch-libc] error:", err)
	os.Exit(1)
}
