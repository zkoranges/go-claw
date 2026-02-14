package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeleteWordLeft(t *testing.T) {
	in := []rune("hello   world")
	out, cur := deleteWordLeft(in, len(in))
	if string(out) != "hello   " {
		t.Fatalf("unexpected out: %q", string(out))
	}
	if cur != len([]rune("hello   ")) {
		t.Fatalf("unexpected cursor: %d", cur)
	}
}

func TestDeleteWordLeft_SkipsSpacesThenWord(t *testing.T) {
	in := []rune("abc   ")
	out, cur := deleteWordLeft(in, len(in))
	if string(out) != "" {
		t.Fatalf("unexpected out: %q", string(out))
	}
	if cur != 0 {
		t.Fatalf("unexpected cursor: %d", cur)
	}
}

func TestHandleCommand_HelpWritesOutput(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	shouldExit := handleCommand("/help", &cc, "sess", &buf)
	if shouldExit {
		t.Fatalf("expected shouldExit=false")
	}
	if !strings.Contains(buf.String(), "Commands:") {
		t.Fatalf("expected help output, got: %q", buf.String())
	}
}
