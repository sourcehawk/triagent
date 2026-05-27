package server

import (
	"encoding/json"
	"net/http"
)

// handleProfileInputs returns the loaded profile's investigation_inputs schema
// as a JSON array. Only the frontend-relevant subset is exposed: id, label,
// type, optional, placeholder, hint. PromptKeys are intentionally omitted —
// they are a backend rendering concern.
//
// GET /api/profile/inputs
func (a *apiHandlers) handleProfileInputs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	type inputOut struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Type        string `json:"type"`
		Optional    bool   `json:"optional"`
		Placeholder string `json:"placeholder,omitempty"`
		Hint        string `json:"hint,omitempty"`
	}
	inputs := make([]inputOut, 0)
	if a.prof != nil {
		inputs = make([]inputOut, 0, len(a.prof.InvestigationInputs))
		for _, in := range a.prof.InvestigationInputs {
			inputs = append(inputs, inputOut{
				ID:          in.ID,
				Label:       in.Label,
				Type:        in.Type,
				Optional:    in.Optional,
				Placeholder: in.Placeholder,
				Hint:        in.Hint,
			})
		}
	}
	resp := struct {
		Inputs []inputOut `json:"inputs"`
	}{Inputs: inputs}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
