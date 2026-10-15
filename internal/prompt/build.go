package prompt

import (
	"fmt"
	"strings"
)

// Prompt is the text sent to the CLI and the images it references, in the
// order of their "[image N]" placeholders.
type Prompt struct {
	Text   string
	Images []string
}

// renderer renders message contents, replacing images with numbered
// placeholders and collecting their URLs.
type renderer struct {
	images []string
}

func (r *renderer) content(m Message) string {
	if len(m.Parts) == 0 {
		return m.Text
	}
	out := make([]string, 0, len(m.Parts))
	for _, p := range m.Parts {
		if p.ImageURL == "" {
			out = append(out, p.Text)
			continue
		}
		r.images = append(r.images, p.ImageURL)
		out = append(out, fmt.Sprintf("[image %d]", len(r.images)))
	}
	return strings.Join(out, "\n")
}

// lastAssistant returns the index of the last assistant message, or -1.
func lastAssistant(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant {
			return i
		}
	}
	return -1
}

// Current returns the user and tool messages after the last assistant
// message: what the model has not seen yet.
func Current(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs[lastAssistant(msgs)+1:] {
		if m.Role == RoleUser || m.Role == RoleTool {
			out = append(out, m)
		}
	}
	return out
}

// CurrentRequest returns the texts of the user messages after the last
// assistant message.
func CurrentRequest(msgs []Message) []string {
	var out []string
	for _, m := range Current(msgs) {
		if m.Role == RoleUser {
			out = append(out, m.Text)
		}
	}
	return out
}

// ToolResults returns the tool messages after the last assistant message,
// keyed by tool call id. Messages without id are skipped.
func ToolResults(msgs []Message) map[string]string {
	out := map[string]string{}
	for _, m := range Current(msgs) {
		if m.Role == RoleTool && m.ToolCallID != "" {
			out[m.ToolCallID] = m.Text
		}
	}
	return out
}

// Instructions returns the system and developer texts joined by a blank line.
func Instructions(msgs []Message) string {
	var out []string
	for _, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper {
			out = append(out, m.Text)
		}
	}
	return strings.Join(out, "\n\n")
}

// Options control Build.
type Options struct {
	// Resumed sends only the new user and tool messages, since the previous
	// context is already in the session.
	Resumed bool
	// OmitInstructions leaves system and developer messages out of the
	// prompt, for when they are passed to the CLI as its system prompt.
	OmitInstructions bool
}

// Build returns the prompt for a turn; see Options.Resumed.
func Build(msgs []Message, resumed bool) (string, error) {
	return BuildWith(msgs, Options{Resumed: resumed})
}

// BuildWith returns the prompt text for a turn; images are rendered as
// placeholders only. Use BuildPrompt to also get the images.
func BuildWith(msgs []Message, o Options) (string, error) {
	p, err := BuildPrompt(msgs, o)
	return p.Text, err
}

// BuildPrompt returns the prompt for a turn and the images it references.
// With Resumed, only the images of the new messages are included.
func BuildPrompt(msgs []Message, o Options) (Prompt, error) {
	if o.OmitInstructions {
		kept := make([]Message, 0, len(msgs))
		for _, m := range msgs {
			if m.Role != RoleSystem && m.Role != RoleDeveloper {
				kept = append(kept, m)
			}
		}
		msgs = kept
	}
	current := Current(msgs)
	if len(current) == 0 {
		return Prompt{}, ErrNoCurrentRequest
	}
	r := &renderer{}
	var text string
	if o.Resumed {
		text = r.renderCurrent(current)
	} else {
		text = r.buildNew(msgs, current)
	}
	if len(text) > MaxBytes {
		return Prompt{}, ErrTooLarge
	}
	return Prompt{Text: text, Images: r.images}, nil
}

func (r *renderer) renderCurrent(current []Message) string {
	parts := make([]string, len(current))
	for i, m := range current {
		if m.Role == RoleTool {
			parts[i] = r.renderTool(m)
		} else {
			parts[i] = r.content(m)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (r *renderer) renderTool(m Message) string {
	text := r.content(m)
	if m.ToolCallID == "" && m.Name == "" {
		return "<tool>" + text + "</tool>"
	}
	return fmt.Sprintf("<tool tool_call_id=%q name=%q>%s</tool>", m.ToolCallID, m.Name, text)
}

func renderAssistant(m Message) string {
	var b strings.Builder
	b.WriteString("<assistant>")
	b.WriteString(m.Text)
	for _, tc := range m.ToolCalls {
		if b.Len() > len("<assistant>") {
			b.WriteString("\n")
		}
		if tc.ID == "" {
			fmt.Fprintf(&b, "<tool_call name=%q>%s</tool_call>", tc.Name, tc.Arguments)
			continue
		}
		fmt.Fprintf(&b, "<tool_call id=%q name=%q>%s</tool_call>", tc.ID, tc.Name, tc.Arguments)
	}
	b.WriteString("</assistant>")
	return b.String()
}

func (r *renderer) buildNew(msgs []Message, current []Message) string {
	instructions := Instructions(msgs)
	if len(msgs) == 1 && msgs[0].Role == RoleUser {
		return r.content(msgs[0])
	}

	last := lastAssistant(msgs)
	var history strings.Builder
	for i, m := range msgs {
		if m.Role == RoleSystem || m.Role == RoleDeveloper || i > last {
			continue
		}
		switch m.Role {
		case RoleAssistant:
			history.WriteString(renderAssistant(m))
		case RoleTool:
			history.WriteString(r.renderTool(m))
		default:
			fmt.Fprintf(&history, "<%s>%s</%s>", m.Role, r.content(m), m.Role)
		}
		history.WriteString("\n")
	}

	var sections []string
	if instructions != "" {
		sections = append(sections, "<instructions>\n"+instructions+"\n</instructions>")
	}
	if history.Len() > 0 {
		sections = append(sections, "<conversation_history>\n"+history.String()+"</conversation_history>")
	}
	sections = append(sections, "<current_request>\n"+r.renderCurrent(current)+"\n</current_request>")
	return strings.Join(sections, "\n\n")
}
