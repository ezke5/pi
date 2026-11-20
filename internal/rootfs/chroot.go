package rootfs

import (
	"bufio"
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// MountSpec describes a virtual or bind mount prepared inside a target rootfs.
type MountSpec struct {
	Source string
	Target string
	FSType string
	Bind   bool
}

// ChrootMounts returns the ordered mount specifications required for a safe chroot.
func ChrootMounts() []MountSpec {
	return []MountSpec{
		{Source: "/dev", Target: "dev", Bind: true},
		{Source: "/dev/pts", Target: "dev/pts", Bind: true},
		{Source: "tmpfs", Target: "dev/shm", FSType: "tmpfs"},
		{Source: "proc", Target: "proc", FSType: "proc"},
		{Source: "sysfs", Target: "sys", FSType: "sysfs"},
	}
}

// ChrootRunner executes chroot setup, preflight checks, command invocation, and
// reverse unmount cleanup. Hooks default to real system commands and can be
// overridden in tests.
type ChrootRunner struct {
	HostResolvConf  string
	Mount           func(spec MountSpec, targetPath string) error
	Unmount         func(targetPath string) error
	IsMounted       func(targetPath string) (bool, error)
	CheckExecutable func(root, execPath string) error
	Exec            func(root string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// DefaultChrootRunner returns a ChrootRunner backed by mount, umount, mountinfo,
// ELF PT_INTERP preflight, and chroot.
func DefaultChrootRunner() ChrootRunner {
	return ChrootRunner{
		HostResolvConf:  "/etc/resolv.conf",
		Mount:           defaultMount,
		Unmount:         defaultUnmount,
		IsMounted:       defaultIsMounted,
		CheckExecutable: CheckChrootExecutable,
		Exec:            defaultExecChroot,
	}
}

// Chroot mounts virtual filesystems under root, copies host /etc/resolv.conf if
// present, executes cmd (defaulting to /bin/bash --noprofile --norc or /bin/sh),
// and unmounts in reverse order before returning.
func Chroot(root string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return DefaultChrootRunner().Run(root, cmd, stdin, stdout, stderr)
}

// DefaultChrootCommand picks /bin/bash --noprofile --norc when present inside
// root, falling back to /bin/sh.
func DefaultChrootCommand(root string) []string {
	for _, bashPath := range []string{"/bin/bash", "/usr/bin/bash"} {
		candidate, err := TargetPath(root, bashPath)
		if err != nil {
			continue
		}
		if _, err := os.Lstat(candidate); err == nil {
			return []string{bashPath, "--noprofile", "--norc"}
		}
	}
	return []string{"/bin/sh"}
}

// Run executes the chroot lifecycle with guaranteed reverse-order unmounting of
// only the mounts created by this invocation.
func (r ChrootRunner) Run(root string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (retErr error) {
	target, err := Validate(root, false)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("inspect chroot target %q: %w", target, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("chroot target %q is not a directory", target)
	}

	mountFn := r.Mount
	if mountFn == nil {
		mountFn = defaultMount
	}
	unmountFn := r.Unmount
	if unmountFn == nil {
		unmountFn = defaultUnmount
	}
	execFn := r.Exec
	if execFn == nil {
		execFn = defaultExecChroot
	}
	isMountedFn := r.IsMounted
	if isMountedFn == nil && r.Mount == nil {
		isMountedFn = defaultIsMounted
	}
	checkExecFn := r.CheckExecutable
	if checkExecFn == nil && r.Exec == nil {
		checkExecFn = CheckChrootExecutable
	}

	if len(cmd) == 0 {
		cmd = DefaultChrootCommand(target)
	}
	if checkExecFn != nil && len(cmd) > 0 && strings.HasPrefix(cmd[0], "/") {
		if err := checkExecFn(target, cmd[0]); err != nil {
			return err
		}
	}

	mounts := ChrootMounts()
	mounted := make([]string, 0, len(mounts))
	defer func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			if umountErr := unmountFn(mounted[i]); umountErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("unmount %q: %w", mounted[i], umountErr))
			}
		}
	}()

	for _, spec := range mounts {
		mountTarget, err := TargetPath(target, "/"+spec.Target)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(mountTarget, 0o755); err != nil {
			return fmt.Errorf("create chroot mount point %q: %w", mountTarget, err)
		}
		if isMountedFn != nil {
			alreadyMounted, checkErr := isMountedFn(mountTarget)
			if checkErr != nil {
				return fmt.Errorf("check mount state of %q: %w", mountTarget, checkErr)
			}
			if alreadyMounted {
				continue
			}
		}
		if err := mountFn(spec, mountTarget); err != nil {
			return fmt.Errorf("mount %s on %q: %w", spec.Source, mountTarget, err)
		}
		mounted = append(mounted, mountTarget)
	}

	if r.HostResolvConf != "" {
		if err := copyResolvConfIfPresent(r.HostResolvConf, target); err != nil {
			return err
		}
	}

	if err := execFn(target, cmd, stdin, stdout, stderr); err != nil {
		return fmt.Errorf("execute chroot command %v in %q: %w", cmd, target, err)
	}
	return nil
}

