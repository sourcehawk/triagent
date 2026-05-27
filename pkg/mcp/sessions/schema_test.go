package sessions

import (
	"strings"
	"testing"
)

func TestValidateFrontmatter(t *testing.T) {
	good := Frontmatter{
		SchemaVersion: 1,
		ID:            "2026-05-08-prod-eu-1-a3f0b2",
		Date:          "2026-05-08",
		Title:         "OOMKilled on zeebe-broker-2",
		Author:        Author{Name: "T", Email: "t@example.com"},
		Namespace:     "prod-eu-1",
		ContextName:   "prod-eu-1",
		Sources:       Sources{Bundle: "session.triagent.json"},
	}
	if errs := ValidateFrontmatter(good); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}

	// Test that clusters_touched passes validation
	goodWithClusters := Frontmatter{
		SchemaVersion:  1,
		ID:             "2026-05-08-prod-eu-1-a3f0b2",
		Date:           "2026-05-08",
		Title:          "OOMKilled on zeebe-broker-2",
		Author:         Author{Name: "T", Email: "t@example.com"},
		Namespace:      "prod-eu-1",
		ClustersTouched: []string{"prod-eu-1", "prod-us-1"},
		Sources:        Sources{Bundle: "session.triagent.json"},
	}
	if errs := ValidateFrontmatter(goodWithClusters); len(errs) != 0 {
		t.Fatalf("expected no errors with clusters_touched, got %v", errs)
	}

	cases := []struct {
		name   string
		mutate func(*Frontmatter)
		want   string
	}{
		{"missing email", func(fm *Frontmatter) { fm.Author.Email = "" }, "author.email"},
		{"missing name", func(fm *Frontmatter) { fm.Author.Name = "" }, "author.name"},
		{"unsupported version", func(fm *Frontmatter) { fm.SchemaVersion = 2 }, "schema_version"},
		{"bad slug", func(fm *Frontmatter) { fm.ID = "not-a-slug" }, "session slug pattern"},
		{"empty date", func(fm *Frontmatter) { fm.Date = "" }, "date"},
		{"empty title", func(fm *Frontmatter) { fm.Title = "" }, "title"},
		{"empty namespace", func(fm *Frontmatter) { fm.Namespace = "" }, "namespace"},
		{"missing bundle", func(fm *Frontmatter) { fm.Sources.Bundle = "" }, "sources.bundle"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fm := good
			c.mutate(&fm)
			errs := ValidateFrontmatter(fm)
			if len(errs) == 0 {
				t.Fatalf("expected validation error mentioning %q, got none", c.want)
			}
			joined := strings.Join(errs, " | ")
			if !strings.Contains(joined, c.want) {
				t.Fatalf("expected error containing %q, got %q", c.want, joined)
			}
		})
	}
}

func TestValidateBodyHeaders(t *testing.T) {
	good := "## Summary\nx\n## Timeline\nx\n## What was tried\nx\n## Findings\nx\n## Outcome\nx\n"
	if errs := ValidateBodyHeaders(good); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	bad := "## Summary\nx\n## Outcome\nx\n"
	if errs := ValidateBodyHeaders(bad); len(errs) == 0 {
		t.Fatalf("expected errors for missing headers")
	}
}

func TestSplitFrontmatter(t *testing.T) {
	raw := "---\nkey: value\n---\n\nbody text\n"
	fm, body, err := SplitFrontmatter([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(fm) != "key: value" {
		t.Fatalf("got fm %q", string(fm))
	}
	if string(body) != "\nbody text\n" {
		t.Fatalf("got body %q", string(body))
	}

	if _, _, err := SplitFrontmatter([]byte("body without frontmatter\n")); err == nil {
		t.Fatalf("expected error for missing frontmatter")
	}
	if _, _, err := SplitFrontmatter([]byte("---\nno close\n")); err == nil {
		t.Fatalf("expected error for unclosed frontmatter")
	}
}
