//go:build !windows

package router

import "errors"

func gpuHolders() ([]Holder, error) {
	return nil, errors.New("no whole-device GPU instrument is implemented on this platform")
}
