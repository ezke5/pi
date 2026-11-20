package rootfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		{Source: "proc", Target: "proc", FSType: "proc"},
		{Source: "sysfs", Target: "sys", FSType: "sysfs"},
	}
}

// ChrootRunner executes chroot setup, command invocation, and reverse unmount
// cleanup. Hooks default to real system commands and can be overridden in tests.
type ChrootRunner struct {
	HostResolvConf string
	Mount          func(spec MountSpec, targetPath string) error
	Unmount        func(targetPath string) error
	Exec           func(root string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// DefaultChrootRunner returns a ChrootRunner backed by mount, umount, and chroot.
func DefaultChrootRunner() ChrootRunner {
	return ChrootRunner{
		HostResolvConf: "/etc/resolv.conf",
		Mount:          defaultMount,
		Unmount:        defaultUnmount,
		Exec:           defaultExecChroot,
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

// Run executes the chroot lifecycle with guaranteed reverse-order unmounting.
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

	if len(cmd) == 0 {
		cmd = DefaultChrootCommand(target)
	}
	if err := execFn(target, cmd, stdin, stdout, stderr); err != nil {
		return fmt.Errorf("execute chroot command %v in %q: %w", cmd, target, err)
	}
	return nil
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
	return c.Run()
}
