package strategies

import (
	"fmt"
	"strings"
)

// renderArgs substitutes ${cluster_id} / ${namespace} placeholders in a
// SuggestedCall's args using the session's context. String values are
// templated; other types pass through unchanged.
func renderArgs(call SuggestedCall, sess *Session) map[string]any {
	if len(call.Args) == 0 {
		return nil
	}
	out := make(map[string]any, len(call.Args))
	for k, v := range call.Args {
		if s, ok := v.(string); ok {
			s = strings.ReplaceAll(s, "${cluster_id}", sess.ClusterID)
			s = strings.ReplaceAll(s, "${namespace}", sess.Namespace)
			out[k] = s
		} else {
			out[k] = v
		}
	}
	return out
}

// nextOptions builds the agent-facing description of where the walker can go
// from the current node. Each option includes the branch's condition (so the
// agent can pick the matching one) and the target node ID.
type NextOption struct {
	Condition string `json:"condition"`
	Goto      string `json:"goto"`
}

// stepView is what we return to the agent when it asks about a node — either
// after walk_playbook or get_state.
type stepView struct {
	NodeID           string         `json:"node_id"`
	Description      string         `json:"description"`
	SuggestedCalls   []renderedCall `json:"suggested_calls,omitempty"`
	ExpectedFindings []string       `json:"expected_findings,omitempty"`
	NextOptions      []NextOption   `json:"next_options,omitempty"`
	TerminalAdvice   string         `json:"terminal_advice,omitempty"`
	// Handoff is the structured cross-playbook pointer for terminal
	// nodes — the agent should walk_playbook against one of these
	// when the conclusion is "switch to another playbook". The prose in
	// terminal_advice is the why; this is the what.
	Handoff []string `json:"handoff,omitempty"`
}

// renderedCall is a SuggestedCall with templated args resolved.
type renderedCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

func renderStep(node Node, sess *Session) stepView {
	calls := make([]renderedCall, 0, len(node.SuggestedCalls))
	for _, c := range node.SuggestedCalls {
		calls = append(calls, renderedCall{Tool: c.Tool, Args: renderArgs(c, sess)})
	}
	opts := make([]NextOption, 0, len(node.Next))
	for _, b := range node.Next {
		opts = append(opts, NextOption(b))
	}
	return stepView{
		NodeID:           node.ID,
		Description:      node.Description,
		SuggestedCalls:   calls,
		ExpectedFindings: node.ExpectedFindings,
		NextOptions:      opts,
		TerminalAdvice:   node.TerminalAdvice,
		Handoff:          node.Handoff,
	}
}

// findNode returns the node from the playbook or an error if missing.
func findNode(pb *Playbook, nodeID string) (Node, error) {
	if n, ok := pb.Nodes[nodeID]; ok {
		return n, nil
	}
	return Node{}, fmt.Errorf("node %q not found in playbook %q", nodeID, pb.ID)
}

// isPureTransition reports whether a node contributes no agent-facing work:
// no expected findings, no suggested calls, exactly one next branch, no
// delegate_to / handoff / terminal_advice. The walker can transparently
// advance past such nodes — the agent's only legal action on reaching one
// was step_complete{goto: <single_next>}, so skipping the round-trip is
// correctness-preserving.
func isPureTransition(n Node) bool {
	return len(n.ExpectedFindings) == 0 &&
		len(n.SuggestedCalls) == 0 &&
		len(n.Next) == 1 &&
		n.DelegateTo == "" &&
		len(n.Handoff) == 0 &&
		n.TerminalAdvice == ""
}
