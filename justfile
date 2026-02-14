set dotenv-load

bin     := "/tmp/goclaw"
home    := env("GOCLAW_HOME", "~/.goclaw")
addr    := "127.0.0.1:18789"

# list available recipes
default:
    @just --list

# build the goclaw binary
build:
    go build -o {{bin}} ./cmd/goclaw

# run all tests
test:
 go test ./... -count=1

# run tests with verbose output
test-v:
    go test ./... -count=1 -v

# run go vet
vet:
    go vet ./...

# build + vet + test
check: build vet test

# start the daemon (interactive, with TUI)
run: build
    {{bin}}

# start the daemon (headless, no TUI)
run-headless: build
    GOCLAW_NO_TUI=1 {{bin}}

# send a chat message via WebSocket (requires websocat)
chat session_id message:
    @echo '{"jsonrpc":"2.0","id":1,"method":"agent.chat","params":{"session_id":"{{session_id}}","content":"{{message}}"}}' | websocat ws://{{addr}}/ws

# check system status via WebSocket (requires websocat)
status:
    @echo '{"jsonrpc":"2.0","id":1,"method":"system.status"}' | websocat ws://{{addr}}/ws

# open an interactive WebSocket session (requires websocat)
ws:
    websocat ws://{{addr}}/ws

# fetch /metrics endpoint
metrics:
    curl -s http://{{addr}}/metrics | python3 -m json.tool

# tail the daemon logs
logs:
    tail -f {{home}}/logs/system.jsonl

# show the last 20 log lines
logs-recent:
    tail -20 {{home}}/logs/system.jsonl

# reset goclaw home (removes db, config, soul — asks for confirmation)
reset:
    @printf "This will delete all data in GOCLAW_HOME. Continue? [y/N] "
    @read r && case "$r" in [yY]|[yY][eE][sS]) ;; *) echo "Aborted."; exit 1 ;; esac
    rm -rf {{home}}
    @echo "Cleaned {{home}}. Run 'just run' to start fresh with the genesis wizard."

# clean build artifacts
clean:
    rm -f {{bin}}
    go clean ./...

# kill daemon, remove all data + config + build artifacts to start over
prune:
    @printf "This will kill the daemon and delete GOCLAW_HOME + build artifacts. Continue? [y/N] "
    @read r && case "$r" in [yY]|[yY][eE][sS]) ;; *) echo "Aborted."; exit 1 ;; esac
    -lsof -ti :18789 | xargs kill 2>/dev/null
    rm -rf {{home}}
    rm -f {{bin}}
    go clean ./...
    @echo "Pruned. Run 'just run' to start fresh."

# test, build, commit all, and push (wip)
push: test build
    git add -A && git commit -m "wip" && git push

# tidy go modules
tidy:
    go mod tidy

# show the current SOUL.md
soul:
    @cat {{home}}/SOUL.md 2>/dev/null || echo "No SOUL.md yet. Run 'just run' to create one."

# show the current config
config:
    @cat {{home}}/config.yaml 2>/dev/null || echo "No config.yaml yet. Run 'just run' to create one."
