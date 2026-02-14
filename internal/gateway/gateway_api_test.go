package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/agent"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/gateway"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/tools"

	_ "github.com/mattn/go-sqlite3"
)

// apiTestServer sets up a gateway test server and returns the httptest.Server plus the store.
// Caller is responsible for calling ts.Close() via t.Cleanup.
func apiTestServer(t *testing.T, opts ...func(*gateway.Config)) (*httptest.Server, *persistence.Store) {
	t.Helper()
	store := openStoreForGatewayTest(t)

	eng := engine.New(store, nil, engine.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		MaxQueueDepth: 100,
	})

	cfg := gateway.Config{
		Store:             store,
		Registry: func() *agent.Registry {
			reg := agent.NewRegistry(store, nil, nil, nil, nil)
			reg.RegisterTestAgent("default", eng)
			return reg
		}(),
		Policy:            gatewayTestPolicy,
		AuthToken:         gatewayTestAuthToken,
		ConfigFingerprint: "test-fingerprint-abc123",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	srv := gateway.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts, store
}

// apiGet performs an authenticated GET request and returns the response.
func apiGet(t *testing.T, ts *httptest.Server, path string, authenticated bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// decodeJSON reads and decodes the response body into a map.
func decodeJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode JSON response: %v\nbody: %s", err, string(body))
	}
	return result
}

func TestAPITasks_ListAll(t *testing.T) {
	ts, store := apiTestServer(t)
	ctx := context.Background()

	// Create sessions and tasks.
	sessionID := "a1a1a1a1-b2b2-c3c3-d4d4-e5e5e5e5e501"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	var taskIDs []string
	for i := 0; i < 3; i++ {
		taskID, err := store.CreateTask(ctx, sessionID, `{"content":"task-`+string(rune('A'+i))+`"}`)
		if err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		taskIDs = append(taskIDs, taskID)
	}

	resp := apiGet(t, ts, "/api/tasks", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := decodeJSON(t, resp)

	// Verify "tasks" array.
	tasksRaw, ok := body["tasks"]
	if !ok {
		t.Fatalf("response missing 'tasks' key, got: %v", body)
	}
	tasks, ok := tasksRaw.([]interface{})
	if !ok {
		t.Fatalf("'tasks' is not an array, got: %T", tasksRaw)
	}
	if len(tasks) < 3 {
		t.Fatalf("expected at least 3 tasks, got %d", len(tasks))
	}

	// Verify "total" count.
	totalRaw, ok := body["total"]
	if !ok {
		t.Fatalf("response missing 'total' key, got: %v", body)
	}
	total, ok := totalRaw.(float64)
	if !ok || int(total) < 3 {
		t.Fatalf("expected total >= 3, got %v", totalRaw)
	}

	// Verify all created task IDs appear in results.
	foundIDs := map[string]bool{}
	for _, raw := range tasks {
		taskMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := taskMap["id"].(string); ok {
			foundIDs[id] = true
		}
	}
	for _, id := range taskIDs {
		if !foundIDs[id] {
			t.Errorf("task %s not found in response", id)
		}
	}
}

func TestAPITasks_FilterByStatus(t *testing.T) {
	ts, store := apiTestServer(t)
	ctx := context.Background()

	sessionID := "b1b1b1b1-c2c2-d3d3-e4e4-f5f5f5f5f502"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Create 3 tasks — all start as QUEUED.
	var taskIDs []string
	for i := 0; i < 3; i++ {
		taskID, err := store.CreateTask(ctx, sessionID, `{"content":"filter-test"}`)
		if err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		taskIDs = append(taskIDs, taskID)
	}

	// Transition one task to SUCCEEDED: claim -> start run -> complete.
	claimedTask, err := store.ClaimNextPendingTask(ctx)
	if err != nil || claimedTask == nil {
		t.Fatalf("claim task: task=%v err=%v", claimedTask, err)
	}
	if err := store.StartTaskRun(ctx, claimedTask.ID, claimedTask.LeaseOwner, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.CompleteTask(ctx, claimedTask.ID, `{"reply":"done"}`); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	// Filter for QUEUED — should return only the remaining QUEUED tasks (2 tasks).
	resp := apiGet(t, ts, "/api/tasks?status=QUEUED", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)

	tasks, ok := body["tasks"].([]interface{})
	if !ok {
		t.Fatalf("'tasks' is not an array, got: %T", body["tasks"])
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 QUEUED tasks, got %d", len(tasks))
	}

	// Verify all returned tasks have status QUEUED.
	for _, raw := range tasks {
		taskMap, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("task entry is not a map")
		}
		status, ok := taskMap["status"].(string)
		if !ok || status != "QUEUED" {
			t.Errorf("expected status QUEUED, got %v", taskMap["status"])
		}
	}

	// Verify total count matches filter.
	total, ok := body["total"].(float64)
	if !ok || int(total) != 2 {
		t.Errorf("expected total=2 for QUEUED filter, got %v", body["total"])
	}

	// Filter for SUCCEEDED — should return 1 task.
	respSucceeded := apiGet(t, ts, "/api/tasks?status=SUCCEEDED", true)
	if respSucceeded.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", respSucceeded.StatusCode)
	}
	bodySucceeded := decodeJSON(t, respSucceeded)
	succeededTasks, ok := bodySucceeded["tasks"].([]interface{})
	if !ok {
		t.Fatalf("'tasks' is not an array for SUCCEEDED filter")
	}
	if len(succeededTasks) != 1 {
		t.Fatalf("expected 1 SUCCEEDED task, got %d", len(succeededTasks))
	}
}

