package server

import (
	"context"
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
