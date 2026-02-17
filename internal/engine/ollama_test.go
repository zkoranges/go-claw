package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectOllamaTools_Supported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req struct{ Model string `json:"model"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Model != "llama3.1:8b" {
			t.Fatalf("model = %q, want llama3.1:8b", req.Model)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion", "tools"},
		})
	}))
	defer srv.Close()

	got := detectOllamaTools(srv.URL+"/v1", "ollama/llama3.1:8b")
	if !got {
		t.Fatal("expected tools supported")
	}
}

func TestDetectOllamaTools_NotSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion"},
		})
	}))
	defer srv.Close()

	got := detectOllamaTools(srv.URL+"/v1", "gemma:2b")
	if got {
		t.Fatal("expected tools NOT supported")
	}
}

func TestDetectOllamaTools_Unreachable(t *testing.T) {
	got := detectOllamaTools("http://127.0.0.1:1/v1", "any")
	if got {
		t.Fatal("expected false when server unreachable")
	}
}

func TestDetectOllamaTools_StripsPrefix(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Model string `json:"model"` }
		json.NewDecoder(r.Body).Decode(&req)
		receivedModel = req.Model
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"tools"},
		})
	}))
	defer srv.Close()

	detectOllamaTools(srv.URL+"/v1", "ollama/qwen3:8b")
	if receivedModel != "qwen3:8b" {
		t.Fatalf("model sent to Ollama = %q, want qwen3:8b (ollama/ prefix stripped)", receivedModel)
	}
}
