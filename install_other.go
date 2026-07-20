//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func installExecutable() (string, error) {
	target, err := installedExecutablePath()
	if err != nil {
		return "", err
	}
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !samePath(source, target) {
		if err := copyExecutable(source, target); err != nil {
			return "", err
		}
	}
	if err := updateShellPath(filepath.Dir(target), true); err != nil {
		return "", err
	}
	return target, nil
}

func uninstallExecutable() (string, error) {
	target, err := installedExecutablePath()
	if err != nil {
		return "", err
	}
	if err := updateShellPath(filepath.Dir(target), false); err != nil {
		return "", err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	_ = os.Remove(filepath.Dir(target))
	return "iShell removed from your user PATH.", nil
}

func installedExecutablePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ishell", "bin", "ishell"), nil
}

const (
	pathBlockStart = "# >>> ishell PATH >>>"
	pathBlockEnd   = "# <<< ishell PATH <<<"
)

func copyExecutable(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(target), ".ishell-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0700); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, target)
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func updateShellPath(directory string, add bool) error {
	config, command, err := shellPathConfig(directory)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(config)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) && !add {
		return nil
	}
	updated, err := removePathBlock(string(contents))
	if err != nil {
		return fmt.Errorf("update %s: %w", config, err)
	}
	if add {
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += pathBlockStart + "\n" + command + "\n" + pathBlockEnd + "\n"
	}
	if updated == string(contents) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		return err
	}
	return os.WriteFile(config, []byte(updated), 0600)
}

func shellPathConfig(directory string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	shell := filepath.Base(strings.TrimSpace(os.Getenv("SHELL")))
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "zsh"
		} else {
			shell = "bash"
		}
	}
	command := "export PATH=" + shellQuote(directory) + ":\"$PATH\""
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), command, nil
	case "bash":
		name := ".bashrc"
		if runtime.GOOS == "darwin" {
			name = ".bash_profile"
		}
		return filepath.Join(home, name), command, nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "conf.d", "ishell.fish"), "fish_add_path -m " + shellQuote(directory), nil
	default:
		return "", "", fmt.Errorf("unsupported shell %q; add %s to its startup file", shell, command)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func removePathBlock(contents string) (string, error) {
	for {
		start := strings.Index(contents, pathBlockStart)
		if start == -1 {
			return contents, nil
		}
		end := strings.Index(contents[start+len(pathBlockStart):], pathBlockEnd)
		if end == -1 {
			return "", fmt.Errorf("missing %s", pathBlockEnd)
		}
		end += start + len(pathBlockStart) + len(pathBlockEnd)
		if strings.HasPrefix(contents[end:], "\r\n") {
			end += 2
		} else if strings.HasPrefix(contents[end:], "\n") {
			end++
		}
		contents = contents[:start] + contents[end:]
	}
}
