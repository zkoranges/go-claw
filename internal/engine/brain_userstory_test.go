package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
)

func TestUserStory_US2_MultiStepResearchRecordsSearchAndReadTasks(t *testing.T) {
	// [SPEC: SPEC-GOAL-G3, SPEC-DATA-SCHEMA-1] [PDR: V-8, V-10]
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	baseURL := ""
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "text/html")
			if strings.Contains(q, "5090") {
				_, _ = w.Write([]byte(`<html><body>
					<div class="result">
						<a class="result__a" href="` + baseURL + `/read/5090">RTX 5090 street price is $1999</a>
						<a class="result__snippet">RTX 5090 street price is $1999</a>
					</div>
				</body></html>`))
				return
			}
			_, _ = w.Write([]byte(`<html><body>
				<div class="result">
					<a class="result__a" href="` + baseURL + `/read/4090">RTX 4090 current price is $1599</a>
					<a class="result__snippet">RTX 4090 current price is $1599</a>
				</div>
			</body></html>`))
		case "/read/5090":
			_, _ = w.Write([]byte("<html><body>Market listing: RTX 5090 $1999.</body></html>"))
		case "/read/4090":
			_, _ = w.Write([]byte("<html><body>Market listing: RTX 4090 $1599.</body></html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer searchSrv.Close()
	baseURL = searchSrv.URL
	searchURL := searchSrv.URL + "/search"

	t.Setenv("GOCLAW_SEARCH_ENDPOINT", searchURL)

	u, err := url.Parse(searchSrv.URL)
	if err != nil {
		t.Fatalf("parse search server url: %v", err)
	}

	pol := policy.Policy{
		AllowDomains:      []string{u.Hostname()},
		AllowCapabilities: []string{"tools.web_search", "tools.read_url"},
		AllowLoopback:     true,
	}
	brain := engine.NewGenkitBrain(context.Background(), store, engine.BrainConfig{
		Policy: pol,
		Soul:   "You are a technical assistant.",
	})

	sessionID := "7bf10d75-8e68-4b74-a7db-73575a8f53ab"
	if err := store.EnsureSession(context.Background(), sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	reply, err := brain.Respond(context.Background(), sessionID, "Find the price of RTX 5090 and compare it to 4090")
	if err != nil {
		t.Fatalf("brain respond: %v", err)
	}
	if !strings.Contains(reply, "$1999") || !strings.Contains(reply, "$1599") {
		t.Fatalf("expected synthesized reply to include both prices, got %q", reply)
	}

	tasks, err := store.ListTasksBySession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list tasks by session: %v", err)
	}

	var hasSearch, hasRead bool
	for _, task := range tasks {
		var payload map[string]any
		if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
			continue
		}
		tool, _ := payload["tool"].(string)
		switch strings.ToLower(tool) {
		case "search":
			hasSearch = true
		case "read":
			hasRead = true
		}
	}
	if !hasSearch || !hasRead {
		t.Fatalf("expected distinct Search and Read task records, got search=%t read=%t", hasSearch, hasRead)
	}
}

func TestUserStory_US3_RandomSkillCanAnswerImmediately(t *testing.T) {
	// [SPEC: SPEC-GOAL-G4] [PDR: V-26]
	store := openStoreForEngineTest(t)
	brain := engine.NewGenkitBrain(context.Background(), store, engine.BrainConfig{
		Policy: policy.Default(),
		Soul:   "You are a friendly assistant.",
	})
	brain.RegisterSkill("random")

	reply, err := brain.Respond(context.Background(), "1bcde8fa-bf5f-4c07-8624-5b8440ad5b9f", "Generate a random number")
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "random number") {
		t.Fatalf("expected random skill response, got %q", reply)
	}
}
