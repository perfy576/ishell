//go:build !darwin && !windows

package main

func platformLocale() string { return "" }
