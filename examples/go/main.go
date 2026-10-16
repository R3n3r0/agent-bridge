// Example client using the official OpenAI Go SDK against agent-bridge.
//
//	AGENT_BRIDGE_URL=http://127.0.0.1:8787/v1/ go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	baseURL := os.Getenv("AGENT_BRIDGE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787/v1/"
	}
	apiKey := os.Getenv("AGENT_BRIDGE_API_KEY")
	if apiKey == "" {
		apiKey = "unused"
	}
	client := openai.NewClient(option.WithBaseURL(baseURL), option.WithAPIKey(apiKey))
	ctx := context.Background()

	params := openai.ChatCompletionNewParams{
		Model:    "bridge/haiku",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("Explain goroutines in two sentences.")},
	}

	// Non-streaming.
	res, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Choices[0].Message.Content)
	fmt.Printf("tokens: %d\n\n", res.Usage.TotalTokens)

	// Streaming, continuing the same conversation with X-Session-Id.
	params.Messages = append(params.Messages,
		openai.AssistantMessage(res.Choices[0].Message.Content),
		openai.UserMessage("Now give a one-line example."))
	stream := client.Chat.Completions.NewStreaming(ctx, params, option.WithHeader("X-Session-Id", "example-go"))
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			fmt.Print(chunk.Choices[0].Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}
