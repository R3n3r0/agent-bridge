package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/media"
	"github.com/r3n3r0/agent-bridge/internal/prompt"
	"github.com/r3n3r0/agent-bridge/internal/runner"
)

// fakeResolver implements media.Resolver from a fixed table.
type fakeResolver map[string]media.Image

func (f fakeResolver) Resolve(ctx context.Context, url string) (media.Image, error) {
	if img, ok := f[url]; ok {
		return img, nil
	}
	return media.Image{}, errors.New("not found")
}

func imageMsg(text string, urls ...string) prompt.Message {
	m := prompt.Message{Role: prompt.RoleUser, Text: text, Parts: []prompt.Part{{Text: text}}}
	for _, u := range urls {
		m.Parts = append(m.Parts, prompt.Part{ImageURL: u})
	}
	return m
}

func imageService(t *testing.T, resolver media.Resolver, maxImages int) *fixture {
	t.Helper()
	f := newFixture(t, config.OnBusyWait, func(int, runner.Request) (*runner.Outcome, error) { return success("s", "ok"), nil })
	f.svc.(*service).Images = resolver
	f.svc.(*service).MaxImages = maxImages
	return f
}

type stdinMessage struct {
	Message struct {
		Content []struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Source struct {
				MediaType string `json:"media_type"`
			} `json:"source"`
		} `json:"content"`
	} `json:"message"`
}

func TestImagesSentAsStructuredInput(t *testing.T) {
	images := fakeResolver{
		"u1": {MediaType: "image/png", Data: []byte{1}},
		"u2": {MediaType: "image/jpeg", Data: []byte{2}},
		"u3": {MediaType: "image/gif", Data: []byte{3}},
	}
	f := imageService(t, images, 0)
	req := request(imageMsg("compare", "u1", "u2"))
	req.SessionKey = "img"
	if _, err := f.svc.Execute(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	call := f.exec.calls[0]
	if argAfter(call.Args, "--input-format") != "stream-json" {
		t.Fatalf("args %q", call.Args)
	}
	var msg stdinMessage
	if err := json.Unmarshal([]byte(call.Stdin), &msg); err != nil {
		t.Fatalf("stdin %q: %v", call.Stdin, err)
	}
	c := msg.Message.Content
	if len(c) != 5 || c[0].Text != "compare\n[image 1]\n[image 2]" || c[2].Source.MediaType != "image/png" || c[4].Source.MediaType != "image/jpeg" {
		t.Fatalf("content %+v", c)
	}

	// Resumed turn: only the new image is sent.
	next := request(imageMsg("compare", "u1", "u2"), assistant("same"), imageMsg("and now?", "u3"))
	next.SessionKey = "img"
	if _, err := f.svc.Execute(context.Background(), next, nil); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(f.exec.calls[1].Stdin), &msg)
	c = msg.Message.Content
	if len(c) != 3 || c[0].Text != "and now?\n[image 1]" || c[2].Source.MediaType != "image/gif" || argAfter(f.exec.calls[1].Args, "--resume") == "" {
		t.Fatalf("resumed content %+v", c)
	}

	// Text-only turns keep plain stdin.
	plain := request(user("hi"))
	plain.SessionKey = "plain"
	_, _ = f.svc.Execute(context.Background(), plain, nil)
	if f.exec.calls[2].Stdin != "hi" || argAfter(f.exec.calls[2].Args, "--input-format") != "" {
		t.Fatalf("plain turn %+v", f.exec.calls[2])
	}
}

func TestImageErrors(t *testing.T) {
	cases := []struct {
		name     string
		resolver media.Resolver
		max      int
		urls     []string
		want     string
	}{
		{"disabled", nil, 0, []string{"u1"}, "not enabled"},
		{"too many", fakeResolver{"u1": {MediaType: "image/png"}}, 1, []string{"u1", "u1"}, "too many images"},
		{"unresolvable", fakeResolver{}, 0, []string{"missing"}, "image 1"},
		{"too large", fakeResolver{"big": {MediaType: "image/png", Data: make([]byte, prompt.MaxBytes)}}, 0, []string{"big"}, "too large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := imageService(t, c.resolver, c.max)
			_, err := f.svc.Execute(context.Background(), request(imageMsg("x", c.urls...)), nil)
			if kindOf(t, err) != KindInvalid || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v", err)
			}
			if len(f.exec.calls) != 0 {
				t.Fatal("process started")
			}
		})
	}
}