// CheckChrootExecutable verifies that execPath resolves inside root and, if it
// is a dynamically linked ELF binary, that its PT_INTERP dynamic linker (e.g.
// /lib64/ld-linux-x86-64.so.2) also resolves inside root.
func CheckChrootExecutable(root, execPath string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve chroot root %q: %w", root, err)
	}
	resolved, err := resolvePathInRoot(rootAbs, execPath)
	if err != nil {
		return fmt.Errorf("chroot command %q is missing or broken in %q: %w", execPath, rootAbs, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("inspect chroot command %q in %q: %w", execPath, rootAbs, err)
	}
	if info.IsDir() {
		return fmt.Errorf("chroot command %q in %q is a directory", execPath, rootAbs)
	}
	interp, err := readELFInterp(resolved)
	if err != nil || interp == "" {
		return nil
	}
	if _, err := resolvePathInRoot(rootAbs, interp); err != nil {
		return fmt.Errorf("chroot binary %q requires missing ELF interpreter %q in %q: %w", execPath, interp, rootAbs, err)
	}
	return nil
}

func readELFInterp(path string) (string, error) {
	ef, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer ef.Close()
	for _, prog := range ef.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(prog.Open())
		if err != nil {
			return "", err
		}
		data = bytes.TrimRight(data, "\x00")
		return string(data), nil
	}
	return "", nil
}

func resolvePathInRoot(rootAbs, rootRelPath string) (string, error) {
	cur, err := TargetPath(rootAbs, rootRelPath)
	if err != nil {
		return "", err
	}
	for depth := 0; depth < 40; depth++ {
		info, err := os.Lstat(cur)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return cur, nil
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return "", err
		}
		var next string
		if filepath.IsAbs(target) {
			next, err = TargetPath(rootAbs, target)
			if err != nil {
				return "", err
			}
		} else {
			next = filepath.Clean(filepath.Join(filepath.Dir(cur), target))
			rel, relErr := filepath.Rel(rootAbs, next)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("symlink target %q escapes root", target)
			}
		}
		cur = next
	}
	return "", fmt.Errorf("symlink loop resolving %q", rootRelPath)
}

func defaultIsMounted(targetPath string) (bool, error) {
	return isMountedFromFile("/proc/self/mountinfo", targetPath)
}

func isMountedFromFile(mountInfoPath, targetPath string) (bool, error) {
	cleanTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return false, err
	}
	cleanTarget = filepath.Clean(cleanTarget)
	f, err := os.Open(mountInfoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		mountPoint := unescapeMountInfoField(fields[4])
		if filepath.Clean(mountPoint) == cleanTarget {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func unescapeMountInfoField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if val, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(val))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func copyResolvConfIfPresent(hostPath, targetRoot string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read host resolv.conf %q: %w", hostPath, err)
	}
	dest, err := TargetPath(targetRoot, "/etc/resolv.conf")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create chroot /etc directory %q: %w", filepath.Dir(dest), err)
	}
	if existing, statErr := os.Lstat(dest); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(dest); err != nil {
				return fmt.Errorf("replace chroot resolv.conf symlink %q: %w", dest, err)
			}
		} else if existing.Mode().Perm()&0o200 == 0 {
			if err := os.Chmod(dest, existing.Mode().Perm()|0o200); err != nil {
				return fmt.Errorf("make chroot resolv.conf writable %q: %w", dest, err)
			}
		}
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("write chroot resolv.conf %q: %w", dest, err)
	}
	return nil
}

func defaultMount(spec MountSpec, targetPath string) error {
	var args []string
	if spec.Bind {
		args = []string{"--bind", spec.Source, targetPath}
	} else {
		args = []string{"-t", spec.FSType, spec.Source, targetPath}
	}
	out, err := exec.Command("mount", args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("%w (%s)", err, detail)
		}
		return err
	}
	return nil
}

func defaultUnmount(targetPath string) error {
	out, err := exec.Command("umount", targetPath).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("%w (%s)", err, detail)
		}
		return err
	}
	return nil
}

func defaultExecChroot(root string, cmdArgs []string, stdin io.Reader, stdout, stderr io.Writer) error {
	args := append([]string{root}, cmdArgs...)
	c := exec.Command("chroot", args...)
	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm"
	}
	c.Env = []string{
		"HOME=/root",
		"TERM=" + term,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"PS1=(pi-chroot) \\w \\$ ",
	}
	c.Stdin = stdin
	c.Stdout = stdout
	c.Stderr = stderr
	if err := c.Start(); err != nil {
		return err
	}
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				if c.Process != nil {
					_ = c.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	err := c.Wait()
	close(done)
	signal.Stop(sigCh)
	return err
}
