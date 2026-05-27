package profile

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// ValidateInputValues checks that the provided values match the profile's
// InvestigationInputs schema: every key in values is a declared input ID,
// and every non-optional input has a present, non-empty value.
func ValidateInputValues(inputs []InvestigationInput, values map[string]map[string]any) error {
	declared := map[string]InvestigationInput{}
	for _, in := range inputs {
		declared[in.ID] = in
	}
	for id := range values {
		if _, ok := declared[id]; !ok {
			return fmt.Errorf("unknown investigation input %q", id)
		}
	}
	for _, in := range inputs {
		if in.Optional {
			continue
		}
		ctx := values[in.ID]
		if ctx == nil {
			return fmt.Errorf("required input %q is missing", in.ID)
		}
		switch in.Type {
		case "text", "url", "textarea", "cluster_id":
			if s, _ := ctx["value"].(string); s == "" {
				return fmt.Errorf("required input %q has empty value", in.ID)
			}
		case "slack_channel":
			id, _ := ctx["id"].(string)
			url, _ := ctx["url"].(string)
			if id == "" && url == "" {
				return fmt.Errorf("required input %q needs id or url", in.ID)
			}
		}
	}
	return nil
}

// RenderedKey is one (key, value) pair to emit into the prompt's
// parameter block.
type RenderedKey struct {
	Key   string
	Value string
}

// RenderPromptKeys evaluates an input's prompt_keys against the supplied
// template context (the input's value or a struct keyed by template field
// names for slack_channel). For each PromptKey, the `if` template is
// evaluated first; the line is emitted only when the rendered `if` is
// non-empty after trimming whitespace. An empty If string means no guard —
// the value template is applied unconditionally.
func RenderPromptKeys(in InvestigationInput, ctx map[string]any) ([]RenderedKey, error) {
	out := make([]RenderedKey, 0, len(in.PromptKeys))
	for _, pk := range in.PromptKeys {
		if pk.If != "" {
			ok, err := evalIf(pk.If, ctx)
			if err != nil {
				return nil, fmt.Errorf("input %q if-template: %w", in.ID, err)
			}
			if !ok {
				continue
			}
		}
		v, err := evalString(pk.Value, ctx)
		if err != nil {
			return nil, fmt.Errorf("input %q value-template: %w", in.ID, err)
		}
		out = append(out, RenderedKey{Key: pk.Key, Value: v})
	}
	return out, nil
}

func evalString(tpl string, ctx map[string]any) (string, error) {
	t, err := template.New("v").Parse(tpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, ctx); err != nil {
		return "", err
	}
	return b.String(), nil
}

func evalIf(tpl string, ctx map[string]any) (bool, error) {
	s, err := evalString(tpl, ctx)
	if err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(s)
	// Go text/template renders boolean expressions as "true" or "false".
	// Treat the empty string and the literal "false" as falsy.
	return trimmed != "" && trimmed != "false", nil
}
