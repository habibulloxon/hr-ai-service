package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/habibulloxon/hr-ai-service/internal/model"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// AIClient evaluates candidate submissions with a chat-completion model.
// It is an interface so handlers can be tested against a fake implementation.
type AIClient interface {
	EvaluateTechTask(ctx context.Context, response, question string, job model.JobData) (model.AIResponse, error)
	EvaluateInterviewResponse(ctx context.Context, transcript, question string, job model.JobData, base64Frames []string, language string) (model.InterviewResult, error)
	FinalEvaluation(ctx context.Context, allFeedbacks, cvContent string, job model.JobData, language string) (model.AIResponse, error)
}

// OpenAIService is the production AIClient backed by the OpenAI API.
type OpenAIService struct {
	client       openai.Client
	model        openai.ChatModel
	maxTech      int64
	maxInterview int64
	maxFinal     int64
	log          *slog.Logger
}

// NewOpenAIService falls back to a default model when modelName is empty.
func NewOpenAIService(apiKey, modelName string, maxTech, maxInterview, maxFinal int64, log *slog.Logger) *OpenAIService {
	if modelName == "" {
		modelName = string(openai.ChatModelGPT4_1Mini)
	}
	return &OpenAIService{
		client:       openai.NewClient(option.WithAPIKey(apiKey)),
		model:        openai.ChatModel(modelName),
		maxTech:      maxTech,
		maxInterview: maxInterview,
		maxFinal:     maxFinal,
		log:          log,
	}
}

// complete issues a chat completion in JSON-object mode and returns the raw
// message content. JSON mode guarantees the response is parseable JSON, which
// removes the need for brittle text scraping in callers.
func (s *OpenAIService) complete(ctx context.Context, systemMsg string, content []openai.ChatCompletionContentPartUnionParam, maxTokens int64) (string, error) {
	resp, err := s.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemMsg),
			openai.UserMessage(content),
		},
		Model:     s.model,
		MaxTokens: openai.Int(maxTokens),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil {
		return "", fmt.Errorf("openai completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// EvaluateTechTask scores a written technical-task answer.
func (s *OpenAIService) EvaluateTechTask(ctx context.Context, response, question string, job model.JobData) (model.AIResponse, error) {
	const systemMsg = `Act as a professional HR manager analyzing a technical task response.
Evaluate the applicant's written response based on:
1. Technical accuracy and depth of knowledge
2. Problem-solving approach and methodology
3. Code quality, structure, and best practices (if applicable)
4. Clarity of explanation and communication
5. Completeness of the solution
6. Alignment with job requirements and evaluation criteria
Provide constructive feedback focusing on technical competency. Return results as a JSON object with this exact structure:
{
  "score": <integer 0-100>,
  "overall_ai_feedback": "<detailed feedback string>"
}
Keep the feedback short and concise. Do not include any text outside the JSON object.`

	raw, err := s.complete(ctx, systemMsg, buildTechTaskResponseContent(response, question, job), s.maxTech)
	if err != nil {
		return model.AIResponse{}, err
	}

	var out model.AIResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return model.AIResponse{}, fmt.Errorf("decode tech task response: %w", err)
	}
	return out, nil
}

// EvaluateInterviewResponse analyses a single interview answer (transcript plus
// video frames) and suggests a follow-up question.
func (s *OpenAIService) EvaluateInterviewResponse(ctx context.Context, transcript, question string, job model.JobData, base64Frames []string, language string) (model.InterviewResult, error) {
	systemMsg := `Act as a professional HR manager analyzing a single interview response.
Evaluate the applicant's response based on:
1. Content quality and relevance to the question
2. Communication skills and clarity
3. Technical knowledge demonstration
4. Body language and presentation (from video frames)
5. Alignment with job requirements and evaluation criteria

Analyze what might be missing or unclear in the candidate's response and generate a follow-up clarification question.

You HAVE TO follow these rules:
- The feedback must be in ` + language + ` language.
- Return results as a JSON object with this exact structure:
  {
    "ai_response": "<detailed feedback string>",
    "additional_question": "<follow-up clarification question>"
  }
- Do not include any additional text outside the JSON object.
- Ensure the feedback is concise, constructive, and actionable.
- The additional_question should clarify gaps, ask for specifics, or probe deeper into the candidate's knowledge.
- Use the provided job data and video frames to inform your analysis.
- If additional_question is not needed, return an empty string.
- Keep the feedback short and concise. Do not use too many words.`

	raw, err := s.complete(ctx, systemMsg, buildInterviewResponseContent(transcript, question, job, base64Frames), s.maxInterview)
	if err != nil {
		return model.InterviewResult{}, err
	}

	var out model.InterviewResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return model.InterviewResult{}, fmt.Errorf("decode interview response: %w", err)
	}
	return out, nil
}

