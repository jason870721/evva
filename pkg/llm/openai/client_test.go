package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

func TestOpenAIEffort(t *testing.T) {
	tests := []struct {
		level int
		want  string
	}{
		{0, ""},
		{1, "low"},
		{2, "medium"},
		{3, "high"},
		{4, "high"},
		{5, ""}, // out-of-range
	}
	for _, tt := range tests {
		if got := openaiEffort(tt.level); got != tt.want {
			t.Errorf("openaiEffort(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestIsReasoningModel(t *testing.T) {
	// Currently every model in constant.OPENAI.Models is reasoning-class.
	// When a non-reasoning model (gpt-4*, gpt-3.5*) is added later, this
	// test must be updated to reflect the new allowlist.
	if !isReasoningModel("gpt-5.5") {
		t.Error("gpt-5.5 should be recognized as a reasoning model")
	}
	if !isReasoningModel("gpt-5.4-mini") {
		t.Error("gpt-5.4-mini should be recognized as a reasoning model")
	}
}

func TestStripSamplingForReasoning(t *testing.T) {
	tmp := 0.7
	topP := 0.9
	params := llm.LLMParams{
		Temperature: &tmp,
		TopP:        &topP,
	}

	// Reasoning model: params should be stripped.
	stripped := stripSamplingForReasoning(params, "gpt-5.5")
	if stripped.Temperature != nil {
		t.Error("temperature should be nil for reasoning model")
	}
	if stripped.TopP != nil {
		t.Error("top_p should be nil for reasoning model")
	}
}

// TestCompleteRoundTrip verifies the request body shape sent to the OpenAI
// API: Authorization header, JSON body, model field, and that sampling
// params are dropped for reasoning models.
func TestCompleteRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if ct := r.Header.Get("content-type"); ct != "application/json" {
			t.Errorf("content-type: got %q, want application/json", ct)
		}
		if auth := r.Header.Get("authorization"); auth != "Bearer test-key" {
			t.Errorf("authorization: got %q, want Bearer test-key", auth)
		}

		var body apiRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if body.Model != "gpt-5.5" {
			t.Errorf("model: got %q, want gpt-5.5", body.Model)
		}

		// Reasoning models must not carry temperature or top_p.
		if body.Temperature != nil {
			t.Error("temperature should be nil for reasoning model")
		}
		if body.TopP != nil {
			t.Error("top_p should be nil for reasoning model")
		}

		// Verify reasoning_effort is set.
		if body.ReasoningEffort != "high" {
			t.Errorf("reasoning_effort: got %q, want high", body.ReasoningEffort)
		}

		resp := apiResponse{
			Choices: []struct {
				Message      apiMessage `json:"message"`
				FinishReason string     `json:"finish_reason"`
			}{
				{Message: apiMessage{Role: "assistant", Content: "Hello!"}},
			},
		}
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := New(llm.APIConfig{ApiURL: server.URL, ApiSecret: "test-key"}, "gpt-5.5",
		llm.WithEffort(3), // high
		llm.WithTemperature(0.7),
	)

	resp, err := c.Complete(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("Content: got %q, want Hello!", resp.Content)
	}
}

// TestCompleteUsage verifies the OpenAI-specific usage shape: cached tokens
// from prompt_tokens_details and reasoning tokens from
// completion_tokens_details.
func TestCompleteUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{
			"choices": [{"message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5,
				"total_tokens": 15,
				"prompt_tokens_details": {"cached_tokens": 3},
				"completion_tokens_details": {"reasoning_tokens": 2}
			}
		}`
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(resp))
	}))
	defer server.Close()

	c := New(llm.APIConfig{ApiURL: server.URL, ApiSecret: "test-key"}, "gpt-5.5")
	resp, err := c.Complete(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens: got %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens: got %d, want 5", resp.Usage.OutputTokens)
	}
	if resp.Usage.CacheReadTokens != 3 {
		t.Errorf("CacheReadTokens: got %d, want 3", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.ReasoningTokens != 2 {
		t.Errorf("ReasoningTokens: got %d, want 2", resp.Usage.ReasoningTokens)
	}
}

// TestCompleteToolCalls verifies tool call assembly from the API response.
func TestCompleteToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "read", "arguments": "{\"path\":\"/tmp\"}"}
					}]
				},
				"finish_reason": "tool_calls"
			}]
		}`
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(resp))
	}))
	defer server.Close()

	c := New(llm.APIConfig{ApiURL: server.URL, ApiSecret: "test-key"}, "gpt-5.5")
	resp, err := c.Complete(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "read /tmp"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: got %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "read" {
		t.Errorf("tool call: got id=%q name=%q, want id=call_1 name=read", tc.ID, tc.Name)
	}
	if !strings.Contains(string(tc.Input), "/tmp") {
		t.Errorf("tool call input: got %s, want to contain /tmp", string(tc.Input))
	}
}

// TestCompleteAPIError verifies API error responses are surfaced.
func TestCompleteAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"type": "rate_limit", "message": "Rate limit exceeded"}}`))
	}))
	defer server.Close()

	c := New(llm.APIConfig{ApiURL: server.URL, ApiSecret: "test-key"}, "gpt-5.5")
	_, err := c.Complete(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status 429: %v", err)
	}
}

// TestCompleteAPIErrorBody verifies that a 200 OK carrying an error object is
// surfaced (e.g. when OpenAI returns the error in the JSON body rather than as
// an HTTP status).
func TestCompleteAPIErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error": {"type": "invalid_request_error", "message": "Unrecognized model: gpt-fake"}}`))
	}))
	defer server.Close()

	c := New(llm.APIConfig{ApiURL: server.URL, ApiSecret: "test-key"}, "gpt-5.5")
	_, err := c.Complete(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for 200 with error body")
	}
	if !strings.Contains(err.Error(), "invalid_request_error") {
		t.Errorf("error should mention error type: %v", err)
	}
	if !strings.Contains(err.Error(), "Unrecognized model") {
		t.Errorf("error should mention error message: %v", err)
	}
}
