package wasm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
)

// OnToolLoadedFunc is called when a WASM module is successfully compiled and loaded.
type OnToolLoadedFunc func(name string)

type Watcher struct {
	skillDir string
	host     *Host
	logger   *slog.Logger

	events       chan string
	notify       chan Notification
	onToolLoaded OnToolLoadedFunc

	tinygoPath atomic.Pointer[string]
	lastError  atomic.Pointer[string]
}

type Notification struct {
	Level   string
	Message string
}

const requiredSkillABIVersion = "v1"

func NewWatcher(skillDir string, host *Host, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		skillDir: skillDir,
		host:     host,
		logger:   logger,
		events:   make(chan string, 16),
		notify:   make(chan Notification, 32),
	}
}

func (w *Watcher) ToolsUpdated() <-chan string {
	return w.events
}

func (w *Watcher) Notifications() <-chan Notification {
	return w.notify
}

// OnToolLoaded registers a callback invoked when a WASM module is loaded.
func (w *Watcher) OnToolLoaded(fn OnToolLoadedFunc) {
	w.onToolLoaded = fn
}

func (w *Watcher) TinygoStatus() (bool, string) {
	if p := w.tinygoPath.Load(); p != nil {
		return true, *p
	}
	if err := w.lastError.Load(); err != nil {
		return false, *err
	}
	return false, "tinygo not checked"
}

func (w *Watcher) Start(ctx context.Context) error {
	path, err := exec.LookPath("tinygo")
	if err != nil {
		msg := "tinygo not found in PATH (required for hot-swap)"
		w.lastError.Store(&msg)
		w.logger.Warn(msg)
		w.pushNotification("error", msg)
	} else {
		w.tinygoPath.Store(&path)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new fsnotify watcher: %w", err)
	}
	if err := watcher.Add(w.skillDir); err != nil {
		_ = watcher.Close()
		return fmt.Errorf("watch skill dir: %w", err)
	}

	go func() {
		defer watcher.Close()

		// Compile any existing source files on startup.
		matches, _ := filepath.Glob(filepath.Join(w.skillDir, "*.go"))
		for _, src := range matches {
			w.compileAndLoad(ctx, src)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if filepath.Ext(ev.Name) != ".go" {
					continue
				}
				go w.compileAndLoad(ctx, ev.Name)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				msg := err.Error()
				w.lastError.Store(&msg)
				w.logger.Error("skill watcher error", "error", err)
				w.pushNotification("error", msg)
			}
		}
	}()
	return nil
}

func (w *Watcher) compileAndLoad(ctx context.Context, src string) {
	tinygo := w.tinygoPath.Load()
	if tinygo == nil {
		msg := "tinygo unavailable; skipping compile"
		w.lastError.Store(&msg)
		w.pushNotification("error", msg)
		return
	}

	skillName := strings.TrimSuffix(filepath.Base(src), filepath.Ext(filepath.Base(src)))
	abiVersion, err := readSkillABIVersion(src)
	if err != nil {
		msg := fmt.Sprintf("failed to read ABI version for %s: %v", skillName, err)
		w.lastError.Store(&msg)
		w.pushNotification("error", msg)
		return
	}
	if abiVersion != requiredSkillABIVersion {
		msg := fmt.Sprintf("Skill ABI mismatch (%s): got %s want %s", skillName, abiVersion, requiredSkillABIVersion)
		w.lastError.Store(&msg)
		w.pushNotification("error", msg)
		return
	}
	w.pushNotification("info", fmt.Sprintf("Compiling %s...", skillName))

	finalOut := strings.TrimSuffix(src, filepath.Ext(src)) + ".wasm"
	stagedOut := strings.TrimSuffix(src, filepath.Ext(src)) + ".staged.wasm"
	cmd := exec.CommandContext(ctx, *tinygo, "build", "-target=wasi", "-o", stagedOut, src)
	bytes, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("tinygo build failed for %s: %v: %s", src, err, strings.TrimSpace(string(bytes)))
		w.lastError.Store(&msg)
		w.logger.Error("skill compile failed", "src", src, "error", err, "output", strings.TrimSpace(string(bytes)))
		w.pushNotification("error", fmt.Sprintf("Skill compile error (%s): %s", skillName, strings.TrimSpace(string(bytes))))
		return
	}

	wasmBytes, err := os.ReadFile(stagedOut)
	if err != nil {
		msg := fmt.Sprintf("failed reading staged wasm for %s: %v", skillName, err)
		w.lastError.Store(&msg)
		w.pushNotification("error", msg)
		return
	}
	if err := w.host.LoadModuleFromBytes(ctx, skillName, wasmBytes, stagedOut); err != nil {
		msg := err.Error()
		w.lastError.Store(&msg)
		w.logger.Error("skill load failed", "wasm", stagedOut, "error", err)
		w.pushNotification("error", fmt.Sprintf("Skill load error (%s): %v", skillName, err))
		return
	}
	if err := os.Rename(stagedOut, finalOut); err != nil {
		msg := fmt.Sprintf("failed promoting staged wasm for %s: %v", skillName, err)
		w.lastError.Store(&msg)
		w.pushNotification("warn", msg)
	}
	moduleName := skillName
	if w.onToolLoaded != nil {
		w.onToolLoaded(moduleName)
	}
	select {
	case w.events <- filepath.Base(src):
	default:
	}
	w.pushNotification("info", fmt.Sprintf("Skill Loaded: %s", moduleName))
	w.logger.Info("skill hot-swapped", "src", src, "wasm", finalOut)
}

func (w *Watcher) pushNotification(level, msg string) {
	select {
	case w.notify <- Notification{
		Level:   level,
		Message: msg,
	}:
	default:
	}
}

func readSkillABIVersion(src string) (string, error) {
	abiPath := strings.TrimSuffix(src, filepath.Ext(src)) + ".abi"
	data, err := os.ReadFile(abiPath)
	if err != nil {
		if os.IsNotExist(err) {
			return requiredSkillABIVersion, nil
		}
		return "", err
	}
	version := strings.TrimSpace(string(data))
	if version == "" {
		return requiredSkillABIVersion, nil
	}
	return version, nil
}
