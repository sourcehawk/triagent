package watches

import "testing"

func TestApplyFiltersContains(t *testing.T) {
	it := Item{Snapshot: Snapshot{Title: "Engine OOMs in prod"}}
	f := Filter{Field: "title", Op: "contains", Value: "OOM"}
	if got := ApplyFilters(it, []Filter{f}); got != nil {
		t.Fatalf("clean item should pass, got %+v", got)
	}
	f2 := Filter{Field: "title", Op: "contains", Value: "Webhook"}
	if got := ApplyFilters(it, []Filter{f2}); got == nil {
		t.Fatal("title without 'Webhook' should be filtered")
	}
}

func TestApplyFiltersANDs(t *testing.T) {
	it := Item{Snapshot: Snapshot{Title: "OOM", Body: "boring"}}
	fs := []Filter{
		{Field: "title", Op: "contains", Value: "OOM"},
		{Field: "body", Op: "contains", Value: "interesting"},
	}
	got := ApplyFilters(it, fs)
	if got == nil || got.RuleIndex != 1 {
		t.Fatalf("expected filter on rule 1, got %+v", got)
	}
}

func TestApplyFiltersRegex(t *testing.T) {
	it := Item{Snapshot: Snapshot{Body: "duplicate of #4421"}}
	f := Filter{Field: "body", Op: "not_regex_matches", Value: `(?i)\bduplicate of\b`}
	if got := ApplyFilters(it, []Filter{f}); got == nil {
		t.Fatal("not_regex_matches should filter when regex matches")
	}
}

func TestApplyFiltersLabelMatchAny(t *testing.T) {
	it := Item{Snapshot: Snapshot{Labels: []string{"bug", "p1"}}}
	f := Filter{Field: "label", Op: "contains", Value: "p1"}
	if got := ApplyFilters(it, []Filter{f}); got != nil {
		t.Fatalf("expected match-any to pass, got %+v", got)
	}
	f2 := Filter{Field: "label", Op: "contains", Value: "blocker"}
	if got := ApplyFilters(it, []Filter{f2}); got == nil {
		t.Fatal("expected mismatch to filter")
	}
}
