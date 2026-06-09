// Package model holds the shared data types exchanged between the HTTP handlers
// and the service layer.
package model

// JobData describes the position a candidate is being evaluated against.
type JobData struct {
	Title              string   `json:"title"`
	RequiredSkills     []string `json:"required_skills"`
	EvaluationCriteria string   `json:"evaluation_criteria"`
	Description        string   `json:"description"`
}

// AIResponse is the scored feedback returned for tech tasks and final
// evaluations.
type AIResponse struct {
	Score             int    `json:"score"`
	OverallAIFeedback string `json:"overall_ai_feedback"`
}

// InterviewResult is the feedback returned for a single interview response,
// including a suggested follow-up question.
type InterviewResult struct {
	AIResponse         string `json:"ai_response"`
	AdditionalQuestion string `json:"additional_question"`
}
