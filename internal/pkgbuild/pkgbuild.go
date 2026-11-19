// Package pkgbuild reads simple PKGBUILD recipes.
package pkgbuild

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Recipe struct {
	Path            string
	Pkgname         string
	Pkgver          string
	Pkgrel          string
	Source          []string
	SHA256Sums      []string
	Depends         []string
	BuildFunction   string
	PackageFunction string
}

var scalarRE = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$`)

func splitShellWords(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "()")
	var result []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, r := range value {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

func Read(path string) (Recipe, error) {
	f, err := os.Open(path)
	if err != nil {
		return Recipe{}, fmt.Errorf("open PKGBUILD %q: %w", path, err)
	}
	defer f.Close()
	recipe := Recipe{Path: path, Pkgrel: "1"}
	scanner := bufio.NewScanner(f)
	var function string
	var functionLines []string
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if function != "" {
			if trimmed == "}" {
				body := strings.Join(functionLines, "\n")
				if function == "build" {
					recipe.BuildFunction = body
				}
				if function == "package" {
					recipe.PackageFunction = body
				}
				function = ""
				functionLines = nil
				continue
			}
			functionLines = append(functionLines, line)
			continue
		}
		if strings.HasPrefix(trimmed, "build()") {
			function = "build"
			continue
		}
		if strings.HasPrefix(trimmed, "package()") {
			function = "package"
			continue
		}
		m := scalarRE.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		switch m[1] {
		case "pkgname":
			recipe.Pkgname = strings.Trim(m[2], `"' `)
		case "pkgver":
			recipe.Pkgver = strings.Trim(m[2], `"' `)
		case "pkgrel":
			recipe.Pkgrel = strings.Trim(m[2], `"' `)
		case "source":
			recipe.Source = splitShellWords(m[2])
		case "sha256sums":
			recipe.SHA256Sums = splitShellWords(m[2])
		case "depends":
			recipe.Depends = splitShellWords(m[2])
		}
	}
	if err := scanner.Err(); err != nil {
		return Recipe{}, fmt.Errorf("read PKGBUILD %q: %w", path, err)
	}
	if recipe.Pkgname == "" {
		return Recipe{}, fmt.Errorf("PKGBUILD %q is missing pkgname", path)
	}
	if recipe.Pkgver == "" {
		return Recipe{}, fmt.Errorf("PKGBUILD %q is missing pkgver", path)
	}
	if len(recipe.Source) != len(recipe.SHA256Sums) && len(recipe.SHA256Sums) != 0 {
		return Recipe{}, fmt.Errorf("PKGBUILD %q: source count (%d) differs from sha256sums count (%d)", path, len(recipe.Source), len(recipe.SHA256Sums))
	}
	return recipe, nil
}

func (r Recipe) ArchiveName() string {
	return fmt.Sprintf("%s-%s-%s.pkg.tar.zst", r.Pkgname, r.Pkgver, r.Pkgrel)
}

func Find(ports, name string) (string, error) {
	candidates := []string{
		filepath.Join(ports, name, "PKGBUILD"),
		filepath.Join(ports, name+".PKGBUILD"),
		name,
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("PKGBUILD for package %q not found in ports directory %q or as a direct path", name, ports)
}
