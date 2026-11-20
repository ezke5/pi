package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/ezke5/pi/internal/archive"
	"github.com/ezke5/pi/internal/binary"
	"github.com/ezke5/pi/internal/config"
	"github.com/ezke5/pi/internal/deps"
	"github.com/ezke5/pi/internal/fetch"
	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/pkgbuild"
	"github.com/ezke5/pi/internal/repoindex"
	"github.com/ezke5/pi/internal/rootfs"
	"github.com/ezke5/pi/internal/state"
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
	force := fs.Bool("force", false, "override dependency safety checks")
	rebuild := fs.Bool("rebuild", false, "rebuild and reinstall even when matching version and USE state are already installed")
	keepWork := fs.Bool("keep-work", false, "retain temporary build work directory after successful build")
	noStrip := fs.Bool("no-strip", false, "disable automatic ELF/archive stripping during packaging")
	dryRun := fs.Bool("dry-run", false, "show operations without changing the target")
	reuse := fs.Bool("reuse", true, "reuse cached sources")
	sourceBuild := fs.Bool("source-build", false, "always build from PKGBUILD instead of using binaries")
	indexPath := fs.String("index", "", "JSON repository index")
	repoURL := fs.String("repo-url", "", "base URL for a JSON repository index and binary assets")
	arch := fs.String("arch", "", "package architecture")
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
	if *indexPath != "" {
		c.Index = *indexPath
	}
	if *repoURL != "" {
		c.RepoURL = *repoURL
	}
	if *arch != "" {
		c.Arch = *arch
	}

	switch pos[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	case "init":
		return cmdInit(c, *configPath, pos[1:], stdout)
	case "bootstrap":
		return cmdBootstrap(c, *allowHost, *dryRun, stdout)
	case "chroot":
		return cmdChroot(c, pos[1:], *dryRun, os.Stdin, stdout, stderr)
	case "verify":
		return cmdVerify(c, pos[1:], *allowHost, stdout)
	case "info", "inspect":
		return cmdInfo(c, pos[1:], stdout)
	case "build":
		return cmdBuild(c, pos[1:], *reuse, *rebuild, *keepWork, *noStrip, *dryRun, stdout)
	case "install":
		return cmdInstall(c, pos[1:], *allowHost, *force, *rebuild, *keepWork, *noStrip, *dryRun, *reuse, *sourceBuild, stdout)
	case "remove":
		return cmdRemove(c, pos[1:], *allowHost, *force, *dryRun, stdout)
	case "list":
		return cmdList(c, *allowHost, stdout)
	default:
		return fmt.Errorf("unknown subcommand %q; run 'pi help' for usage", pos[0])
	}
}

