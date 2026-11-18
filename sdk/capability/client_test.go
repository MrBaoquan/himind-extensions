package capability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrokerAuthenticationEnvelopeAndErrorRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/invoke" || r.Header.Get("Authorization") != "Bearer synthetic-secret" {
			t.Error("missing scoped credentials")
		}
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid envelope")
		}
		if request["capability"] != "workspace.session.activity" {
			t.Error("wrong capability")
		}
		if request["input"].(map[string]any)["scope"] != "all" {
			t.Error("lost input")
		}
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"complete": true}})
	}))
	defer server.Close()
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", server.URL+"/invoke")
	t.Setenv("HIMIND_CAPABILITY_BROKER_TOKEN", "synthetic-secret")
	var output map[string]any
	if err := Invoke(context.Background(), "workspace.session.activity", map[string]any{"scope": "all"}, &output); err != nil || output["complete"] != true {
		t.Fatalf("call failed: %v", err)
	}
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("synthetic-secret private-body"))
	}))
	defer forbidden.Close()
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", forbidden.URL+"/invoke")
	err := Invoke(context.Background(), "workspace.session.activity", nil, &output)
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "private-body") {
		t.Fatalf("bad diagnostic: %v", err)
	}
}

func TestBrokerRejectsRemoteRedirectMissingResultAndCancelledRequest(t *testing.T) {
	t.Setenv("HIMIND_CAPABILITY_BROKER_TOKEN", "synthetic")
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", "http://example.com:1234/invoke")
	var output any
	if Invoke(context.Background(), "x", nil, &output) == nil {
		t.Fatal("accepted remote broker")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") == "unused" {
			return
		}
		http.Redirect(w, r, "http://example.com/forbidden", 302)
	}))
	defer server.Close()
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", server.URL+"/invoke")
	if Invoke(context.Background(), "x", nil, &output) == nil {
		t.Fatal("followed redirect")
	}
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"error":"no result"}`)) }))
	defer invalid.Close()
	t.Setenv("HIMIND_CAPABILITY_BROKER_URL", invalid.URL+"/invoke")
	if Invoke(context.Background(), "x", nil, &output) == nil {
		t.Fatal("accepted missing result")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if Invoke(ctx, "x", nil, &output) == nil {
		t.Fatal("ignored cancellation")
	}
}
