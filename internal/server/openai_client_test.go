package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// TestOfficialOpenAIClient checks that the official Go SDK works by changing
// only base URL, API key and model.
func TestOfficialOpenAIClient(t *testing.T) {
	h := newHarness(t, harnessOpts{apiKey: "bridge-key", keepalive: 20 * time.Millisecond,
		env: []string{"FAKE_SCENARIO=slow", "FAKE_SLEEP_MS=100"}})
	client := openai.NewClient(
		option.WithBaseURL(h.ts.URL+"/v1/"),
		option.WithAPIKey("bridge-key"),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	params := openai.ChatCompletionNewParams{
		Model: "coder",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are terse."),
			openai.UserMessage("Say hello"),
		},
		Temperature: openai.Float(0.1),
	}
	c, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if c.Choices[0].Message.Content != "Fake answer." || c.Usage.TotalTokens != 20 || c.Usage.PromptTokens != 15 ||
		c.Choices[0].FinishReason != "stop" || c.Model != "coder" {
		t.Fatalf("completion %+v", c)
	}

	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	acc := openai.ChatCompletionAccumulator{}
	chunks := 0
	for stream.Next() {
		acc.AddChunk(stream.Current())
		chunks++
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if acc.Choices[0].Message.Content != "Fake answer." || acc.Usage.TotalTokens != 20 || chunks != 4 {
		t.Fatalf("stream content %q usage %+v chunks %d", acc.Choices[0].Message.Content, acc.Usage, chunks)
	}

	models, err := client.Models.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models.Data) != 5 {
		t.Fatalf("models %d", len(models.Data))
	}
	m, err := client.Models.Get(ctx, "claude-code/haiku")
	if err != nil || m.ID != "claude-code/haiku" {
		t.Fatalf("model %+v %v", m, err)
	}

	bad := openai.NewClient(option.WithBaseURL(h.ts.URL+"/v1/"), option.WithAPIKey("wrong"), option.WithMaxRetries(0))
	if _, err := bad.Chat.Completions.New(ctx, params); err == nil {
		t.Fatal("wrong key accepted")
	}
}

// TestOfficialOpenAIClientToolCalls runs the standard function-calling loop
// of the official SDK, unchanged, against the bridge.
func TestOfficialOpenAIClientToolCalls(t *testing.T) {
	h := newHarness(t, harnessOpts{env: []string{"FAKE_SCENARIO=tools"}})
	client := openai.NewClient(option.WithBaseURL(h.ts.URL+"/v1/"), option.WithAPIKey("unused"), option.WithMaxRetries(0))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	params := openai.ChatCompletionNewParams{
		Model:    "claude-code/sonnet",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("What's the weather in Rome?")},
		Tools: []openai.ChatCompletionToolParam{{
			Function: openai.FunctionDefinitionParam{
				Name:        "get_weather",
				Description: openai.String("Current weather for a city"),
				Parameters: openai.FunctionParameters{
					"type":       "object",
					"properties": map[string]any{"city": map[string]string{"type": "string"}},
					"required":   []string{"city"},
				},
			},
		}},
	}
	completion, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	toolCalls := completion.Choices[0].Message.ToolCalls
	if completion.Choices[0].FinishReason != "tool_calls" || len(toolCalls) != 1 || toolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("completion %+v", completion.Choices[0])
	}
	var args struct{ City string }
	if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args); err != nil || args.City != "Rome" {
		t.Fatalf("arguments %q", toolCalls[0].Function.Arguments)
	}
	params.Messages = append(params.Messages, completion.Choices[0].Message.ToParam())
	for _, tc := range toolCalls {
		params.Messages = append(params.Messages, openai.ToolMessage("sunny, 24C", tc.ID))
	}
	completion, err = client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Choices[0].Message.Content != "results: sunny, 24C" || completion.Choices[0].FinishReason != "stop" {
		t.Fatalf("final %+v", completion.Choices[0])
	}

	// Same loop, streamed, with the SDK accumulator.
	params.Messages = params.Messages[:1]
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	acc := openai.ChatCompletionAccumulator{}
	var finished []openai.FinishedChatCompletionToolCall
	for stream.Next() {
		acc.AddChunk(stream.Current())
		if tc, ok := acc.JustFinishedToolCall(); ok {
			finished = append(finished, tc)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if len(acc.Choices[0].Message.ToolCalls) != 1 || acc.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("streamed %+v", acc.Choices[0])
	}
	params.Messages = append(params.Messages, acc.Choices[0].Message.ToParam(),
		openai.ToolMessage("rain", acc.Choices[0].Message.ToolCalls[0].ID))
	completion, err = client.Chat.Completions.New(ctx, params)
	if err != nil || completion.Choices[0].Message.Content != "results: rain" {
		t.Fatalf("final after stream %+v %v (finished %d)", completion, err, len(finished))
	}
	if len(h.starts()) != 2 {
		t.Fatalf("each conversation must use one process, got %d", len(h.starts()))
	}
}