func splitFlags(args []string) ([]string, []string) {
	valueFlags := map[string]bool{
		"--ports": true, "--work": true, "--temp": true, "--cache": true,
		"--root": true, "--config": true,
		"--index": true, "--repo-url": true, "--arch": true,
	}
	boolFlags := map[string]bool{
		"--allow-host-root": true, "--dry-run": true, "--reuse": true, "--force": true,
		"--rebuild": true, "--keep-work": true, "--no-strip": true, "--source-build": true,
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
					isSubcommand := next == "init" || next == "bootstrap" || next == "chroot" || next == "verify" || next == "info" || next == "inspect" || next == "build" || next == "install" || next == "remove" || next == "list" || next == "help"
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
	fmt.Fprintln(w, "Usage: pi [global flags] <command> [arguments]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init [dir]                 Create recommended directory and config layout")
	fmt.Fprintln(w, "  bootstrap                  Create minimal FHS skeleton in target root")
	fmt.Fprintln(w, "  chroot [CMD...]            Mount virtual filesystems and chroot into target root")
	fmt.Fprintln(w, "  verify [name...]           Verify installed package footprints, modes, and symlinks")
	fmt.Fprintln(w, "  info|inspect <PKGBUILD>    Inspect a package recipe")
	fmt.Fprintln(w, "  build <PKGBUILD|name>      Build and package a recipe")
	fmt.Fprintln(w, "  install <package|name>     Install a .pkg.tar.zst archive")
	fmt.Fprintln(w, "  remove <name>              Remove an installed package")
	fmt.Fprintln(w, "  list                       List installed package records")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --root, --ports, --work, --temp, --cache, --config, --index, --repo-url, --arch")
	fmt.Fprintln(w, "  --dry-run, --allow-host-root, --force, --rebuild, --keep-work, --no-strip, --source-build")
}

func cmdVerify(c config.Config, args []string, allowHost bool, stdout io.Writer) error {
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	lock, err := state.Lock(target, false)
	if err != nil {
		return err
	}
	defer lock.Unlock()

	var names []string
	if len(args) > 0 {
		names = args
	} else {
		records, err := state.Installed(target)
		if err != nil {
			return err
		}
		for _, rec := range records {
			names = append(names, rec.Pkgname)
		}
	}
	failed := 0
	for _, name := range names {
		rec, err := state.Load(target, name)
		if err != nil {
			fmt.Fprintf(stdout, "FAIL: %s: %v\n", name, err)
			failed++
			continue
		}
		res := state.VerifyPackage(target, name, "", "", "")
		if res.OK {
			fmt.Fprintf(stdout, "ok: %s-%s-%s\n", rec.Pkgname, rec.Pkgver, rec.Pkgrel)
		} else {
			fmt.Fprintf(stdout, "FAIL: %s: %s\n", name, res.Reason())
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("verification failed for %d package(s)", failed)
	}
	return nil
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
	useState := c.ResolveUSE(r.Pkgname)
	fmt.Fprintf(stdout, "name: %s\nversion: %s-%s\nrecipe: %s\n", r.Pkgname, r.Pkgver, r.Pkgrel, r.Path)
	fmt.Fprintf(stdout, "sources: %d\ndependencies: %d\n", len(r.Source), len(r.Depends))
	fmt.Fprintf(stdout, "use: %s\nuse_disabled: %s\nuse_hash: %s\n", strings.Join(useState.Enabled, " "), strings.Join(useState.Disabled, " "), useState.Hash)
	fmt.Fprintf(stdout, "build: jobs=%d cflags=%q cxxflags=%q ldflags=%q strip=%q (config: %s)\n",
		c.Build.Jobs, c.Build.CFLAGS, c.Build.CXXFLAGS, c.Build.LDFLAGS, c.Build.Strip, buildConfigSource(c))
	fmt.Fprintf(stdout, "build_source: PKGBUILD (%s)\n", r.Path)
	return nil
}

func buildConfigSource(c config.Config) string {
	if c.Config != "" {
		return c.Config
	}
	return "defaults"
}

func cmdBuild(c config.Config, args []string, reuse, rebuild, keepWork, noStrip, dryRun bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("build requires a PKGBUILD path or package name")
	}
	graph, err := (deps.Resolver{Ports: c.Ports}).Resolve(args[0])
	if err != nil {
		return err
	}
	recipes := graph.Recipes()
	toBuild := make([]pkgbuild.Recipe, 0, len(recipes))
	for i, r := range recipes {
		useState := c.ResolveUSE(r.Pkgname)
		if !rebuild && c.Root != "" {
			if ok, reason := state.VerifyInstalled(c.Root, r.Pkgname, r.Pkgver, r.Pkgrel, useState.Hash); ok {
				fmt.Fprintf(stdout, "%d. %s-%s-%s (skipping: already installed with matching USE hash %s; use --rebuild to force)\n",
					i+1, r.Pkgname, r.Pkgver, r.Pkgrel, useState.Hash)
				continue
			} else if installed, loadErr := state.Load(c.Root, r.Pkgname); loadErr == nil && installed.Pkgver == r.Pkgver && installed.Pkgrel == r.Pkgrel {
				fmt.Fprintf(stdout, "rebuilding %s-%s-%s: %s\n", r.Pkgname, r.Pkgver, r.Pkgrel, reason)
			}
		}
		fmt.Fprintf(stdout, "%d. %s-%s-%s (build from PKGBUILD)\n", i+1, r.Pkgname, r.Pkgver, r.Pkgrel)
		toBuild = append(toBuild, r)
	}
	if dryRun || len(toBuild) == 0 {
		return nil
	}
	if err := c.EnsureDirs(); err != nil {
		return err
	}
	for _, r := range toBuild {
		if err := cmdBuildRecipe(c, r, reuse, keepWork, noStrip, stdout); err != nil {
			return err
		}
	}
	return nil
}

func cmdBuildRecipe(c config.Config, r pkgbuild.Recipe, reuse, keepWork, noStrip bool, stdout io.Writer) error {
	if err := os.MkdirAll(c.Binpkgs, 0o755); err != nil {
		return fmt.Errorf("create binpkgs directory %q: %w", c.Binpkgs, err)
	}
	if err := checkWritableDirs(c.Work, c.Cache, c.Binpkgs); err != nil {
		return err
	}
	buildDir := filepath.Join(c.Work, r.Pkgname+"-"+r.Pkgver)
	srcDir := filepath.Join(buildDir, "src")
	pkgDir := filepath.Join(buildDir, "pkg")
	tmpDir := filepath.Join(buildDir, "tmp")
	homeDir := filepath.Join(buildDir, "home")
	if err := os.RemoveAll(buildDir); err != nil {
		return fmt.Errorf("clean build work directory %q: %w", buildDir, err)
	}
	for _, dir := range []string{srcDir, pkgDir, tmpDir, homeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create build directory %q: %w", dir, err)
		}
	}

	retainWork := keepWork || c.Build.KeepWork
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sigDone := make(chan struct{})
	defer func() {
		close(sigDone)
		signal.Stop(sigCh)
	}()
	go func() {
		select {
		case <-sigCh:
			if !retainWork {
				_ = os.RemoveAll(buildDir)
			}
		case <-sigDone:
		}
	}()

	for i, source := range r.Source {
		sourceResult, err := fetch.ResolveSourceWithTimeout(source, filepath.Dir(r.Path), c.Cache, reuse, c.DownloadTimeout, os.Stdin, stdout)
		if err != nil {
			return fmt.Errorf("%s", fetch.ManualDownloadNotice(fmt.Errorf("source %q for %s: %w", source, r.Pkgname, err)))
		}
		local := sourceResult.Path
		sourceName := sourceResult.Name
		if i < len(r.SHA256Sums) && r.SHA256Sums[i] != "" && r.SHA256Sums[i] != "SKIP" {
			if _, err := fetch.VerifySHA256(local, r.SHA256Sums[i]); err != nil {
				return fmt.Errorf("%s", fetch.ManualDownloadNotice(fmt.Errorf("source %q for %s: %w", source, r.Pkgname, err)))
			}
		}
		// Copy source archives into srcdir for build().
		dest := filepath.Join(srcDir, sourceName)
		if err := copyFile(local, dest); err != nil {
			return fmt.Errorf("stage source %q in %q: %w", local, srcDir, err)
		}
		if isSourceArchive(dest) && !contains(r.Noextract, sourceName) {
			if err := extractSourceArchive(dest, srcDir); err != nil {
				return err
			}
		}
	}

	useState := c.ResolveUSE(r.Pkgname)
	env := buildEnv(c, buildDir, srcDir, pkgDir, r, useState)
	runPhase := func(phaseName, body string) error {
		script := "#!/bin/bash\nset -e\numask 022\n"
		script += "use_enabled() { case \" $PI_USE_FLAGS \" in *\" $1 \"*) return 0;; *) return 1;; esac; }\n"
		script += "use_disabled() { case \" $PI_USE_DISABLED \" in *\" $1 \"*) return 0;; *) return 1;; esac; }\n"
		script += "cd \"$srcdir\"\n"
		script += phaseName + "() {\n" + body + "\n}\n"
		script += phaseName + "\n"
		cmd := exec.Command("bash")
		cmd.Env = env
		cmd.Stdin = strings.NewReader(script)
		cmd.Stdout = stdout
		cmd.Stderr = stdout
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("execute %s() for %s: %w", phaseName, r.Pkgname, err)
		}
		return nil
	}

	if r.BuildFunction != "" {
		if err := runPhase("build", r.BuildFunction); err != nil {
			return err
		}
	}
	if r.PackageFunction != "" {
		// Enforce strict build/staging separation when both build() and package()
		// are defined so stray build() writes cannot leak into $pkgdir.
		if r.BuildFunction != "" {
			if err := os.RemoveAll(pkgDir); err != nil {
				return fmt.Errorf("reset package staging directory %q: %w", pkgDir, err)
			}
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				return fmt.Errorf("recreate package staging directory %q: %w", pkgDir, err)
			}
		}
		if err := runPhase("package", r.PackageFunction); err != nil {
			return err
		}
	}

	if c.Build.CleanJunk {
		if err := cleanStagingJunk(pkgDir); err != nil {
			return err
		}
	}
	_, _, _, _, stripSetting := effectiveBuildSettings(c)
	if stripEnabled(stripSetting, noStrip) {
		if err := stripStagingFiles(pkgDir, filepath.Dir(r.Path), stdout); err != nil {
			return fmt.Errorf("strip staged files for %s: %w", r.Pkgname, err)
		}
	}
	if err := archive.ValidateSymlinks(pkgDir); err != nil {
		return fmt.Errorf("validate package %s: %w", r.Pkgname, err)
	}

	entries, err := footprint.Generate(pkgDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("package %s produced an empty staging directory; check DESTDIR=\"$pkgdir\"", r.Pkgname)
	}
	output := filepath.Join(c.Binpkgs, r.ArchiveName())
	if err := archive.CreateWithUSE(pkgDir, output, r.Pkgname, r.Pkgver, r.Pkgrel, c.Arch,
		useState.Enabled, useState.Disabled, useState.Hash, entries, r.Backup); err != nil {
		return err
	}
	diffRecipeFootprint(filepath.Dir(r.Path), entries, stdout)
	if !retainWork {
		if err := os.RemoveAll(buildDir); err != nil {
			fmt.Fprintf(stdout, "warning: clean build work directory %q: %v\n", buildDir, err)
		}
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

func cmdInstall(c config.Config, args []string, allowHost, force, rebuild, keepWork, noStrip, dryRun, reuse, sourceBuild bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("install requires a .pkg.tar.zst archive or package name")
	}
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	if strings.HasSuffix(args[0], ".pkg.tar.zst") {
		archivePath := args[0]
		if _, statErr := os.Stat(archivePath); statErr != nil {
			archivePath = filepath.Join(c.Binpkgs, filepath.Base(args[0]))
		}
		info, infoErr := archive.ReadInfo(archivePath)
		record := state.Record{}
		if infoErr == nil {
			record = state.Record{
				Pkgname:     info.Pkgname,
				Pkgver:      info.Pkgver,
				Pkgrel:      info.Pkgrel,
				Arch:        info.Arch,
				USEFlags:    info.USEFlags,
				USEDisabled: info.USEDisabled,
				USEHash:     info.USEHash,
				Backup:      info.Backup,
				Depends:     info.Depends,
			}
			if info.USEHash != "" {
				targetUSE := c.ResolveUSE(info.Pkgname)
				if targetUSE.Hash != info.USEHash {
					return fmt.Errorf("package archive %q USE hash mismatch: archive %s, requested %s", archivePath, info.USEHash, targetUSE.Hash)
				}
			}
		} else {
			base := strings.TrimSuffix(filepath.Base(archivePath), ".pkg.tar.zst")
			record.Pkgname = base
		}
		if !rebuild && record.Pkgname != "" && record.Pkgver != "" {
			if ok, _ := state.VerifyInstalled(target, record.Pkgname, record.Pkgver, record.Pkgrel, record.USEHash); ok {
				fmt.Fprintf(stdout, "1. %s: already installed (%s-%s, use_hash=%s); skipping (use --rebuild to reinstall)\n",
					filepath.Base(archivePath), record.Pkgver, record.Pkgrel, record.USEHash)
				return nil
			}
		}
		status := "new install"
		if record.Pkgname != "" {
			installed, loadErr := state.Load(target, record.Pkgname)
			if loadErr == nil && installed.Pkgver != "" {
				status = fmt.Sprintf("already installed (%s-%s -> %s-%s)", installed.Pkgver, installed.Pkgrel, record.Pkgver, record.Pkgrel)
			}
		}
		fmt.Fprintf(stdout, "1. %s: %s; existing archive\n", filepath.Base(archivePath), status)
		if dryRun {
			return installArchive(c, target, archivePath, record, force, true, stdout)
		}
		return installArchive(c, target, archivePath, record, force, false, stdout)
	}
	useHash := c.ResolveUSE(args[0]).Hash
	if archivePath := findPackageArchiveForUSE(c.Binpkgs, args[0], c.Arch, useHash); archivePath != "" {
		if _, statErr := os.Stat(filepath.Join(c.Ports, args[0], "PKGBUILD")); statErr != nil {
			info, infoErr := archive.ReadInfo(archivePath)
			record := state.Record{Pkgname: args[0]}
			if infoErr == nil {
				record = state.Record{
					Pkgname:     info.Pkgname,
					Pkgver:      info.Pkgver,
					Pkgrel:      info.Pkgrel,
					Arch:        info.Arch,
					USEFlags:    info.USEFlags,
					USEDisabled: info.USEDisabled,
					USEHash:     info.USEHash,
					Backup:      info.Backup,
					Depends:     info.Depends,
				}
				if info.USEHash != "" && c.ResolveUSE(record.Pkgname).Hash != info.USEHash {
					return fmt.Errorf("package archive %q USE hash mismatch: archive %s, requested %s", archivePath, info.USEHash, c.ResolveUSE(record.Pkgname).Hash)
				}
			}
			if !rebuild && record.Pkgname != "" && record.Pkgver != "" {
				if ok, _ := state.VerifyInstalled(target, record.Pkgname, record.Pkgver, record.Pkgrel, record.USEHash); ok {
					fmt.Fprintf(stdout, "1. %s: already installed (%s-%s, use_hash=%s); skipping (use --rebuild to reinstall)\n",
						filepath.Base(archivePath), record.Pkgver, record.Pkgrel, record.USEHash)
					return nil
				}
			}
			status := "new install"
			if record.Pkgname != "" {
				installed, loadErr := state.Load(target, record.Pkgname)
				if loadErr == nil && installed.Pkgver != "" {
					status = fmt.Sprintf("already installed (%s-%s -> %s-%s)", installed.Pkgver, installed.Pkgrel, record.Pkgver, record.Pkgrel)
				}
			}
			fmt.Fprintf(stdout, "1. %s: %s; existing archive\n", filepath.Base(archivePath), status)
			if dryRun {
				return installArchive(c, target, archivePath, record, force, true, stdout)
			}
			return installArchive(c, target, archivePath, record, force, false, stdout)
		}
	}
	resolver := deps.Resolver{Ports: c.Ports}
	var graph deps.Graph
	if sourceBuild || rebuild {
		graph, err = resolver.Resolve(args[0])
	} else {
		graph, err = resolver.ResolveRuntime(args[0])
	}
	if err != nil {
		archivePath := findPackageArchiveForUSE(c.Binpkgs, args[0], c.Arch, c.ResolveUSE(args[0]).Hash)
		if archivePath == "" {
			return err
		}
		info, infoErr := archive.ReadInfo(archivePath)
		record := state.Record{Pkgname: args[0]}
		if infoErr == nil {
			record = state.Record{
				Pkgname:     info.Pkgname,
				Pkgver:      info.Pkgver,
				Pkgrel:      info.Pkgrel,
				Arch:        info.Arch,
				USEFlags:    info.USEFlags,
				USEDisabled: info.USEDisabled,
				USEHash:     info.USEHash,
				Backup:      info.Backup,
				Depends:     info.Depends,
			}
		}
		if !rebuild && record.Pkgname != "" && record.Pkgver != "" {
			if ok, _ := state.VerifyInstalled(target, record.Pkgname, record.Pkgver, record.Pkgrel, record.USEHash); ok {
				fmt.Fprintf(stdout, "1. %s: already installed (%s-%s, use_hash=%s); skipping (use --rebuild to reinstall)\n",
					filepath.Base(archivePath), record.Pkgver, record.Pkgrel, record.USEHash)
				return nil
			}
		}
		fmt.Fprintf(stdout, "1. %s: new install; existing archive\n", filepath.Base(archivePath))
		if dryRun {
			return installArchive(c, target, archivePath, record, force, true, stdout)
		}
		return installArchive(c, target, archivePath, record, force, false, stdout)
	}
	if len(graph.Order) == 0 {
		archivePath := args[0]
		if _, statErr := os.Stat(archivePath); statErr != nil {
			archivePath = filepath.Join(c.Binpkgs, filepath.Base(args[0]))
		}
		return installArchive(c, target, archivePath, state.Record{Pkgname: strings.TrimSuffix(filepath.Base(archivePath), ".pkg.tar.zst")}, force, dryRun, stdout)
	}
	var index repoindex.Index
	indexLoaded := false
	if !sourceBuild && !rebuild && c.Index != "" {
		loaded, loadErr := binary.LoadIndex(c.Index)
		if loadErr == nil {
			index, indexLoaded = loaded, true
		} else if !os.IsNotExist(loadErr) {
			fmt.Fprintf(stdout, "repository index unavailable; using source/cache fallback: %v\n", loadErr)
		}
	}
	if !sourceBuild && !rebuild && !dryRun && !indexLoaded && c.RepoURL != "" {
		indexURL := binary.NormalizeURL(c.RepoURL, "index.json")
		temp, tempErr := os.CreateTemp("", "pi-index-*.json")
		if tempErr == nil {
			tempPath := temp.Name()
			temp.Close()
			defer os.Remove(tempPath)
			if downloadErr := fetch.Download(indexURL, tempPath, c.DownloadTimeout); downloadErr == nil {
				if loaded, loadErr := binary.LoadIndex(tempPath); loadErr == nil {
					index, indexLoaded = loaded, true
				}
			} else {
				fmt.Fprintf(stdout, "repository index unavailable; using source/cache fallback: %v\n", downloadErr)
			}
		}
	}
	for i, name := range graph.Order {
		r := graph.Nodes[name].Recipe
		useState := c.ResolveUSE(r.Pkgname)
		version, release := r.Pkgver, r.Pkgrel
		if !rebuild {
			checkVer, checkRel := r.Pkgver, r.Pkgrel
			if indexLoaded {
				if entry, ok := repoindex.FindForUSE(index, r.Pkgname, c.Arch, useState.Hash); ok && entry.Pkgver != "" {
					checkVer, checkRel = entry.Pkgver, entry.Pkgrel
				}
			}
			if ok, reason := state.VerifyInstalled(target, r.Pkgname, checkVer, checkRel, useState.Hash); ok {
				fmt.Fprintf(stdout, "%d. %s-%s-%s: already installed with matching USE hash %s; skipping (use --rebuild to reinstall)\n",
					i+1, r.Pkgname, checkVer, checkRel, useState.Hash)
				continue
			} else if installed, loadErr := state.Load(target, r.Pkgname); loadErr == nil && installed.Pkgver == checkVer && installed.Pkgrel == checkRel {
				fmt.Fprintf(stdout, "reinstalling %s-%s-%s: %s\n", r.Pkgname, checkVer, checkRel, reason)
			}
		}
		var archivePath, source string
		var binaryEntry repoindex.Entry
		if rebuild {
			archivePath, source = filepath.Join(c.Binpkgs, r.ArchiveName()), "build from PKGBUILD"
		} else {
			archivePath, source = selectInstallArchiveForUSE(c.Binpkgs, r, c.Arch, useState.Hash)
			if indexLoaded {
				if entry, ok := repoindex.FindForUSE(index, r.Pkgname, c.Arch, useState.Hash); ok {
					cachedPath := filepath.Join(c.Binpkgs, entry.Filename)
					if verifyErr := binary.VerifyUSE(cachedPath, entry, useState.Hash); verifyErr == nil {
						archivePath, binaryEntry, source = cachedPath, entry, "repository binary"
					} else {
						if dryRun {
							archivePath, source = filepath.Join(c.Binpkgs, r.ArchiveName()), "build from PKGBUILD"
							fmt.Fprintf(stdout, "binary unavailable in cache for %s; dry-run would fall back to PKGBUILD\n", r.Pkgname)
						} else {
							if resolvedPath, resolvedEntry, resolveErr := binary.ResolveForUSEWithTimeout(index, r.Pkgname, c.Arch, useState.Hash, c.Binpkgs, c.DownloadTimeout, stdout); resolveErr == nil {
								archivePath, binaryEntry, source = resolvedPath, resolvedEntry, "repository binary"
							} else {
								archivePath, source = filepath.Join(c.Binpkgs, r.ArchiveName()), "build from PKGBUILD"
								fmt.Fprintf(stdout, "binary unavailable for %s; falling back to PKGBUILD: %v\n", r.Pkgname, resolveErr)
							}
						}
					}
				}
			}
		}
		record, _ := state.Load(target, r.Pkgname)
		status := "new install"
		if record.Pkgname == r.Pkgname && record.Pkgver != "" {
			status = fmt.Sprintf("already installed (%s-%s -> %s-%s)", record.Pkgver, record.Pkgrel, r.Pkgver, r.Pkgrel)
		}
		if binaryEntry.Pkgver != "" {
			version, release = binaryEntry.Pkgver, binaryEntry.Pkgrel
		}
		fmt.Fprintf(stdout, "%d. %s-%s-%s: %s; %s; use=%s; use_disabled=%s; use_hash=%s\n",
			i+1, r.Pkgname, version, release, status, source,
			strings.Join(useState.Enabled, " "), strings.Join(useState.Disabled, " "), useState.Hash)
		if dryRun {
			continue
		}
		if source == "build from PKGBUILD" {
			// Only resolve/install build-time makedepends when building from PKGBUILD.
			if !sourceBuild && !rebuild && len(r.MakeDepends) > 0 {
				if makeGraph, makeErr := resolver.Resolve(r.MakeDepends...); makeErr == nil {
					for _, depName := range makeGraph.Order {
						depRecipe := makeGraph.Nodes[depName].Recipe
						depUSE := c.ResolveUSE(depRecipe.Pkgname)
						if ok, _ := state.VerifyInstalled(target, depRecipe.Pkgname, depRecipe.Pkgver, depRecipe.Pkgrel, depUSE.Hash); ok {
							continue
						}
						if err := c.EnsureDirs(); err != nil {
							return err
						}
						if err := cmdBuildRecipe(c, depRecipe, reuse, keepWork, noStrip, stdout); err != nil {
							return err
						}
						depArchive := filepath.Join(c.Binpkgs, depRecipe.ArchiveName())
						if err := installArchive(c, target, depArchive, state.Record{
							Pkgname:     depRecipe.Pkgname,
							Pkgver:      depRecipe.Pkgver,
							Pkgrel:      depRecipe.Pkgrel,
							Arch:        c.Arch,
							USEFlags:    depUSE.Enabled,
							USEDisabled: depUSE.Disabled,
							USEHash:     depUSE.Hash,
							Depends:     append([]string(nil), depRecipe.Depends...),
							Backup:      depRecipe.Backup,
						}, force, false, stdout); err != nil {
							return err
						}
					}
				} else {
					return makeErr
				}
			}
			if err := c.EnsureDirs(); err != nil {
				return err
			}
			if err := cmdBuildRecipe(c, r, reuse, keepWork, noStrip, stdout); err != nil {
				return err
			}
		}
		runtimeDeps := append([]string(nil), r.Depends...)
		if err := installArchive(c, target, archivePath, state.Record{
			Pkgname:     r.Pkgname,
			Pkgver:      version,
			Pkgrel:      release,
			Arch:        c.Arch,
			USEFlags:    useState.Enabled,
			USEDisabled: useState.Disabled,
			USEHash:     useState.Hash,
			Depends:     runtimeDeps,
			Backup:      r.Backup,
		}, force, false, stdout); err != nil {
			return err
		}
	}
	return nil
}

