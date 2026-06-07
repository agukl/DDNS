package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if exists(nssmPath, name) {
		if err := updateServiceBinaryPath(name, nssmPath); err != nil {
			return err
		}
	} else {
		if err := runNSSM(nssmPath, "install", name, exePath, "service", "-config", configPath); err != nil {
			return err
		}
	}
	settings := [][]string{
		{"set", name, "Application", exePath},
		{"set", name, "AppParameters", "service -config " + configPath},
		{"set", name, "AppDirectory", appDir},
		{"set", name, "DisplayName", displayName},
		{"set", name, "Description", "Lightweight DDNS client"},
		{"set", name, "Start", "SERVICE_AUTO_START"},
		{"set", name, "AppExit", "Default", "Restart"},
		{"set", name, "AppRestartDelay", "5000"},
	}
	for _, args := range settings {
		if err := runNSSM(nssmPath, args...); err != nil {
			return err
		}
	}
	return nil
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
	return runNSSM(nssmPath, "start", name)
}

func Stop(name string) error {
	nssmPath, err := findNSSM()
	if err != nil {
		return err
	}
	return runNSSM(nssmPath, "stop", name)
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
