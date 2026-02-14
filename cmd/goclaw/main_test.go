package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseDaemonSubcommandArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    daemonSubcommandMode
		wantErr bool
	}{
		{name: "no args means run", args: nil, want: daemonSubcommandRun},
		{name: "double dash help", args: []string{"--help"}, want: daemonSubcommandHelp},
		{name: "single dash help", args: []string{"-h"}, want: daemonSubcommandHelp},
		{name: "help token", args: []string{"help"}, want: daemonSubcommandHelp},
		{name: "unexpected arg", args: []string{"extra"}, want: daemonSubcommandRun, wantErr: true},
		{name: "too many args", args: []string{"--help", "extra"}, want: daemonSubcommandRun, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDaemonSubcommandArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("mode mismatch: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestPrintDaemonSubcommandUsage(t *testing.T) {
	var buf bytes.Buffer
	printDaemonSubcommandUsage(&buf)
	out := buf.String()

	if !strings.Contains(out, "usage: goclaw daemon [--help]") {
		t.Fatalf("usage output missing daemon subcommand usage: %q", out)
	}
	if !strings.Contains(out, "goclaw -daemon") {
		t.Fatalf("usage output missing flag usage: %q", out)
	}
}
