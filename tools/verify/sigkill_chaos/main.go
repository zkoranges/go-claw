//go:build ignore

// sigkill_chaos is a standalone chaos test that verifies GoClaw's crash
// recovery guarantees. It builds the daemon binary, starts it, inserts
// tasks directly into SQLite, SIGKILLs the daemon, restarts it, and
// verifies that:
//   - The database is not corrupted (opens and queries cleanly)
//   - Previously RUNNING tasks are recovered to QUEUED on restart
//
// Usage:
//
//	go run ./tools/verify/sigkill_chaos/
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

const sessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("VERDICT PASS (sigkill_chaos)")
}

func run() error {
	ctx := context.Background()

	// 1. Build the goclaw binary.
	root := moduleRoot()
	binDir, err := os.MkdirTemp("", "sigkill-chaos-bin-*")
	if err != nil {
		return fmt.Errorf("mktemp bin: %w", err)
	}
	defer os.RemoveAll(binDir)
	binPath := filepath.Join(binDir, "goclaw")

	fmt.Println("BUILD goclaw binary...")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/goclaw")
	build.Dir = root
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build binary: %w", err)
	}

	// 2. Create a temp GOCLAW_HOME with minimal config.
	home, err := os.MkdirTemp("", "sigkill-chaos-home-*")
	if err != nil {
		return fmt.Errorf("mktemp home: %w", err)
	}
	defer os.RemoveAll(home)

	addr := pickFreeAddr()
	configYAML := fmt.Sprintf("bind_addr: %q\nworker_count: 1\ntask_timeout_seconds: 600\n", addr)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	policyYAML := "allow_capabilities:\n  - acp.read\n  - acp.mutate\n"
	if err := os.WriteFile(filepath.Join(home, "policy.yaml"), []byte(policyYAML), 0o644); err != nil {
		return fmt.Errorf("write policy: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.token"), []byte("chaos-test-token\n"), 0o600); err != nil {
		return fmt.Errorf("write auth token: %w", err)
	}

	daemonEnv := append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_NO_TUI=1",
	)

	// 3. Start the daemon.
	fmt.Println("START daemon (first run)...")
	daemon := exec.Command(binPath, "-daemon")
	daemon.Env = daemonEnv
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// 4. Wait for healthy.
	fmt.Println("WAIT for /healthz...")
	if err := waitHealthy(addr, 10*time.Second); err != nil {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
		return fmt.Errorf("daemon not healthy: %w", err)
	}
	fmt.Println("HEALTHY")

	// 5. Insert tasks directly via SQLite and transition one to RUNNING.
	dbPath := filepath.Join(home, "goclaw.db")
	store, err := persistence.Open(dbPath)
	if err != nil {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
		return fmt.Errorf("open store: %w", err)
	}
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		store.Close()
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
		return fmt.Errorf("ensure session: %w", err)
	}

	var taskIDs []string
	for i := 0; i < 3; i++ {
		id, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"chaos-task-%d"}`, i))
		if err != nil {
			store.Close()
			_ = daemon.Process.Kill()
			_ = daemon.Wait()
			return fmt.Errorf("create task %d: %w", i, err)
		}
		taskIDs = append(taskIDs, id)
		fmt.Printf("CREATED task %s\n", id)
	}

	// Claim and start the first task to put it in RUNNING state.
	task, err := store.ClaimNextPendingTask(ctx)
	if err != nil || task == nil {
		store.Close()
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
		return fmt.Errorf("claim task: %w (task=%v)", err, task)
	}
	if err := store.StartTaskRun(ctx, task.ID, task.LeaseOwner, ""); err != nil {
		store.Close()
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
		return fmt.Errorf("start task run: %w", err)
	}
	fmt.Printf("RUNNING task %s (lease_owner=%s)\n", task.ID, task.LeaseOwner)
	runningTaskID := task.ID
	store.Close()

	// 6. SIGKILL the daemon.
	fmt.Println("SIGKILL daemon...")
	if err := daemon.Process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("sigkill: %w", err)
	}
	_ = daemon.Wait()
	fmt.Println("DAEMON killed")

	// Brief pause to ensure port is released.
	time.Sleep(500 * time.Millisecond)

	// 7. Restart the daemon.
	fmt.Println("RESTART daemon (second run)...")
	daemon2 := exec.Command(binPath, "-daemon")
	daemon2.Env = daemonEnv
	daemon2.Stdout = os.Stdout
	daemon2.Stderr = os.Stderr
	if err := daemon2.Start(); err != nil {
		return fmt.Errorf("restart daemon: %w", err)
	}
	defer func() {
		_ = daemon2.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = daemon2.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = daemon2.Process.Kill()
			_ = daemon2.Wait()
		}
	}()

	if err := waitHealthy(addr, 10*time.Second); err != nil {
		return fmt.Errorf("restarted daemon not healthy: %w", err)
	}
	fmt.Println("HEALTHY (after restart)")

	// 8. Verify DB integrity and task recovery.
	store2, err := persistence.Open(dbPath)
	if err != nil {
		return fmt.Errorf("reopen store after kill: %w", err)
	}
	defer store2.Close()

	// Verify: the previously RUNNING task should now be QUEUED (recovered).
	recovered, err := store2.GetTask(ctx, runningTaskID)
	if err != nil {
		return fmt.Errorf("get recovered task: %w", err)
	}
	fmt.Printf("RECOVERED task %s status=%s\n", recovered.ID, recovered.Status)
	if recovered.Status != persistence.TaskStatusQueued {
		return fmt.Errorf("expected task %s to be QUEUED after recovery, got %s", runningTaskID, recovered.Status)
	}

	// Verify: DB integrity check via PRAGMA.
	var integrityResult string
	if err := store2.DB().QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&integrityResult); err != nil {
		return fmt.Errorf("integrity check: %w", err)
	}
	fmt.Printf("INTEGRITY_CHECK=%s\n", integrityResult)
	if integrityResult != "ok" {
		return fmt.Errorf("DB integrity check failed: %s", integrityResult)
	}

	fmt.Println("ALL CHECKS PASSED")
	return nil
}

func moduleRoot() string {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go env GOMOD: %v\n", err)
		os.Exit(1)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		fmt.Fprintln(os.Stderr, "go env GOMOD returned empty; expected path to go.mod")
		os.Exit(1)
	}
	return filepath.Dir(gomod)
}

func pickFreeAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pick free addr: %v\n", err)
		os.Exit(1)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitHealthy(addr string, timeout time.Duration) error {
	url := fmt.Sprintf("http://%s/healthz", addr)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("healthz at %s not OK after %v", addr, timeout)
}
