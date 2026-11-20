package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ezke5/pi/internal/config"
	"github.com/ezke5/pi/internal/rootfs"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "an:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	global, rest := splitFlags(args)
	fs := flag.NewFlagSet("an", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "target rootfs directory")
	configPath := fs.String("config", "", "INI configuration file")
	allowHost := fs.Bool("allow-host-root", false, "explicitly allow bootstrap on /")
	dryRun := fs.Bool("dry-run", false, "show operations without changing the target")
	if err := fs.Parse(global); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) == 0 {
		pos = rest
	}
	if len(pos) == 0 {
		printUsage(stdout)
		return nil
	}

	if pos[0] == "-h" || pos[0] == "--help" || pos[0] == "help" {
		printUsage(stdout)
		return nil
	}

	if *configPath == "" {
		for _, candidate := range []string{"pi.ini", ".pi.ini", filepath.Join(os.Getenv("HOME"), ".config", "pi", "config.ini")} {
			if _, err := os.Stat(candidate); err == nil {
				*configPath = candidate
				break
			}
		}
	}
	c, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	c.Merge("", "", "", "", *root)

	switch pos[0] {
	case "bootstrap":
		return cmdBootstrap(c, *allowHost, *dryRun, stdout)
	case "chroot":
		return cmdChroot(c, pos[1:], *dryRun, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown subcommand %q; run 'an help' for usage", pos[0])
	}
}

func splitFlags(args []string) ([]string, []string) {
	valueFlags := map[string]bool{
		"--root":   true,
		"--config": true,
	}
	boolFlags := map[string]bool{
		"--allow-host-root": true,
		"--dry-run":         true,
	}
	global := []string{}
	rest := []string{}
	seenChroot := false
	inChrootCmd := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if inChrootCmd {
			rest = append(rest, arg)
			continue
		}
		if arg == "--" {
			inChrootCmd = true
			rest = append(rest, arg)
			continue
		}
		matched := false
		for flagName := range valueFlags {
			if arg == flagName || strings.HasPrefix(arg, flagName+"=") {
				global = append(global, arg)
				if arg == flagName && i+1 < len(args) {
					next := args[i+1]
					isSubcommand := next == "bootstrap" || next == "chroot" || next == "help"
					if !strings.HasPrefix(next, "--") && !isSubcommand {
						i++
						global = append(global, next)
					}
				}
				matched = true
				break
			}
		}
		if !matched {
			for flagName := range boolFlags {
				if arg == flagName || strings.HasPrefix(arg, flagName+"=") {
					global = append(global, arg)
					matched = true
					break
				}
			}
		}
		if !matched {
			rest = append(rest, arg)
			if arg == "chroot" && !seenChroot {
				seenChroot = true
			} else if seenChroot {
				inChrootCmd = true
			}
		}
	}
	return global, rest
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: an [global flags] <command> [arguments]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  bootstrap                  Create minimal FHS skeleton in target root")
	fmt.Fprintln(w, "  chroot [CMD...]            Mount virtual filesystems and chroot into target root")
	fmt.Fprintln(w, "  help                       Show this help message")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --root, --config, --dry-run, --allow-host-root")
}

func cmdBootstrap(c config.Config, allowHost, dryRun bool, stdout io.Writer) error {
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(stdout, "dry-run: bootstrap rootfs skeleton at %s\n", target)
		return nil
	}
	if err := rootfs.Bootstrap(target); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "bootstrapped rootfs at %s\n", target)
	return nil
}

func cmdChroot(c config.Config, args []string, dryRun bool, stdin io.Reader, stdout, stderr io.Writer) error {
	target, err := rootfs.Validate(c.Root, false)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if dryRun {
		cmd := args
		if len(cmd) == 0 {
			cmd = rootfs.DefaultChrootCommand(target)
		}
		fmt.Fprintf(stdout, "dry-run: chroot %s (%s)\n", target, strings.Join(cmd, " "))
		return nil
	}
	return rootfs.Chroot(target, args, stdin, stdout, stderr)
}
