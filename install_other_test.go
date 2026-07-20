//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAndUninstallExecutableWithZsh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	config := filepath.Join(home, ".zshrc")
	original := "export EDITOR=vi\n"
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	target, err := installExecutable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Fatalf("installed executable mode = %o, want owner execute bit", info.Mode().Perm())
	}
	contents, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "export PATH='"+filepath.Dir(target)+"':\"$PATH\"") {
		t.Fatalf("zsh config does not add installed directory: %q", contents)
	}
	if zsh, err := exec.LookPath("zsh"); err == nil {
		command := exec.Command(zsh, "-i", "-c", "command -v ishell")
		command.Env = append(os.Environ(), "ZDOTDIR="+home)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("find ishell in a new zsh session: %v", err)
		}
		if got := strings.TrimSpace(string(output)); got != target {
			t.Fatalf("zsh resolved ishell as %q, want %q", got, target)
		}
	}

	if _, err := installExecutable(); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(contents), pathBlockStart); count != 1 {
		t.Fatalf("PATH block count = %d, want 1", count)
	}

	if _, err := uninstallExecutable(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("installed executable still exists: %v", err)
	}
	contents, err = os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(contents); got != original {
		t.Fatalf("zsh config after uninstall = %q, want %q", got, original)
	}
}
