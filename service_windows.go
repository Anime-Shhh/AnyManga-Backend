package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "AnyMangaBackend"

func serviceCommand(configPath, command string) (bool, error) {
	if command == "" {
		return false, nil
	}
	if command != "install" && command != "start" {
		return true, errors.New("supported service actions are install and start")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return true, err
	}
	defer manager.Disconnect()
	if command == "start" {
		service, err := manager.OpenService(serviceName)
		if err != nil {
			return true, err
		}
		defer service.Close()
		return true, service.Start()
	}
	if configPath == "" {
		return true, errors.New("--config is required for service installation")
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return true, err
	}
	if _, err := loadConfig(absolute); err != nil {
		return true, err
	}
	executable, err := os.Executable()
	if err != nil {
		return true, err
	}
	if existing, err := manager.OpenService(serviceName); err == nil {
		existing.Close()
		return true, errors.New("AnyMangaBackend already exists; use the documented controlled service update")
	}
	service, err := manager.CreateService(serviceName, executable, mgr.Config{DisplayName: "AnyManga Backend", Description: "Go manga metadata API", StartType: mgr.StartAutomatic, DelayedAutoStart: true, ServiceStartName: `NT AUTHORITY\LocalService`}, "--config", absolute)
	if err != nil {
		return true, err
	}
	defer service.Close()
	actions := []mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 10 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}, {Type: mgr.ServiceRestart, Delay: 60 * time.Second}}
	if err := service.SetRecoveryActions(actions, 86400); err != nil {
		return true, err
	}
	return true, service.SetRecoveryActionsOnNonCrashFailures(true)
}

type windowsHandler struct{ server *http.Server }

func (h *windowsHandler) Execute(args []string, changes <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	finished := make(chan error, 1)
	go func() { finished <- h.server.ListenAndServe() }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-finished:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("service failed: %v", err)
				return true, 1
			}
			return false, 0
		case change := <-changes:
			switch change.Cmd {
			case svc.Interrogate:
				status <- change.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				shutdown(h.server)
				return false, 0
			}
		}
	}
}
func runPlatformService(server *http.Server) (bool, error) {
	inside, err := svc.IsWindowsService()
	if err != nil {
		return true, fmt.Errorf("service detection: %w", err)
	}
	if !inside {
		return false, nil
	}
	return true, svc.Run(serviceName, &windowsHandler{server})
}
