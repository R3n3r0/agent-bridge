package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var testPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func pngDataURL() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG)
}

func imageBody(model string, urls ...string) map[string]any {
	parts := []map[string]any{{"type": "text", "text": "what is in these images?"}}
	for _, u := range urls {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u, "detail": "auto"}})
	}
	return map[string]any{"model": model, "messages": []map[string]any{{"role": "user", "content": parts}}}
}

func TestImagesEndToEnd(t *testing.T) {
	jpeg := append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 64)...)
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(jpeg) }))
	defer img.Close()

	h := newHarness(t, harnessOpts{})
	c := decode[chatCompletion](t, h.post(imageBody("bridge/sonnet", pngDataURL(), img.URL+"/photo.jpg"), nil))
	want := `Fake answer. [text="what is in these images?\n[image 1]\n[image 2]" images=image/png,image/jpeg]`
	if c.Choices[0].Message.text() != want {
		t.Fatalf("answer %q", c.Choices[0].Message.text())
	}
	r := h.starts()[0]
	if v, _ := argValue(r.Args, "--input-format"); v != "stream-json" {
		t.Fatalf("args %q", r.Args)
	}
	if strings.Contains(h.logs.String(), base64.StdEncoding.EncodeToString(testPNG)[:40]) {
		t.Fatal("image data in logs")
	}

	// Errors are reported before any process starts.
	for _, body := range []map[string]any{
		imageBody("bridge/sonnet", "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("not an image"))),
		imageBody("bridge/sonnet", "ftp://example.com/a.png"),
		imageBody("bridge/sonnet", img.URL+"/missing", "x"),
	} {
		expectError(t, h.post(body, nil), 400, errInvalidRequest)
	}
	if len(h.starts()) != 1 {
		t.Fatalf("processes %d", len(h.starts()))
	}
}

func TestImagesStreamAndLimits(t *testing.T) {
	h := newHarness(t, harnessOpts{configJSON: `"images": {"max_count": 1}`})
	body := imageBody("bridge/haiku", pngDataURL())
	body["stream"] = true
	resp := h.post(body, nil)
	evs := sseEvents(t, resp.Body)
	resp.Body.Close()
	var text strings.Builder
	for _, e := range evs {
		if strings.HasPrefix(e, "data: {") {
			text.WriteString(chunkOf(t, e).Choices[0].Delta.Content)
		}
	}
	if !strings.Contains(text.String(), "images=image/png") {
		t.Fatalf("stream %q", evs)
	}
	expectError(t, h.post(imageBody("bridge/haiku", pngDataURL(), pngDataURL()), nil), 400, errInvalidRequest)

	off := newHarness(t, harnessOpts{configJSON: `"images": {"enabled": false}`})
	expectError(t, off.post(imageBody("bridge/haiku", pngDataURL()), nil), 400, errUnsupported)
}

// TestOfficialOpenAIClientImages sends an image with the official SDK.
func TestOfficialOpenAIClientImages(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	client := openai.NewClient(option.WithBaseURL(h.ts.URL+"/v1/"), option.WithAPIKey("unused"), option.WithMaxRetries(0))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "bridge/sonnet",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
				openai.TextContentPart("Describe the picture."),
				openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: pngDataURL()}),
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.Choices[0].Message.Content, "images=image/png") {
		t.Fatalf("answer %q", completion.Choices[0].Message.Content)
	}
}
