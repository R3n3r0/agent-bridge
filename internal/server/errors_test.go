package server

import (
	"errors"
	"net/http"
	"testing"

	"github.com/r3n3r0/agent-bridge/internal/turn"
)

func TestToAPIError(t *testing.T) {
	cases := []struct {
		kind   turn.Kind
		status int
		code   string
	}{
		{turn.KindInvalid, http.StatusBadRequest, errInvalidRequest},
		{turn.KindSessionBusy, http.StatusConflict, errSessionBusy},
		{turn.KindQueueFull, http.StatusTooManyRequests, errQueueFull},
		{turn.KindTimeout, http.StatusGatewayTimeout, errTimeout},
		{turn.KindUpstream, http.StatusBadGateway, errUpstream},
		{turn.KindCanceled, http.StatusServiceUnavailable, errServerError},
		{turn.KindShutdown, http.StatusServiceUnavailable, errServerError},
		{turn.Kind(99), http.StatusInternalServerError, errServerError},
	}
	for _, c := range cases {
		e := toAPIError(&turn.Error{Kind: c.kind, Message: "m"})
		if e.Status != c.status || e.Code != c.code {
			t.Errorf("kind %d: got %d %s", c.kind, e.Status, e.Code)
		}
	}
	if e := toAPIError(&turn.Error{Kind: turn.KindQueueFull}); e.RetryAfter != retryAfterQueueFull {
		t.Error("missing Retry-After")
	}
	if e := toAPIError(errors.New("x")); e.Status != http.StatusInternalServerError {
		t.Error("plain error")
	}
}
