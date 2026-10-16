package runner

import (
	"fmt"
	"strings"
)

// acpConfigOption is a session configuration option (model, thinking,
// mode) returned by session/new and session/resume.
type acpConfigOption struct {
	ID           string `json:"id"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

func findOption(options []acpConfigOption, id string) *acpConfigOption {
	for i := range options {
		if options[i].ID == id {
			return &options[i]
		}
	}
	return nil
}

func currentValue(options []acpConfigOption, id string) string {
	if o := findOption(options, id); o != nil {
		return o.CurrentValue
	}
	return ""
}

// modelValue finds the model option matching a requested model, by value
// or by display name.
func modelValue(options []acpConfigOption, model string) (string, error) {
	o := findOption(options, "model")
	if o == nil {
		return model, nil
	}
	var names []string
	for _, v := range o.Options {
		if strings.EqualFold(v.Value, model) || strings.EqualFold(v.Name, model) {
			return v.Value, nil
		}
		names = append(names, v.Name)
	}
	return "", fmt.Errorf("model %q is not configured in the CLI (available: %s)", model, strings.Join(names, ", "))
}

// currentModelName is the display name of the session's model.
func currentModelName(options []acpConfigOption, requested string) string {
	o := findOption(options, "model")
	if o == nil {
		return requested
	}
	want := o.CurrentValue
	if requested != "" {
		if v, err := modelValue(options, requested); err == nil {
			want = v
		}
	}
	for _, v := range o.Options {
		if v.Value == want {
			return v.Name
		}
	}
	return want
}

// thinkingValue maps a reasoning effort to the thinking option: "off" for
// none and minimal, the same level when the model offers it, the highest
// level for xhigh and max, otherwise "on". Empty when nothing applies.
func thinkingValue(options []acpConfigOption, effort string) string {
	o := findOption(options, "thinking")
	if o == nil || effort == "" {
		return ""
	}
	has := func(v string) bool {
		for _, x := range o.Options {
			if x.Value == v {
				return true
			}
		}
		return false
	}
	switch {
	case effort == "none" || effort == "minimal":
		if has("off") {
			return "off"
		}
		return ""
	case has(effort):
		return effort
	case effort == "xhigh" || effort == "max":
		if last := o.Options[len(o.Options)-1].Value; last != "off" {
			return last
		}
	}
	if has("on") {
		return "on"
	}
	return ""
}
