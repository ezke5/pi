package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ezke5/pi/internal/archive"
	"github.com/ezke5/pi/internal/config"
	"github.com/ezke5/pi/internal/fetch"
	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/pkgbuild"
	"github.com/ezke5/pi/internal/rootfs"
)

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	global, rest := splitFlags(args)
	fs := flag.NewFlagSet("pi", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ports := fs.String("ports", "", "ports directory")
	work := fs.String("work", "", "build work directory")
	temp := fs.String("temp", "", "temporary download/log directory")
	cache := fs.String("cache", "", "binary package/source cache directory")
	root := fs.String("root", "", "target CRUX root")
	configPath := fs.String("config", "", "INI configuration file")
	allowHost := fs.Bool("allow-host-root", false, "explicitly allow operating on /")
	dryRun := fs.Bool("dry-run", false, "show operations without changing the target")
	reuse := fs.Bool("reuse", true, "reuse cached sources")
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
	c.Merge(*ports, *work, *temp, *cache, *root)
	if *cache != "" {
		c.Binpkgs = *cache
	}

	switch pos[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	case "init":
		return cmdInit(c, *configPath, pos[1:], stdout)
	case "info", "inspect":
		return cmdInfo(c, pos[1:], stdout)
	case "build":
		return cmdBuild(c, pos[1:], *reuse, dryRun, stdout)
	case "install":
		return cmdInstall(c, pos[1:], *allowHost, dryRun, stdout)
	case "remove":
		return cmdRemove(c, pos[1:], *allowHost, dryRun, stdout)
	case "list":
		return cmdList(c, *allowHost, stdout)
	default:
		return fmt.Errorf("unknown subcommand %q; run 'pi help' for usage", pos[0])
	}
}

func splitFlags(args []string) ([]string, []string) {
	known := map[string]bool{"--ports": true, "--work": true, "--temp": true, "--cache": true, "--root": true, "--config": true, "--allow-host-root": true, "--dry-run": true, "--reuse": true}
	global := []string{}
	rest := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if known[arg] || strings.HasPrefix(arg, "--ports=") || strings.HasPrefix(arg, "--work=") ||
			strings.HasPrefix(arg, "--temp=") || strings.HasPrefix(arg, "--cache=") ||
			strings.HasPrefix(arg, "--root=") || strings.HasPrefix(arg, "--config=") ||
			strings.HasPrefix(arg, "--reuse=") {
			global = append(global, arg)
			if known[arg] && !strings.Contains(arg, "=") && i+1 < len(args) && arg != "--allow-host-root" && arg != "--dry-run" && arg != "--reuse" {
				i++
				global = append(global, args[i])
			}
		} else {
			rest = append(rest, arg)
		}
	}
	return global, rest
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: pi [global flags] <command> [arguments]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init [dir]                 Create recommended directory and config layout")
	fmt.Fprintln(w, "  info|inspect <PKGBUILD>    Inspect a package recipe")
	fmt.Fprintln(w, "  build <PKGBUILD|name>      Build and package a recipe")
	fmt.Fprintln(w, "  install <package|name>     Install a .pkg.tar.zst archive")
	fmt.Fprintln(w, "  remove <name>              Remove an installed package")
	fmt.Fprintln(w, "  list                       List installed package records")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --root, --ports, --work, --temp, --cache, --config")
	fmt.Fprintln(w, "  --dry-run, --allow-host-root")
}

func cmdInit(c config.Config, configPath string, args []string, stdout io.Writer) error {
	if len(args) > 0 {
		c.Ports = filepath.Join(args[0], "ports")
		c.Work = filepath.Join(args[0], "work")
		c.Temp = filepath.Join(args[0], "temp")
		c.Cache = filepath.Join(args[0], "pkg")
		c.Binpkgs = c.Cache
	}
	if err := c.EnsureDirs(); err != nil {
		return err
	}
	for _, dir := range []string{c.Ports, filepath.Join(c.Ports, "core"), filepath.Join(c.Ports, "extra")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("initialize ports directory %q: %w", dir, err)
		}
	}
	path := configPath
	if path == "" {
		path = "pi.ini"
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stdout, "configuration already exists: %s\n", path)
		return nil
	}
	content := fmt.Sprintf("[paths]\nports = %s\nwork = %s\ntemp = %s\ncache = %s\nbinpkgs = %s\nroot = %s\n", c.Ports, c.Work, c.Temp, c.Cache, c.Binpkgs, c.Root)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write configuration %q: %w", path, err)
	}
	fmt.Fprintf(stdout, "initialized pi configuration at %s\n", path)
	return nil
}