func installArchive(c config.Config, target, archivePath string, record state.Record, force, dryRun bool, stdout io.Writer) error {
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("package archive %q not found; build it first or place it in cache %q", archivePath, c.Binpkgs)
	}
	entries, err := archive.Entries(archivePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type == "dir" || contains(record.Backup, strings.TrimPrefix(entry.Path, "/")) {
			continue
		}
		owned, err := state.OwnedByOthers(target, record.Pkgname, entry.Path)
		if err != nil {
			return err
		}
		if owned && !force {
			return fmt.Errorf("file conflict: %s is owned by another installed package (use --force to override)", entry.Path)
		}
	}
	if dryRun {
		fmt.Fprintf(stdout, "dry-run: install %s into %s\n", archivePath, target)
		return nil
	}
	if _, err := rootfs.EnsureSkeletonIfEmpty(target); err != nil {
		return err
	}
	lock, err := state.Lock(target, true)
	if err != nil {
		return err
	}
	defer lock.Unlock()

	oldEntries, err := state.Footprint(target, record.Pkgname)
	if err != nil {
		return err
	}
	if err := archive.Extract("", archivePath, target); err != nil {
		return err
	}
	newPaths := map[string]bool{}
	for _, entry := range entries {
		newPaths[entry.Path] = true
	}
	for _, old := range oldEntries {
		if newPaths[old.Path] || strings.HasPrefix(old.Path, "/etc/") || contains(record.Backup, strings.TrimPrefix(old.Path, "/")) {
			continue
		}
		owned, err := state.OwnedByOthers(target, record.Pkgname, old.Path)
		if err != nil {
			return err
		}
		if owned {
			continue
		}
		path, err := rootfs.TargetPath(target, old.Path)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale package path %q: %w", old.Path, err)
		}
	}
	for i := len(oldEntries) - 1; i >= 0; i-- {
		old := oldEntries[i]
		if old.Type != "dir" || newPaths[old.Path] {
			continue
		}
		owned, err := state.OwnedByOthers(target, record.Pkgname, old.Path)
		if err != nil {
			return err
		}
		if owned {
			continue
		}
		path, err := rootfs.TargetPath(target, old.Path)
		if err == nil {
			_ = os.Remove(path)
		}
	}
	if err := state.Write(target, record, entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Path, "/lib/") || strings.HasPrefix(entry.Path, "/lib64/") ||
			strings.HasPrefix(entry.Path, "/usr/lib/") || strings.HasPrefix(entry.Path, "/usr/lib64/") {
			if err := rootfs.RunLDConfig(target); err != nil {
				fmt.Fprintf(stdout, "warning: refresh dynamic linker cache: %v\n", err)
			}
			break
		}
	}
	fmt.Fprintf(stdout, "installed %s into %s\n", archivePath, target)
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func findPackageArchive(dir, name string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, name+"-*.pkg.tar.zst"))
	matches = filterRegularFiles(matches)
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		return comparePackageVersions(packageVersion(filepath.Base(matches[i]), name), packageVersion(filepath.Base(matches[j]), name)) < 0
	})
	return matches[len(matches)-1]
}

