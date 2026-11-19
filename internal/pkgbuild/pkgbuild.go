// Package pkgbuild reads simple PKGBUILD recipes.
package pkgbuild

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	MakeDepends     []string
	Noextract       []string
	Backup          []string
	BuildFunction   string
	PackageFunction string
}

var scalarRE = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$`)
var heredocRE = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)`)
var recipeVariableRE = regexp.MustCompile(`\$\{(pkgname|pkgver|pkgrel)\}|\$(pkgname|pkgver|pkgrel)\b`)

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
		if r == ' ' || r == '\t' || r == '\n' {
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
	var function, heredoc, assignment string
	var functionLines, assignmentLines []string
	braceDepth := 0
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if function != "" {
			if heredoc != "" {
				functionLines = append(functionLines, line)
				if strings.TrimSpace(line) == heredoc {
					heredoc = ""
				}
				continue
			}
			if delimiter := heredocDelimiter(line); delimiter != "" {
				heredoc = delimiter
			}
			braceDepth += shellBraceDelta(line)
			if braceDepth == 0 {
				if function == "build" {
					recipe.BuildFunction = strings.Join(functionLines, "\n")
				} else {
					recipe.PackageFunction = strings.Join(functionLines, "\n")
				}
				function, functionLines = "", nil
				continue
			}
			functionLines = append(functionLines, line)
			continue
		}
		if strings.HasPrefix(trimmed, "build()") {
			function, braceDepth = "build", shellBraceDelta(line)
			continue
		}
		if strings.HasPrefix(trimmed, "package()") {
			function, braceDepth = "package", shellBraceDelta(line)
			continue
		}
		if assignment != "" {
			assignmentLines = append(assignmentLines, line)
			value := strings.Join(assignmentLines, "\n")
			if balancedArray(value) {
				setArray(&recipe, assignment, value)
				assignment, assignmentLines = "", nil
			}
			continue
		}
		m := scalarRE.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(m[2]), "(") && !balancedArray(m[2]) {
			assignment, assignmentLines = m[1], []string{m[2]}
			continue
		}
		switch m[1] {
		case "pkgname":
			recipe.Pkgname = strings.Trim(m[2], `"' `)
		case "pkgver":
			recipe.Pkgver = strings.Trim(m[2], `"' `)
		case "pkgrel":
			recipe.Pkgrel = strings.Trim(m[2], `"' `)
		default:
			setArray(&recipe, m[1], m[2])
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
	recipe.Source = expandRecipeVariables(recipe.Source, recipe)
	return recipe, nil
}

func expandRecipeVariables(sources []string, recipe Recipe) []string {
	values := map[string]string{
		"pkgname": recipe.Pkgname,
		"pkgver":  recipe.Pkgver,
		"pkgrel":  recipe.Pkgrel,
	}
	expanded := make([]string, len(sources))
	for i, source := range sources {
		expanded[i] = recipeVariableRE.ReplaceAllStringFunc(source, func(variable string) string {
			name := strings.TrimPrefix(strings.TrimSuffix(variable, "}"), "${")
			if !strings.HasPrefix(variable, "${") {
				name = strings.TrimPrefix(variable, "$")
			}
			return values[name]
		})
	}
	return expanded
}

func setArray(recipe *Recipe, name, value string) {
	switch name {
	case "source":
		recipe.Source = splitShellWords(value)
	case "sha256sums":
		recipe.SHA256Sums = splitShellWords(value)
	case "depends":
		recipe.Depends = splitShellWords(value)
	case "makedepends":
		recipe.MakeDepends = splitShellWords(value)
	case "noextract":
		recipe.Noextract = splitShellWords(value)
	case "backup":
		recipe.Backup = splitShellWords(value)
	}
}

func balancedArray(value string) bool {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range value {
		if escaped {
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
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
		} else if r == '(' {
			depth++
		} else if r == ')' {
			depth--
		}
	}
	return depth == 0
}

func heredocDelimiter(line string) string {
	m := heredocRE.FindStringSubmatch(line)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func shellBraceDelta(line string) int {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range line {
		if escaped {
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
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '#' {
			break
		}
		if r == '{' {
			depth++
		} else if r == '}' {
			depth--
		}
	}
	return depth
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
	entries, err := os.ReadDir(ports)
	if err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(ports, entry.Name(), name, "PKGBUILD")
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("PKGBUILD for package %q not found in ports directory %q or as a direct path", name, ports)
}