func TestAPITasks_RequiresAuth(t *testing.T) {
	ts, _ := apiTestServer(t)

	// No auth header.
	resp := apiGet(t, ts, "/api/tasks", false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %d", resp.StatusCode)
	}

	// Wrong token.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer wrong-token-xyz")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request with wrong token: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp2.StatusCode)
	}

	// Also verify other API endpoints require auth.
	endpoints := []string{
		"/api/sessions",
		"/api/skills",
		"/api/config",
	}
	for _, ep := range endpoints {
		r := apiGet(t, ts, ep, false)
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s without auth, got %d", ep, r.StatusCode)
		}
	}
}

func TestAPISessions_List(t *testing.T) {
	ts, store := apiTestServer(t)
	ctx := context.Background()

	// Create sessions.
	sessionIDs := []string{
		"c1c1c1c1-d2d2-e3e3-f4f4-a5a5a5a5a503",
		"c1c1c1c1-d2d2-e3e3-f4f4-a5a5a5a5a504",
		"c1c1c1c1-d2d2-e3e3-f4f4-a5a5a5a5a505",
	}
	for _, id := range sessionIDs {
		if err := store.EnsureSession(ctx, id); err != nil {
			t.Fatalf("ensure session %s: %v", id, err)
		}
	}

	resp := apiGet(t, ts, "/api/sessions", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)

	sessionsRaw, ok := body["sessions"]
	if !ok {
		t.Fatalf("response missing 'sessions' key, got: %v", body)
	}
	sessions, ok := sessionsRaw.([]interface{})
	if !ok {
		t.Fatalf("'sessions' is not an array, got: %T", sessionsRaw)
	}
	if len(sessions) < 3 {
		t.Fatalf("expected at least 3 sessions, got %d", len(sessions))
	}

	// Verify all created session IDs appear in results.
	foundIDs := map[string]bool{}
	for _, raw := range sessions {
		sessionMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := sessionMap["id"].(string); ok {
			foundIDs[id] = true
		}
	}
	for _, id := range sessionIDs {
		if !foundIDs[id] {
			t.Errorf("session %s not found in response", id)
		}
	}

	// Verify limit parameter works.
	respLimited := apiGet(t, ts, "/api/sessions?limit=1", true)
	if respLimited.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for limited request, got %d", respLimited.StatusCode)
	}
	bodyLimited := decodeJSON(t, respLimited)
	limitedSessions, ok := bodyLimited["sessions"].([]interface{})
	if !ok {
		t.Fatalf("'sessions' is not an array in limited response")
	}
	if len(limitedSessions) != 1 {
		t.Fatalf("expected 1 session with limit=1, got %d", len(limitedSessions))
	}
}