func findPackageArchiveForUSE(dir, name, arch, useHash string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, name+"-*.pkg.tar.zst"))
	sort.Slice(matches, func(i, j int) bool {
		return comparePackageVersions(packageVersion(filepath.Base(matches[i]), name), packageVersion(filepath.Base(matches[j]), name)) < 0
	})
	for i := len(matches) - 1; i >= 0; i-- {
		path := matches[i]
		if !isRegularFile(path) {
			continue
		}
		info, err := archive.ReadInfo(path)
		if err != nil {
			return path
		}
		if info.USEHash != "" && useHash != "" && info.USEHash != useHash {
			continue
		}
		if info.Arch != "" && arch != "" && info.Arch != arch {
			continue
		}
		return path
	}
	return ""
}

func selectInstallArchive(cache string, recipe pkgbuild.Recipe) (string, string) {
	exact := filepath.Join(cache, recipe.ArchiveName())
	if isRegularFile(exact) {
		return exact, "existing archive"
	}
	if archivePath := findPackageArchive(cache, recipe.Pkgname); archivePath != "" {
		return archivePath, "existing archive"
	}
	return exact, "build from PKGBUILD"
}

func selectInstallArchiveForUSE(cache string, recipe pkgbuild.Recipe, arch, useHash string) (string, string) {
	candidates := []string{filepath.Join(cache, recipe.ArchiveName())}
	if matches, _ := filepath.Glob(filepath.Join(cache, recipe.Pkgname+"-*.pkg.tar.zst")); len(matches) > 0 {
		candidates = append(candidates, matches...)
	}
	for _, path := range candidates {
		if !isRegularFile(path) {
			continue
		}
		info, err := archive.ReadInfo(path)
		if err != nil {
			return path, "existing archive" // legacy archive without metadata
		}
		if info.USEHash != "" && useHash != "" && info.USEHash != useHash {
			continue
		}
		if info.Arch != "" && arch != "" && info.Arch != arch {
			continue
		}
		return path, "existing archive"
	}
	return filepath.Join(cache, recipe.ArchiveName()), "build from PKGBUILD"
}

