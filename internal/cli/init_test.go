package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestInitCommand(t *testing.T) {
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "acme", "--no-git"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Created acme/") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
	if _, err := os.Stat("acme/compose.yml"); err != nil {
		t.Errorf("compose.yml not created: %v", err)
	}
	if _, err := os.Stat("acme/.git"); err == nil {
		t.Errorf("--no-git still created a git repository")
	}
}

func TestInitCommandRejectsBadName(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCommand()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init", "Acme"})
	if err := root.Execute(); err == nil {
		t.Fatal("init Acme succeeded, want error")
	}
	if _, err := os.Stat("Acme"); err == nil {
		t.Error("directory created for invalid name")
	}
}
