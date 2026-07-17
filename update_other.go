//go:build !windows

package main

import "os"

func replaceRunningExecutable(staged, target string) (string, error) {
	if err := os.Chmod(staged, 0700); err != nil {
		return "", err
	}
	if err := os.Rename(staged, target); err != nil {
		return "", err
	}
	return "iShell updated. Restart it to use the new version.", nil
}

func updateDestination(current string) (string, error) {
	return current, nil
}
