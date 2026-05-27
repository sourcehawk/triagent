package meta

import "testing"

func TestToolSpecs(t *testing.T) {
	specs := ToolSpecs()
	if len(specs) != 1 {
		t.Fatalf("want 1 spec, got %d", len(specs))
	}
	s := specs[0]
	if s.Server != "triagent-meta" {
		t.Fatalf("server %q", s.Server)
	}
	if s.Name != "set_session_label" {
		t.Fatalf("name %q", s.Name)
	}
	if s.Description == "" {
		t.Fatalf("description is empty")
	}
	if len(s.Inputs) != 1 || s.Inputs[0].Name != "label" {
		t.Fatalf("inputs: %+v", s.Inputs)
	}
	if !s.Inputs[0].Required {
		t.Fatalf("expected label to be required")
	}
}
