package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

const DefaultName = "DDNS"

func Install(name, displayName, configPath string) error {
	nssmPath, err := findNSSM()
	if err != nil {
		return err
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return err
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if displayName == "" {
		displayName = name
	}

	appDir := filepath.Dir(exePath)
	baseDir := filepath.Dir(appDir)
	stdoutPath := filepath.Join(baseDir, "output", "logs", "service-stdout.log")
	stderrPath := filepath.Join(baseDir, "output", "logs", "service-stderr.log")
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0755); err != nil {
		return err
	}
	if exists(nssmPath, name) {
		if err := updateServiceBinaryPath(name, nssmPath); err != nil {
			return err
		}
	} else {
		if err := runNSSM(nssmPath, "install", name, exePath, "run", "-config", configPath); err != nil {
			return err
		}
	}
	settings := [][]string{
		{"set", name, "Application", exePath},
		{"set", name, "AppParameters", "run -config " + quoteArg(configPath)},
		{"set", name, "AppDirectory", appDir},
		{"set", name, "DisplayName", displayName},
		{"set", name, "Description", "Lightweight DDNS client"},
		{"set", name, "Start", "SERVICE_AUTO_START"},
		{"set", name, "AppExit", "Default", "Restart"},
		{"set", name, "AppRestartDelay", "5000"},
		{"set", name, "AppThrottle", "1500"},
		{"set", name, "AppStdout", stdoutPath},
		{"set", name, "AppStderr", stderrPath},
		{"set", name, "AppRotateFiles", "1"},
		{"set", name, "AppRotateOnline", "1"},
		{"set", name, "AppRotateSeconds", "86400"},
		{"set", name, "AppRotateBytes", "1048576"},
	}
	for _, args := range settings {
		if err := runNSSM(nssmPath, args...); err != nil {
			return err
		}
	}
	return nil
}

func quoteArg(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func Uninstall(name string) error {
	nssmPath, err := findNSSM()
	if err != nil {
		return err
	}
	if !serviceExists(name) {
		return nil
	}
	_ = runNSSM(nssmPath, "stop", name)
	if err := runNSSM(nssmPath, "remove", name, "confirm"); err != nil && serviceExists(name) {
		if scErr := deleteWithSC(name); scErr != nil && serviceExists(name) {
			return fmt.Errorf("%w; sc delete fallback failed: %v", err, scErr)
		}
	}
	return nil
}

func Start(name string) error {
	nssmPath, err := findNSSM()
	if err != nil {
		return err
	}
	status, err := nssmStatus(nssmPath, name)
	if err == nil && status == "SERVICE_RUNNING" {
		return nil
	}
	if err == nil && status == "SERVICE_PAUSED" {
		return stopThenStart(nssmPath, name)
	}
	if err := runNSSM(nssmPath, "start", name); err != nil {
		status, statusErr := nssmStatus(nssmPath, name)
		if statusErr == nil && status == "SERVICE_PAUSED" {
			return stopThenStart(nssmPath, name)
		}
		return err
	}
	return nil
}

func Stop(name string) error {
	nssmPath, err := findNSSM()
	if err != nil {
		return err
	}
	if !serviceExists(name) {
		return nil
	}
	status, err := nssmStatus(nssmPath, name)
	if err == nil && status == "SERVICE_STOPPED" {
		return nil
	}
	return runNSSM(nssmPath, "stop", name)
}

func Restart(name string) error {
	nssmPath, err := findNSSM()
	if err != nil {
		return err
	}
	status, err := nssmStatus(nssmPath, name)
	if err == nil && status == "SERVICE_STOPPED" {
		return runNSSM(nssmPath, "start", name)
	}
	return stopThenStart(nssmPath, name)
}

func findNSSM() (string, error) {
	candidates := []string{}
	if exePath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exePath), "nssm.exe"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "nssm.exe"))
	}
	candidates = append(candidates, "nssm.exe")

	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("nssm.exe not found; put nssm.exe next to ddns.exe or in the project directory")
}

func runNSSM(nssmPath string, args ...string) error {
	cmd := exec.Command(nssmPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nssm %v failed: %w: %s", args, err, decodeOutput(output))
	}
	return nil
}

func stopThenStart(nssmPath, name string) error {
	if err := runNSSM(nssmPath, "stop", name); err != nil {
		if scErr := stopWithSC(name); scErr != nil {
			return fmt.Errorf("%w; sc stop fallback failed: %v", err, scErr)
		}
	}
	if err := waitForStatus(nssmPath, name, "SERVICE_STOPPED", 20*time.Second); err != nil {
		return err
	}
	return runNSSM(nssmPath, "start", name)
}

func updateServiceBinaryPath(name, nssmPath string) error {
	cmd := exec.Command("sc.exe", "config", name, "binPath=", nssmPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc config %s binPath failed: %w: %s", name, err, decodeOutput(output))
	}
	return nil
}

func exists(nssmPath, name string) bool {
	cmd := exec.Command(nssmPath, "status", name)
	return cmd.Run() == nil
}

func nssmStatus(nssmPath, name string) (string, error) {
	cmd := exec.Command(nssmPath, "status", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nssm status %s failed: %w: %s", name, err, decodeOutput(output))
	}
	return strings.TrimSpace(decodeOutput(output)), nil
}

func waitForStatus(nssmPath, name, expected string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastStatus string
	var lastErr error
	for {
		status, err := nssmStatus(nssmPath, name)
		if err == nil && status == expected {
			return nil
		}
		lastStatus = status
		lastErr = err
		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("service %s did not reach %s, current status %s", name, expected, lastStatus)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func serviceExists(name string) bool {
	cmd := exec.Command("sc.exe", "query", name)
	return cmd.Run() == nil
}

func deleteWithSC(name string) error {
	cmd := exec.Command("sc.exe", "delete", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc delete %s failed: %w: %s", name, err, decodeOutput(output))
	}
	return nil
}

func stopWithSC(name string) error {
	cmd := exec.Command("sc.exe", "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc stop %s failed: %w: %s", name, err, decodeOutput(output))
	}
	return nil
}

func decodeOutput(output []byte) string {
	if len(output) == 0 {
		return ""
	}
	if bytes.Count(output, []byte{0}) < len(output)/4 {
		return strings.TrimSpace(string(output))
	}

	if len(output)%2 != 0 {
		output = output[:len(output)-1]
	}
	u16 := make([]uint16, 0, len(output)/2)
	for i := 0; i < len(output); i += 2 {
		u16 = append(u16, binary.LittleEndian.Uint16(output[i:i+2]))
	}
	return strings.TrimSpace(string(utf16.Decode(u16)))
}
