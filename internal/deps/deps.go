// Package deps resolves PKGBUILD dependency graphs.
package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ezke5/pi/internal/pkgbuild"
)

type Node struct {
	Recipe pkgbuild.Recipe
	Deps   []string
}

type Graph struct {
	Nodes map[string]Node
	Order []string
}

type Resolver struct {
	Ports string
}

func (r Resolver) Resolve(names ...string) (Graph, error) {
	return r.resolve(true, names...)
}

// ResolveRuntime resolves only runtime Depends (excluding build-only
// MakeDepends), suitable when installing prebuilt binary archives.
func (r Resolver) ResolveRuntime(names ...string) (Graph, error) {
	return r.resolve(false, names...)
}

func (r Resolver) resolve(includeMakeDepends bool, names ...string) (Graph, error) {
	g := Graph{Nodes: map[string]Node{}}
	visiting := map[string]int{}
	var stack []string
	var visit func(string, string) error
	visit = func(name, requiredBy string) error {
		rawName := name
		name = dependencyName(name)
		if name == "" {
			return nil
		}
		if visiting[name] == 1 {
			start := 0
			for i, item := range stack {
				if item == name {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), name)
			return fmt.Errorf("dependency cycle: %s", strings.Join(cycle, " -> "))
		}
		if visiting[name] == 2 {
			return nil
		}
		path, err := pkgbuild.Find(r.Ports, name)
		if err != nil {
			if info, statErr := os.Stat(rawName); statErr == nil && !info.IsDir() {
				path = rawName
				err = nil
			}
		}
		if err != nil {
			if requiredBy != "" {
				return fmt.Errorf("missing dependency %q required by %q: %w", name, requiredBy, err)
			}
			return err
		}
		recipe, err := pkgbuild.Read(path)
		if err != nil {
			return err
		}
		if recipe.Pkgname != name {
			name = recipe.Pkgname
			if visiting[name] == 2 {
				return nil
			}
		}
		visiting[name] = 1
		stack = append(stack, name)
		all := append([]string{}, recipe.Depends...)
		if includeMakeDepends {
			all = append(all, recipe.MakeDepends...)
		}
		sort.Strings(all)
		for _, dep := range all {
			if err := visit(dep, name); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		visiting[name] = 2
		g.Nodes[name] = Node{Recipe: recipe, Deps: dependencyNames(all)}
		g.Order = append(g.Order, name)
		return nil
	}
	for _, name := range names {
		if err := visit(name, ""); err != nil {
			return Graph{}, err
		}
	}
	return g, nil
}

func dependencyNames(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if name := dependencyName(value); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// dependencyName removes CRUX/pkg-config style version constraints.
func dependencyName(value string) string {
	value = strings.TrimSpace(value)
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if i := strings.Index(value, op); i >= 0 {
			value = value[:i]
		}
	}
	return strings.TrimSpace(filepath.Base(value))
}

func (g Graph) Recipes() []pkgbuild.Recipe {
	out := make([]pkgbuild.Recipe, 0, len(g.Order))
	for _, name := range g.Order {
		out = append(out, g.Nodes[name].Recipe)
	}
	return out
}