func TestAPISkills_List(t *testing.T) {
	// Test with nil SkillsStatus (returns empty array).
	ts, _ := apiTestServer(t)

	resp := apiGet(t, ts, "/api/skills", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)

	skillsRaw, ok := body["skills"]
	if !ok {
		t.Fatalf("response missing 'skills' key, got: %v", body)
	}
	skills, ok := skillsRaw.([]interface{})
	if !ok {
		t.Fatalf("'skills' is not an array, got: %T", skillsRaw)
	}
	// With nil SkillsStatus, should be an empty array.
	if len(skills) != 0 {
		t.Fatalf("expected empty skills array with nil SkillsStatus, got %d items", len(skills))
	}

	// Test with a real SkillsStatus function that returns skills.
	ts2, _ := apiTestServer(t, func(cfg *gateway.Config) {
		cfg.SkillsStatus = func(ctx context.Context) ([]tools.SkillStatus, error) {
			return []tools.SkillStatus{
				{
					Info:       tools.SkillInfo{Name: "test_skill"},
					Configured: true,
					Enabled:    true,
					Eligible:   true,
					State:      "active",
				},
			}, nil
		}
	})

	resp2 := apiGet(t, ts2, "/api/skills", true)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for skills with provider, got %d", resp2.StatusCode)
	}
	body2 := decodeJSON(t, resp2)
	skills2, ok := body2["skills"].([]interface{})
	if !ok {
		t.Fatalf("'skills' is not an array in provider response")
	}
	if len(skills2) != 1 {
		t.Fatalf("expected 1 skill from provider, got %d", len(skills2))
	}
}

func TestAPIConfig_HidesSecrets(t *testing.T) {
	ts, _ := apiTestServer(t)

	resp := apiGet(t, ts, "/api/config", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)

	// Verify expected fields are present.
	configHash, ok := body["config_hash"]
	if !ok {
		t.Fatalf("response missing 'config_hash' key, got: %v", body)
	}
	if configHash != "test-fingerprint-abc123" {
		t.Errorf("expected config_hash 'test-fingerprint-abc123', got %v", configHash)
	}

	policyVersion, ok := body["policy_version"]
	if !ok {
		t.Fatalf("response missing 'policy_version' key, got: %v", body)
	}
	if pv, ok := policyVersion.(string); !ok || pv == "" {
		t.Errorf("expected non-empty policy_version string, got %v", policyVersion)
	}

	// Verify NO secrets or sensitive fields are exposed.
	// Re-read the raw JSON to check for any key/secret patterns.
	resp2 := apiGet(t, ts, "/api/config", true)
	defer resp2.Body.Close()
	rawBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	rawStr := strings.ToLower(string(rawBody))

	sensitivePatterns := []string{
		"api_key",
		"api-key",
		"apikey",
		"secret",
		"password",
		"token",
		"auth_token",
		"bearer",
		gatewayTestAuthToken, // The actual token value must not appear.
	}
	for _, pattern := range sensitivePatterns {
		if strings.Contains(rawStr, strings.ToLower(pattern)) {
			t.Errorf("config response contains sensitive pattern %q: %s", pattern, string(rawBody))
		}
	}

	// Verify only expected keys are present (no extra fields).
	if len(body) != 2 {
		t.Errorf("expected exactly 2 fields (config_hash, policy_version), got %d: %v", len(body), body)
	}
}