func cmdInfo(c config.Config, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("info requires a PKGBUILD path or package name")
	}
	path, err := pkgbuild.Find(c.Ports, args[0])
	if err != nil {
		return err
	}
	r, err := pkgbuild.Read(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "name: %s\nversion: %s-%s\nrecipe: %s\n", r.Pkgname, r.Pkgver, r.Pkgrel, r.Path)
	fmt.Fprintf(stdout, "sources: %d\ndependencies: %d\n", len(r.Source), len(r.Depends))
	return nil
}

func cmdBuild(c config.Config, args []string, reuse bool, dryRun *bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("build requires a PKGBUILD path or package name")
	}
	if err := c.EnsureDirs(); err != nil {
		return err
	}
	path, err := pkgbuild.Find(c.Ports, args[0])
	if err != nil {
		return err
	}
	r, err := pkgbuild.Read(path)
	if err != nil {
		return err
	}
	buildDir := filepath.Join(c.Work, r.Pkgname+"-"+r.Pkgver)
	srcDir := filepath.Join(buildDir, "src")
	pkgDir := filepath.Join(buildDir, "pkg")
	if *dryRun {
		fmt.Fprintf(stdout, "dry-run: build %s-%s-%s in %s\n", r.Pkgname, r.Pkgver, r.Pkgrel, buildDir)
		fmt.Fprintf(stdout, "dry-run: package archive %s\n", filepath.Join(c.Binpkgs, r.ArchiveName()))
		return nil
	}
	if err := os.RemoveAll(buildDir); err != nil {
		return fmt.Errorf("clean build work directory %q: %w", buildDir, err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return fmt.Errorf("create source directory %q: %w", srcDir, err)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fmt.Errorf("create package staging directory %q: %w", pkgDir, err)
	}

	for i, source := range r.Source {
		rawURL := source
		if strings.Contains(source, "::") {
			rawURL = strings.SplitN(source, "::", 2)[1]
		}
		var local string
		if strings.Contains(rawURL, "://") {
			base := filepath.Base(strings.SplitN(rawURL, "?", 2)[0])
			local = filepath.Join(c.Cache, base)
			if info, statErr := os.Stat(local); statErr == nil && !info.IsDir() && reuse {
				fmt.Fprintf(stdout, "reusing cached source %s\n", local)
			} else if statErr == nil && !info.IsDir() && !reuse {
				if err := os.Remove(local); err != nil {
					return fmt.Errorf("remove cached source %q: %w", local, err)
				}
				if err := fetch.Download(rawURL, local, 30*time.Second); err != nil {
					return fmt.Errorf("%s", fetch.ManualDownloadNotice(err))
				}
			} else {
				if err := fetch.Download(rawURL, local, 30*time.Second); err != nil {
					return fmt.Errorf("%s", fetch.ManualDownloadNotice(err))
				}
			}
		} else {
			local = source
			if !filepath.IsAbs(local) {
				local = filepath.Join(filepath.Dir(r.Path), local)
			}
		}
		if info, err := os.Stat(local); err != nil || info.IsDir() {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s", fetch.ManualDownloadNotice(fmt.Errorf("missing local source file %q", local)))
			}
			return fmt.Errorf("source %q is not a regular file", local)
		}
		if i < len(r.SHA256Sums) && r.SHA256Sums[i] != "" && r.SHA256Sums[i] != "SKIP" {
			if _, err := fetch.VerifySHA256(local, r.SHA256Sums[i]); err != nil {
				return fmt.Errorf("%s", fetch.ManualDownloadNotice(err))
			}
		}
		// Copy source archives into srcdir for build().
		dest := filepath.Join(srcDir, filepath.Base(local))
		if err := copyFile(local, dest); err != nil {
			return fmt.Errorf("stage source %q in %q: %w", local, srcDir, err)
		}
	}

	script := fmt.Sprintf("set -e\ncd %q\nSRC=%q\nPKGDIR=%q\n%s\nbuild\npackage\n", srcDir, srcDir, pkgDir, r.BuildFunction+"\n"+r.PackageFunction)
	_ = script
	cmdScript := "#!/bin/bash\nset -e\n" + fmt.Sprintf("export srcdir=%q\nexport pkgdir=%q\n", srcDir, pkgDir)
	cmdScript += "cd \"$srcdir\"\n"
	if r.BuildFunction != "" {
		cmdScript += "build() {\n" + r.BuildFunction + "\n}\n"
	}
	if r.PackageFunction != "" {
		cmdScript += "package() {\n" + r.PackageFunction + "\n}\n"
	}
	if r.BuildFunction != "" {
		cmdScript += "build\n"
	}
	if r.PackageFunction != "" {
		cmdScript += "package\n"
	}
	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader(cmdScript)
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("execute build/package functions for %s: %w", r.Pkgname, err)
	}

	entries, err := footprint.Generate(pkgDir)
	if err != nil {
		return err
	}
	footprintPath := filepath.Join(pkgDir, "var", "lib", "pi", "packages", r.Pkgname+".footprint")
	if err := footprint.Write(footprintPath, entries); err != nil {
		return err
	}
	output := filepath.Join(c.Binpkgs, r.ArchiveName())
	if err := archive.Create(pkgDir, output, r.Pkgname, r.Pkgver, r.Pkgrel, entries); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "built %s\n", output)
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func cmdInstall(c config.Config, args []string, allowHost bool, dryRun *bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("install requires a .pkg.tar.zst archive or package name")
	}
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	archivePath := args[0]
	if !strings.HasSuffix(archivePath, ".pkg.tar.zst") {
		path, findErr := pkgbuild.Find(c.Ports, archivePath)
		if findErr != nil {
			archivePath = filepath.Join(c.Binpkgs, archivePath)
		} else {
			r, readErr := pkgbuild.Read(path)
			if readErr != nil {
				return readErr
			}
			archivePath = filepath.Join(c.Binpkgs, r.ArchiveName())
		}
	}
	if _, err := os.Stat(archivePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("package archive %q not found; build it first or place it in cache %q", archivePath, c.Binpkgs)
		}
		return fmt.Errorf("inspect package archive %q: %w", archivePath, err)
	}
	if *dryRun {
		fmt.Fprintf(stdout, "dry-run: install %s into %s\n", archivePath, target)
		return nil
	}
	if err := archive.Extract("", archivePath, target); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "installed %s into %s\n", archivePath, target)
	return nil
}

