// Package collection defines immutable instructions shared by scheduled jobs.
package collection

import (
	"fmt"
	"slices"
	"strings"
)

// QuerySpec is trusted application SQL. Each statement must return one result set.
// Queries must be read-only and must not include transaction control statements.
type QuerySpec struct {
	Extension string
	ResultKey string
	SQL       string
}

// Plan contains no execution state and may be shared by concurrent jobs.
type Plan struct{ queries []QuerySpec }

func (p *Plan) Len() int {
	if p == nil {
		return 0
	}
	return len(p.queries)
}

// Query returns a value copy of the query at index i.
func (p *Plan) Query(i int) QuerySpec { return p.queries[i] }

type Builder struct{ definitions map[string][]QuerySpec }

// NewBuilder copies and validates the registry; it does not validate SQL syntax.
func NewBuilder(definitions []QuerySpec) (*Builder, error) {
	b := &Builder{definitions: make(map[string][]QuerySpec)}
	for _, q := range definitions {
		if strings.TrimSpace(q.Extension) == "" || strings.TrimSpace(q.ResultKey) == "" || strings.TrimSpace(q.SQL) == "" {
			return nil, fmt.Errorf("query definition requires extension, result key, and SQL")
		}
		for _, existing := range b.definitions[q.Extension] {
			if existing.ResultKey == q.ResultKey {
				return nil, fmt.Errorf("duplicate result key %q for extension %q", q.ResultKey, q.Extension)
			}
		}
		b.definitions[q.Extension] = append(b.definitions[q.Extension], q)
	}
	return b, nil
}

func (b *Builder) Build(extensionNames []string) (*Plan, error) {
	if b == nil {
		return nil, fmt.Errorf("collection builder is not configured")
	}
	if len(extensionNames) == 0 {
		return nil, fmt.Errorf("collection plan requires at least one extension")
	}
	names := slices.Clone(extensionNames)
	slices.Sort(names)
	names = slices.Compact(names)
	p := &Plan{}
	for _, name := range names {
		definitions, ok := b.definitions[name]
		if !ok {
			return nil, fmt.Errorf("unsupported collection extension %q", name)
		}
		p.queries = append(p.queries, definitions...)
	}
	return p, nil
}
