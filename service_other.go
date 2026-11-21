//go:build !windows

package main

import (
	"errors"
	"net/http"
)

func serviceCommand(path, command string) (bool, error) {
	if command != "" {
		return true, errors.New("service commands are supported only on Windows")
	}
	return false, nil
}
func runPlatformService(server *http.Server) (bool, error) { return false, nil }