func filterRegularFiles(paths []string) []string {
	filtered := paths[:0]
	for _, path := range paths {
		if isRegularFile(path) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func packageVersion(filename, name string) string {
	prefix := name + "-"
	return strings.TrimSuffix(strings.TrimPrefix(filename, prefix), ".pkg.tar.zst")
}

func comparePackageVersions(a, b string) int {
	pa, pb := strings.Split(a, "-"), strings.Split(b, "-")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		var ai, bi int
		if _, err := fmt.Sscanf(pa[i], "%d", &ai); err == nil {
			if _, err := fmt.Sscanf(pb[i], "%d", &bi); err == nil && ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		}
		if pa[i] < pb[i] {
			return -1
		}
		return 1
	}
	if len(pa) < len(pb) {
		return -1
	}
	if len(pa) > len(pb) {
		return 1
	}
	return 0
}

func cmdRemove(c config.Config, args []string, allowHost, force, dryRun bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("remove requires a package name")
	}
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	if !force {
		records, err := state.Installed(target)
		if err != nil {
			return err
		}
		var dependents []string
		for _, record := range records {
			for _, dep := range record.Depends {
				fields := strings.Fields(dep)
				if len(fields) == 0 {
					continue
				}
				depName := strings.TrimSpace(fields[0])
				for _, op := range []string{">=", "<=", ">", "<", "="} {
					if i := strings.Index(depName, op); i >= 0 {
						depName = depName[:i]
						break
					}
				}
				if depName == args[0] {
					dependents = append(dependents, record.Pkgname)
					break
				}
			}
		}
		if len(dependents) > 0 {
			return fmt.Errorf("cannot remove %q: installed packages depend on it: %s (use --force)", args[0], strings.Join(dependents, ", "))
		}
	}
	if dryRun {
		fmt.Fprintf(stdout, "dry-run: remove package %s from %s\n", args[0], target)
		return nil
	}
	lock, err := state.Lock(target, true)
	if err != nil {
		return err
	}
	defer lock.Unlock()
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
		owned, err := state.OwnedByOthers(target, args[0], entry.Path)
		if err != nil {
			return err
		}
		if owned {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove package-owned file %q: %w", path, err)
		}
	}
	if err := os.Remove(footprintPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove package footprint %q: %w", footprintPath, err)
	}
	recordDir, _ = state.RecordDir(target)
	_ = os.Remove(filepath.Join(recordDir, args[0]+".meta"))
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
