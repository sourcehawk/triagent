package profile

import (
	"strings"
)

// RenderNamespaceHints applies the profile's NamespaceDerivation against a
// flat string-map of alert-payload fields. Returns zero or more hint
// strings. Behaviour:
//   - Empty config → nil.
//   - Rules takes precedence over Template when both are set.
//   - Rules: top-to-bottom; first matching `when` wins. Unsupported predicate
//     shapes silently skip (don't match). When a matching rule's template
//     fails to render (e.g. a referenced field is missing), evaluation stops
//     and the function returns nil — later rules are NOT consulted. This is
//     intentionally safe-fail: a half-resolved hint is worse than no hint.
//   - Template alone: rendered once.
func RenderNamespaceHints(cfg NamespaceDerivation, fields map[string]string) []string {
	if len(cfg.Rules) > 0 {
		for _, r := range cfg.Rules {
			if !matchesWhen(r.When, fields) {
				continue
			}
			if out, ok := renderTemplate(r.Template, fields); ok {
				return []string{out}
			}
			return nil
		}
		return nil
	}
	if cfg.Template == "" {
		return nil
	}
	out, ok := renderTemplate(cfg.Template, fields)
	if !ok {
		return nil
	}
	return []string{out}
}

// renderTemplate substitutes ${field-name} placeholders. Returns (rendered,
// true) when every placeholder resolved to a non-empty value; otherwise
// (zero-value, false). Leaving placeholders intact would be worse than
// emitting no hint.
func renderTemplate(tmpl string, fields map[string]string) (string, bool) {
	out := tmpl
	for k, v := range fields {
		out = strings.ReplaceAll(out, "${"+k+"}", v)
	}
	if strings.Contains(out, "${") {
		return "", false
	}
	return out, true
}

// matchesWhen is a minimal predicate evaluator. Supports two shapes:
//   - "${field} == 'literal'"
//   - "${field} != ''"
// Anything else is treated as non-matching. Kept small on purpose;
// profiles needing richer logic should compose multiple rules.
func matchesWhen(when string, fields map[string]string) bool {
	when = strings.TrimSpace(when)
	// `${field} == 'literal'`
	if i := strings.Index(when, " == "); i > 0 {
		left := strings.TrimSpace(when[:i])
		right := strings.TrimSpace(when[i+len(" == "):])
		if !strings.HasPrefix(left, "${") || !strings.HasSuffix(left, "}") {
			return false
		}
		if len(right) < 2 || !strings.HasPrefix(right, "'") || !strings.HasSuffix(right, "'") {
			return false
		}
		field := left[2 : len(left)-1]
		lit := right[1 : len(right)-1]
		return fields[field] == lit
	}
	// `${field} != ''`
	if i := strings.Index(when, " != "); i > 0 {
		left := strings.TrimSpace(when[:i])
		right := strings.TrimSpace(when[i+len(" != "):])
		if !strings.HasPrefix(left, "${") || !strings.HasSuffix(left, "}") {
			return false
		}
		if right != "''" {
			return false
		}
		field := left[2 : len(left)-1]
		return fields[field] != ""
	}
	return false
}