func cmdRemove(c config.Config, args []string, allowHost bool, dryRun *bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("remove requires a package name")
	}
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintf(stdout, "dry-run: remove package %s from %s\n", args[0], target)
		return nil
	}
	recordDir, err := rootfs.TargetPath(target, "/var/lib/pi/packages")
	if err != nil {
		return err
	}
	footprintPath := filepath.Join(recordDir, args[0]+".footprint")
	entries, err := footprint.Read(footprintPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("package %q has no footprint record at %q", args[0], footprintPath)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		path, err := rootfs.TargetPath(target, entry.Path)
		if err != nil {
			return err
		}
		if entry.Type == "dir" {
			os.Remove(path)
			continue
		}
		if strings.HasPrefix(entry.Path, "/etc/") {
			fmt.Fprintf(stdout, "preserving config file %s\n", entry.Path)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove package-owned file %q: %w", path, err)
		}
	}
	if err := os.Remove(footprintPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove package footprint %q: %w", footprintPath, err)
	}
	fmt.Fprintf(stdout, "removed %s\n", args[0])
	return nil
}

func cmdList(c config.Config, allowHost bool, stdout io.Writer) error {
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	recordDir, err := rootfs.TargetPath(target, "/var/lib/pi/packages")
	if err != nil {
		return err
	}
	files, err := os.ReadDir(recordDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list package records in %q: %w", recordDir, err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".footprint") {
			fmt.Fprintf(stdout, "%s\n", strings.TrimSuffix(file.Name(), ".footprint"))
		}
	}
	return nil
}
