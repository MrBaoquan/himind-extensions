// Package capability consumes declared dependencies through the Agent's per-call broker.
package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

var ErrUnavailable = errors.New("Agent capability broker is unavailable")

func Available() bool {
	return os.Getenv("HIMIND_CAPABILITY_BROKER_URL") != "" && os.Getenv("HIMIND_CAPABILITY_BROKER_TOKEN") != ""
}

func Invoke(ctx context.Context, name string, input any, output any) error {
	if !Available() {
		return ErrUnavailable
	}
	endpoint, err := url.Parse(os.Getenv("HIMIND_CAPABILITY_BROKER_URL"))
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.Path != "/invoke" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("invalid Agent capability broker address")
	}
	body, err := json.Marshal(map[string]any{"capability": name, "input": input})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+os.Getenv("HIMIND_CAPABILITY_BROKER_TOKEN"))
	client := &http.Client{Timeout: 120 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("broker redirect refused") }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Agent capability call failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 16*1024*1024 {
		return errors.New("Agent capability response exceeds budget")
	}
	if response.StatusCode != http.StatusOK {
		// Never include raw response content, endpoints or broker tokens in diagnostics.
		return fmt.Errorf("Agent capability %s failed (HTTP %d)", name, response.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return errors.New("invalid Agent capability response")
	}
	if len(envelope.Result) == 0 {
		return errors.New("Agent capability response missing result")
	}
	return json.Unmarshal(envelope.Result, output)
}
