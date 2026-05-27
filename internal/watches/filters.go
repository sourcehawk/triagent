package watches

import (
	"fmt"
	"regexp"
	"strings"
)

// ApplyFilters returns a non-nil FilteredAnnotation describing the first
// rule the item failed. nil = item passes all filters.
func ApplyFilters(it Item, fs []Filter) *FilteredAnnotation {
	for i, f := range fs {
		if !filterPasses(it, f) {
			return &FilteredAnnotation{RuleIndex: i, Summary: summarize(f)}
		}
	}
	return nil
}

func filterPasses(it Item, f Filter) bool {
	values := fieldValues(it, f.Field)
	switch f.Op {
	case "contains":
		return anyContains(values, f.Value)
	case "does_not_contain":
		return !anyContains(values, f.Value)
	case "regex_matches":
		re, err := regexp.Compile(f.Value)
		if err != nil {
			return true
		}
		return anyRegex(values, re)
	case "not_regex_matches":
		re, err := regexp.Compile(f.Value)
		if err != nil {
			return true
		}
		return !anyRegex(values, re)
	}
	return true
}

func fieldValues(it Item, field string) []string {
	switch field {
	case "title":
		return []string{it.Snapshot.Title}
	case "body":
		return []string{it.Snapshot.Body}
	case "text":
		return []string{it.Snapshot.Text}
	case "author":
		return []string{it.Snapshot.Author}
	case "label":
		return it.Snapshot.Labels
	}
	return nil
}

func anyContains(xs []string, sub string) bool {
	subL := strings.ToLower(sub)
	for _, x := range xs {
		if strings.Contains(strings.ToLower(x), subL) {
			return true
		}
	}
	return false
}

func anyRegex(xs []string, re *regexp.Regexp) bool {
	for _, x := range xs {
		if re.MatchString(x) {
			return true
		}
	}
	return false
}

func summarize(f Filter) string {
	return fmt.Sprintf("%s %s %q failed", f.Field, f.Op, f.Value)
}
