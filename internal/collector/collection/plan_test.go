package collection

import (
	"sync"
	"testing"
)

func TestBuildImmutableOrderedPlan(t *testing.T) {
	definitions := []QuerySpec{{"b", "one", "SELECT 3"}, {"a", "first", "SELECT 1"}, {"a", "second", "SELECT 2"}}
	builder, err := NewBuilder(definitions)
	if err != nil {
		t.Fatal(err)
	}
	definitions[1].SQL = "changed"
	names := []string{"b", "a", "b"}
	plan, err := builder.Build(names)
	if err != nil {
		t.Fatal(err)
	}
	names[0] = "changed"
	if plan.Len() != 3 || plan.Query(0).SQL != "SELECT 1" || plan.Query(1).ResultKey != "second" || plan.Query(2).Extension != "b" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	query := plan.Query(0)
	query.SQL = "changed"
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			other, err := builder.Build([]string{"a", "b"})
			if err != nil || other.Query(0) != plan.Query(0) {
				t.Errorf("shared plan changed: %v", err)
			}
		})
	}
	wg.Wait()
}

func TestValidation(t *testing.T) {
	for _, definitions := range [][]QuerySpec{
		{{"", "one", "SELECT 1"}}, {{"a", " ", "SELECT 1"}}, {{"a", "one", " "}},
		{{"a", "one", "SELECT 1"}, {"a", "one", "SELECT 2"}},
	} {
		if _, err := NewBuilder(definitions); err == nil {
			t.Errorf("accepted invalid definitions: %+v", definitions)
		}
	}
	b, _ := NewBuilder([]QuerySpec{{"a", "one", "SELECT 1"}, {"b", "one", "SELECT 2"}})
	for _, names := range [][]string{nil, {}, {"missing"}} {
		if _, err := b.Build(names); err == nil {
			t.Errorf("accepted names: %v", names)
		}
	}
	var absent *Builder
	if _, err := absent.Build([]string{"a"}); err == nil {
		t.Fatal("accepted nil builder")
	}
	var plan *Plan
	if plan.Len() != 0 {
		t.Fatal("nil plan has queries")
	}
}
