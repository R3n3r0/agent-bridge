package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Forward sends a call to the broker described by spec and waits for the
// result. It is used by the MCP subprocess.
func Forward(ctx context.Context, client *http.Client, spec Spec, name string, arguments json.RawMessage) (Result, error) {
	body, err := json.Marshal(callRequest{Name: name, Arguments: arguments})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.Endpoint+"/call", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+spec.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Result{}, errors.New(strings.TrimSpace(string(msg)))
	}
	var res Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return Result{}, err
	}
	return res, nil
}