// FinalEvaluation aggregates per-question feedback and the CV into a final score.
func (s *OpenAIService) FinalEvaluation(ctx context.Context, allFeedbacks, cvContent string, job model.JobData, language string) (model.AIResponse, error) {
	systemMsg := `Act as a professional HR manager conducting final evaluation.
Analyze all collected feedback from individual interview responses and the applicant's CV. Provide a comprehensive assessment based on:
1. Technical skills alignment with job requirements
2. Communication and presentation skills
3. Overall suitability for the position
4. Areas of strength and improvement

You HAVE TO follow these rules:
- The feedback must be in ` + language + ` language.
- Return results as a JSON object with this exact structure:
  {
    "score": <integer 0-100>,
    "overall_ai_feedback": "<detailed feedback string>"
  }
- Do not include any additional text outside the JSON object.
- Ensure the feedback is concise, constructive, and actionable.
- Use the provided CV content and all individual feedbacks to inform your evaluation.
- Keep the feedback short and concise. Do not use too many words.`

	raw, err := s.complete(ctx, systemMsg, buildFinalEvaluationContent(allFeedbacks, cvContent, job), s.maxFinal)
	if err != nil {
		return model.AIResponse{}, err
	}

	var out model.AIResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return model.AIResponse{}, fmt.Errorf("decode final evaluation: %w", err)
	}
	return out, nil
}

func evaluationCriteriaSuffix(job model.JobData) string {
	if job.EvaluationCriteria == "" {
		return ""
	}
	return fmt.Sprintf("\n\nEvaluation Criteria: %s", job.EvaluationCriteria)
}

func buildTechTaskResponseContent(response, question string, job model.JobData) []openai.ChatCompletionContentPartUnionParam {
	text := fmt.Sprintf(
		"Tech Task Question: %s\n\nApplicant's Written Response: %s\n\nJob Position: %s\n\nRequired Skills: %s%s\n\nAnalyze the applicant's written response to this technical question.",
		question, response, job.Title, strings.Join(job.RequiredSkills, ", "), evaluationCriteriaSuffix(job),
	)
	return []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(text)}
}

func buildInterviewResponseContent(transcript, question string, job model.JobData, base64Frames []string) []openai.ChatCompletionContentPartUnionParam {
	text := fmt.Sprintf(
		"Interview Question: %s\n\nApplicant Response Transcript: %s\n\nJob Position: %s\n\nRequired Skills: %s%s\n\nAnalyze the applicant's response and provide feedback based on the video frames and transcript.",
		question, transcript, job.Title, strings.Join(job.RequiredSkills, ", "), evaluationCriteriaSuffix(job),
	)

	content := []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(text)}
	for _, frame := range base64Frames {
		content = append(content, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL: fmt.Sprintf("data:image/jpeg;base64,%s", frame),
		}))
	}
	return content
}

func buildFinalEvaluationContent(allFeedbacks, cvContent string, job model.JobData) []openai.ChatCompletionContentPartUnionParam {
	suffix := ""
	if job.EvaluationCriteria != "" {
		suffix = fmt.Sprintf("\n\nEvaluation Criteria:\n%s", job.EvaluationCriteria)
	}
	text := fmt.Sprintf(
		"Feedbacks from individual interview responses: %s\n\nApplicant CV: %s\n\nJob Position: %s\n\nRequired Skills: %s%s\n\nGenerate a comprehensive final evaluation for this applicant.",
		allFeedbacks, cvContent, job.Title, strings.Join(job.RequiredSkills, ", "), suffix,
	)
	return []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(text)}
}
