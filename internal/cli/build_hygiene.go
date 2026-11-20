package cli

import (
	"bufio"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ezke5/pi/internal/config"
	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/pkgbuild"
)

var stagingJunkPaths = []string{
	"usr/share/info/dir",
	"usr/lib/charset.alias",
	"usr/lib64/charset.alias",
}

func checkWritableDirs(dirs ...string) error {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("inspect directory %q: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("path %q is not a directory", dir)
		}
		probe, err := os.CreateTemp(dir, ".pi-write-check-*")
		if err != nil {
			return fmt.Errorf("directory %q is not writable: %w", dir, err)
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
	}
	return nil
}

// buildEnv constructs a clean, deterministic environment for build()/package()
// (mirroring CRUX pkgmk hygiene) so host variables like LD_LIBRARY_PATH,
// PKG_CONFIG_PATH, DESTDIR, or CONFIG_SITE cannot leak into recipes.
func buildEnv(c config.Config, buildDir, srcDir, pkgDir string, r pkgbuild.Recipe, useState config.USEState) []string {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	termEnv := os.Getenv("TERM")
	if termEnv == "" {
		termEnv = "xterm"
	}
	jobs, cflags, cxxflags, ldflags, strip := effectiveBuildSettings(c)
	env := []string{
		"PATH=" + pathEnv,
		"TERM=" + termEnv,
		"LC_ALL=C.UTF-8",
		"HOME=" + filepath.Join(buildDir, "home"),
		"TMPDIR=" + filepath.Join(buildDir, "tmp"),
		"pkgname=" + r.Pkgname,
		"pkgver=" + r.Pkgver,
		"pkgrel=" + r.Pkgrel,
		"srcdir=" + srcDir,
		"pkgdir=" + pkgDir,
		fmt.Sprintf("JOBS=%d", jobs),
		fmt.Sprintf("MAKEFLAGS=-j%d", jobs),
		"CFLAGS=" + cflags,
		"CXXFLAGS=" + cxxflags,
		"LDFLAGS=" + ldflags,
		"STRIP=" + strip,
		"PI_USE_FLAGS=" + strings.Join(useState.Enabled, " "),
		"PI_USE_DISABLED=" + strings.Join(useState.Disabled, " "),
		"PI_USE_HASH=" + useState.Hash,
	}
	return env
}

func effectiveBuildSettings(c config.Config) (jobs int, cflags, cxxflags, ldflags, strip string) {
	jobs, cflags, cxxflags, ldflags, strip = c.Build.Jobs, c.Build.CFLAGS, c.Build.CXXFLAGS, c.Build.LDFLAGS, c.Build.Strip
	if jobs == 0 {
		jobs = c.Jobs
	}
	if jobs <= 0 {
		jobs = 1
	}
	if cflags == "" {
		cflags = c.CFLAGS
	}
	if cxxflags == "" {
		cxxflags = c.CXXFLAGS
	}
	if ldflags == "" {
		ldflags = c.LDFLAGS
	}
	if strip == "" {
		strip = c.STRIP
	}
	return jobs, cflags, cxxflags, ldflags, strip
}

func stripEnabled(stripVal string, noStrip bool) bool {
	if noStrip {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(stripVal)) {
	case "true", "yes", "1", "on":
		return true
	default:
		return false
	}
}

func cleanStagingJunk(pkgDir string) error {
	for _, rel := range stagingJunkPaths {
		p := filepath.Join(pkgDir, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove staging junk %q: %w", rel, err)
		}
	}
	return nil
}

// stripStagingFiles strips ELF executables (--strip-all), shared objects
// (--strip-unneeded), and static ar archives (--strip-debug) while respecting
// an optional CRUX-style recipe .nostrip regex file.
func stripStagingFiles(pkgDir, recipeDir string, out io.Writer) error {
	stripBin, err := exec.LookPath("strip")
	if err != nil {
		return nil
	}
	patterns, err := loadNoStripPatterns(filepath.Join(recipeDir, ".nostrip"))
	if err != nil {
		return err
	}
	return filepath.Walk(pkgDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(pkgDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		for _, re := range patterns {
			if re.MatchString(relSlash) {
				return nil
			}
		}
		stripFlag := classifyStripFlag(path)
		if stripFlag == "" {
			return nil
		}
		origPerm := info.Mode().Perm()
		madeWritable := false
		if origPerm&0o200 == 0 {
			if err := os.Chmod(path, origPerm|0o200); err != nil {
				return fmt.Errorf("make %q writable for strip: %w", relSlash, err)
			}
			madeWritable = true
		}
		cmd := exec.Command(stripBin, stripFlag, path)
		if cmdOut, runErr := cmd.CombinedOutput(); runErr != nil {
			if madeWritable {
				_ = os.Chmod(path, origPerm)
			}
			fmt.Fprintf(out, "warning: strip %s %s failed: %v (%s)\n", stripFlag, relSlash, runErr, strings.TrimSpace(string(cmdOut)))
			return nil
		}
		if madeWritable {
			if err := os.Chmod(path, origPerm); err != nil {
				return fmt.Errorf("restore mode on stripped %q: %w", relSlash, err)
			}
		}
		return nil
	})
}

func loadNoStripPatterns(path string) ([]*regexp.Regexp, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open .nostrip %q: %w", path, err)
	}
	defer f.Close()
	var patterns []*regexp.Regexp
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, err := regexp.Compile(line)
		if err != nil {
			return nil, fmt.Errorf("compile .nostrip %q line %d: %w", path, lineNo, err)
		}
		patterns = append(patterns, re)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read .nostrip %q: %w", path, err)
	}
	return patterns, nil
}

func classifyStripFlag(path string) string {
	if ef, err := elf.Open(path); err == nil {
		typ := ef.Type
		_ = ef.Close()
		switch typ {
		case elf.ET_EXEC:
			return "--strip-all"
		case elf.ET_DYN:
			return "--strip-unneeded"
		}
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var magic [8]byte
	n, err := io.ReadFull(f, magic[:])
	if err == nil && n == 8 && string(magic[:]) == "!<arch>\n" {
		return "--strip-debug"
	}
	return ""
}

func diffRecipeFootprint(recipeDir string, actual []footprint.Entry, out io.Writer) {
	expectedPath := filepath.Join(recipeDir, ".footprint")
	if _, err := os.Stat(expectedPath); err != nil {
		return
	}
	expected, err := footprint.Read(expectedPath)
	if err != nil {
		fmt.Fprintf(out, "warning: could not read recipe .footprint: %v\n", err)
		return
	}
	expSet := make(map[string]footprint.Entry, len(expected))
	for _, e := range expected {
		expSet[e.Path] = e
	}
	actSet := make(map[string]footprint.Entry, len(actual))
	for _, e := range actual {
		actSet[e.Path] = e
	}
	var added, missing []string
	for p := range actSet {
		if _, ok := expSet[p]; !ok {
			added = append(added, p)
		}
	}
	for p := range expSet {
		if _, ok := actSet[p]; !ok {
			missing = append(missing, p)
		}
	}
	sort.Strings(added)
	sort.Strings(missing)
	if len(added) == 0 && len(missing) == 0 {
		return
	}
	fmt.Fprintf(out, "warning: footprint differences against %s:\n", expectedPath)
	for _, p := range missing {
		fmt.Fprintf(out, "MISSING   %s\n", p)
	}
	for _, p := range added {
		fmt.Fprintf(out, "NEW       %s\n", p)
	}
}
