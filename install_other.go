//go:build !windows

package main

import "errors"

func installExecutable() (string, error) {
	return "", errors.New("install is currently supported on Windows only")
}

func uninstallExecutable() (string, error) {
	return "", errors.New("uninstall is currently supported on Windows only")
}
