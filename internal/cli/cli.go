package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ezke5/pi/internal/archive"
	"github.com/ezke5/pi/internal/config"
	"github.com/ezke5/pi/internal/deps"
	"github.com/ezke5/pi/internal/fetch"
	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/pkgbuild"
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
		return cmdBuild(c, pos[1:], *reuse, *dryRun, stdout)
	case "install":
		return cmdInstall(c, pos[1:], *allowHost, *force, *dryRun, *reuse, stdout)
	case "remove":
		return cmdRemove(c, pos[1:], *allowHost, *force, *dryRun, stdout)
	case "list":
		return cmdList(c, *allowHost, stdout)
	default:
		return fmt.Errorf("unknown subcommand %q; run 'pi help' for usage", pos[0])
	}
}

func splitFlags(args []string) ([]string, []string) {
	known := map[string]bool{"--ports": true, "--work": true, "--temp": true, "--cache": true, "--root": true, "--config": true, "--allow-host-root": true, "--dry-run": true, "--reuse": true, "--force": true}
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
	fmt.Fprintln(w, "  --dry-run, --allow-host-root, --force")
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

func cmdBuild(c config.Config, args []string, reuse bool, dryRun bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("build requires a PKGBUILD path or package name")
	}
	graph, err := (deps.Resolver{Ports: c.Ports}).Resolve(args[0])
	if err != nil {
		return err
	}
	for i, r := range graph.Recipes() {
		fmt.Fprintf(stdout, "%d. %s-%s-%s (build from PKGBUILD)\n", i+1, r.Pkgname, r.Pkgver, r.Pkgrel)
	}
	if dryRun {
		return nil
	}
	if err := c.EnsureDirs(); err != nil {
		return err
	}
	for _, r := range graph.Recipes() {
		if err := cmdBuildRecipe(c, r, reuse, stdout); err != nil {
			return err
		}
	}
	return nil
}

func cmdBuildRecipe(c config.Config, r pkgbuild.Recipe, reuse bool, stdout io.Writer) error {
	buildDir := filepath.Join(c.Work, r.Pkgname+"-"+r.Pkgver)
	srcDir := filepath.Join(buildDir, "src")
	pkgDir := filepath.Join(buildDir, "pkg")
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
		sourceName := ""
		if strings.Contains(source, "::") {
			parts := strings.SplitN(source, "::", 2)
			sourceName, rawURL = parts[0], parts[1]
		}
		var local string
		if strings.Contains(rawURL, "://") {
			base := filepath.Base(strings.SplitN(rawURL, "?", 2)[0])
			if sourceName == "" {
				sourceName = base
			}
			local = filepath.Join(c.Cache, base)
			if info, statErr := os.Stat(local); statErr == nil && !info.IsDir() {
				shouldReuse := reuse
				if !reuse {
					if fileInfo, infoErr := os.Stdin.Stat(); infoErr == nil && fileInfo.Mode()&os.ModeCharDevice != 0 {
						shouldReuse, infoErr = fetch.ConfirmReuse(local, os.Stdin, stdout)
						if infoErr != nil {
							return infoErr
						}
					}
				}
				if shouldReuse {
					fmt.Fprintf(stdout, "reusing cached source %s\n", local)
				} else if err := os.Remove(local); err != nil {
					return fmt.Errorf("remove cached source %q: %w", local, err)
				} else if err := fetch.Download(rawURL, local, 30*time.Second); err != nil {
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
			if sourceName == "" {
				sourceName = filepath.Base(local)
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

	cmdScript := "#!/bin/bash\nset -e\n" + fmt.Sprintf("unset CONFIG_SITE\nexport pkgname=%q\nexport pkgver=%q\nexport pkgrel=%q\nexport srcdir=%q\nexport pkgdir=%q\n", r.Pkgname, r.Pkgver, r.Pkgrel, srcDir, pkgDir)
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

func cmdInstall(c config.Config, args []string, allowHost, force, dryRun, reuse bool, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("install requires a .pkg.tar.zst archive or package name")
	}
	target, err := rootfs.Validate(c.Root, allowHost)
	if err != nil {
		return err
	}
	graph, err := (deps.Resolver{Ports: c.Ports}).Resolve(args[0])
	if err != nil {
		if strings.HasSuffix(args[0], ".pkg.tar.zst") {
			graph = deps.Graph{}
		} else {
			return err
		}
	}
	if len(graph.Order) == 0 {
		archivePath := args[0]
		if _, statErr := os.Stat(archivePath); statErr != nil {
			archivePath = filepath.Join(c.Binpkgs, filepath.Base(args[0]))
		}
		return installArchive(c, target, archivePath, state.Record{Pkgname: strings.TrimSuffix(filepath.Base(archivePath), ".pkg.tar.zst")}, force, dryRun, stdout)
	}
	for i, name := range graph.Order {
		r := graph.Nodes[name].Recipe
		archivePath := filepath.Join(c.Binpkgs, r.ArchiveName())
		source := "existing archive"
		if _, statErr := os.Stat(archivePath); os.IsNotExist(statErr) {
			archivePath = findPackageArchive(c.Binpkgs, r.Pkgname)
		}
		if archivePath == "" {
			source = "build from PKGBUILD"
			archivePath = filepath.Join(c.Binpkgs, r.ArchiveName())
		}
		record, _ := state.Load(target, r.Pkgname)
		status := "new install"
		if record.Pkgname == r.Pkgname && record.Pkgver != "" {
			status = fmt.Sprintf("already installed (%s-%s -> %s-%s)", record.Pkgver, record.Pkgrel, r.Pkgver, r.Pkgrel)
		}
		fmt.Fprintf(stdout, "%d. %s-%s-%s: %s; %s\n", i+1, r.Pkgname, r.Pkgver, r.Pkgrel, status, source)
		if dryRun {
			continue
		}
		if source == "build from PKGBUILD" {
			if err := c.EnsureDirs(); err != nil {
				return err
			}
			if err := cmdBuildRecipe(c, r, reuse, stdout); err != nil {
				return err
			}
		}
		if err := installArchive(c, target, archivePath, state.Record{Pkgname: r.Pkgname, Pkgver: r.Pkgver, Pkgrel: r.Pkgrel, Depends: append(r.Depends, r.MakeDepends...), Backup: r.Backup}, force, false, stdout); err != nil {
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
		path, err := rootfs.TargetPath(target, old.Path)
		if err == nil {
			_ = os.Remove(path)
		}
	}
	if err := state.Write(target, record, entries); err != nil {
		return err
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
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		return comparePackageVersions(packageVersion(filepath.Base(matches[i]), name), packageVersion(filepath.Base(matches[j]), name)) < 0
	})
	return matches[len(matches)-1]
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
				if len(fields) > 0 && strings.TrimSpace(fields[0]) == args[0] {
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
