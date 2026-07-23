//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

type breakProcess struct {
	pid     int
	parent  int
	command string
}

func breakAllProcesses() (string, error) {
	processes, err := listBreakProcesses()
	if err != nil {
		return "", err
	}
	current := os.Getpid()
	killed := 0
	var failures []string
	ids := breakProcessIDs(processes)
	for index := len(ids) - 1; index >= 0; index-- {
		pid := ids[index]
		if pid == current {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				continue
			}
			failures = append(failures, fmt.Sprintf("%d: %v", pid, err))
			continue
		}
		killed++
	}
	if len(failures) != 0 {
		return "", fmt.Errorf("force disconnect iShell processes: %s", strings.Join(failures, "; "))
	}
	if killed == 0 {
		return "No other iShell processes are running.", nil
	}
	return fmt.Sprintf("Forcefully disconnected %d iShell-related process(es).", killed), nil
}

func listBreakProcesses() ([]breakProcess, error) {
	output, err := exec.Command("ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	return parseBreakProcesses(string(output)), nil
}

func parseBreakProcesses(output string) []breakProcess {
	var processes []breakProcess
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil || pid <= 0 {
			continue
		}
		processes = append(processes, breakProcess{pid: pid, parent: parent, command: strings.Join(fields[2:], " ")})
	}
	return processes
}

func breakProcessIDs(processes []breakProcess) []int {
	children := make(map[int][]int, len(processes))
	var roots []int
	for _, process := range processes {
		children[process.parent] = append(children[process.parent], process.pid)
		if strings.EqualFold(filepath.Base(process.command), appName) {
			roots = append(roots, process.pid)
		}
	}
	targets := make(map[int]bool, len(processes))
	for _, root := range roots {
		for pending := []int{root}; len(pending) != 0; {
			pid := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if targets[pid] {
				continue
			}
			targets[pid] = true
			pending = append(pending, children[pid]...)
		}
	}
	ids := make([]int, 0, len(targets))
	for pid := range targets {
		ids = append(ids, pid)
	}
	sort.Ints(ids)
	return ids
}
