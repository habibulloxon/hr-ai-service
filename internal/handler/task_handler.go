package handler

import "net/http"

// Task evaluates a written technical-task answer and returns a score plus
// feedback. Expects POST form values: response, question, jobData.
func (h *Handlers) Task(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := r.FormValue("response")
	question := r.FormValue("question")

	job, err := parseJobData(r.FormValue("jobData"))
	if err != nil {
		http.Error(w, "failed to parse job data: "+err.Error(), http.StatusBadRequest)
		return
	}

	result, err := h.ai.EvaluateTechTask(r.Context(), response, question, job)
	if err != nil {
		h.log.Error("evaluate tech task", "err", err)
		http.Error(w, "failed to process tech task: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ai_response": result.OverallAIFeedback,
		"ai_score":    result.Score,
	})
}
