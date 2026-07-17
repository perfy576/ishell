//go:build !windows && !darwin && !linux

package main

import "errors"

func launchDetachedQuickCommand(commandLine string) error {
	return errors.New("detached quick command terminals are currently supported on Windows only")
}
