// Package engine orders stacks by dependency and plans, applies and destroys them.
package engine

import (
	"fmt"
	"slices"
	"strings"
)

// Stack is one deployable unit and the stacks it needs first.
type Stack struct {
	Name      string
	DependsOn []string
}

// Graph is a validated, acyclic set of stacks.
type Graph struct {
	stacks map[string]Stack
}

// CycleError reports a dependency cycle as a path that starts and ends on the same stack.
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return "dependency cycle: " + strings.Join(e.Path, " → ")
}

// NewGraph checks for duplicate names, unknown dependencies and cycles.
func NewGraph(stacks []Stack) (*Graph, error) {
	g := &Graph{stacks: make(map[string]Stack, len(stacks))}
	for _, s := range stacks {
		if s.Name == "" {
			return nil, fmt.Errorf("stack with empty name")
		}
		if _, dup := g.stacks[s.Name]; dup {
			return nil, fmt.Errorf("stack %q defined twice", s.Name)
		}
		g.stacks[s.Name] = s
	}
	for _, s := range stacks {
		for _, d := range s.DependsOn {
			if _, ok := g.stacks[d]; !ok {
				return nil, fmt.Errorf("stack %q depends on unknown stack %q", s.Name, d)
			}
		}
	}
	if _, err := g.Order(g.Names()...); err != nil {
		return nil, err
	}
	return g, nil
}

// Names returns every stack name, sorted.
func (g *Graph) Names() []string {
	names := make([]string, 0, len(g.stacks))
	for n := range g.stacks {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Has reports whether the graph defines the stack.
func (g *Graph) Has(name string) bool {
	_, ok := g.stacks[name]
	return ok
}

// Order returns the named stacks plus everything they depend on, with each
// stack after its dependencies. The order is deterministic: ties follow the
// order of the requested names, then each stack's DependsOn order.
func (g *Graph) Order(names ...string) ([]string, error) {
	const (
		unvisited = iota
		visiting
		done
	)
	mark := map[string]int{}
	var order, path []string
	var visit func(string) error
	visit = func(n string) error {
		switch mark[n] {
		case done:
			return nil
		case visiting:
			start := slices.Index(path, n)
			return &CycleError{Path: append(slices.Clone(path[start:]), n)}
		}
		s, ok := g.stacks[n]
		if !ok {
			return fmt.Errorf("unknown stack %q", n)
		}
		mark[n] = visiting
		path = append(path, n)
		for _, d := range s.DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		mark[n] = done
		order = append(order, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}
