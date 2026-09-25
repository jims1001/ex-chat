package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModuleMigrationRequiresExplicitTarget(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-module", "MSG"}, {"-module", "MSG", "-postgres"},
		{"-module", "MSG", "-postgres", "-sqlite-path", "must-not-create.db"},
		{"-module", "MSG", "-sqlite-path", "must-not-create.db", "unexpected"},
	} {
		if err := run(args, func(string) string { return "" }, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid invocation: %v", args)
		}
	}
}

func TestModuleMigrationCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.db")
	var output bytes.Buffer
	getenv := func(string) string { return "" }
	if err := run([]string{"-module", "UNKNOWN", "-sqlite-path", path}, getenv, &output); err == nil {
		t.Fatal("unknown module accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid module opened a database")
	}
	for i := 0; i < 2; i++ {
		if err := run([]string{"-module", "MSG", "-sqlite-path", path}, getenv, &output); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(output.String(), "MSG schema prepared") || !strings.Contains(output.String(), "not verified") {
		t.Fatal("schema preparation must not claim business extraction")
	}
}
