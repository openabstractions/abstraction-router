//go:build !windows

package router

func powerState() string { return "power state not read on this platform" }
