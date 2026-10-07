package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func configHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		t.Setenv("HOME", dir)
		dir = filepath.Join(dir, "Library", "Application Support")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	return dir
}

func TestApplyDaemonDownContinues(t *testing.T) {
	dir := configHome(t)
	prev := queryNetBird
	queryNetBird = func() (NetBirdInfo, error) { return NetBirdInfo{}, ErrDaemonDown }
	t.Cleanup(func() { queryNetBird = prev })

	var stdout, errOut bytes.Buffer
	if code := execute([]string{"apply"}, &stdout, &errOut); code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout.String(), errOut.String())
	}
	if errOut.String() != "netbird daemon is not running\n" {
		t.Fatalf("stderr = %q", errOut.String())
	}
	body, err := os.ReadFile(filepath.Join(dir, "pixiu", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("MATCH,DIRECT")) {
		t.Fatalf("runtime config:\n%s", body)
	}
}

func TestStartWarnsAndDoesNotStart(t *testing.T) {
	dir := configHome(t)
	if err := os.WriteFile(filepath.Join(dir, "pixiu.toml"), []byte("wifi_blacklist = [\"Cafe\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevScan := scanCurrent
	prevQuery := queryNetBird
	scanCurrent = func() Scan { return Scan{SSID: "Cafe", Medium: "wifi"} }
	queryNetBird = func() (NetBirdInfo, error) { return NetBirdInfo{}, ErrDaemonDown }
	t.Cleanup(func() {
		scanCurrent = prevScan
		queryNetBird = prevQuery
	})

	var stdout, errOut bytes.Buffer
	if code := execute([]string{"start"}, &stdout, &errOut); code != 0 {
		t.Fatalf("exit %d stderr %s", code, errOut.String())
	}
	const warning = "warning: Wi-Fi \"Cafe\" is on wifi_blacklist. Starting would connect both NetBird and Clash on that network.\n"
	if errOut.String() != warning {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "pixiu", "mihomo.pid")); !os.IsNotExist(err) {
		t.Fatal("mihomo was started")
	}
}
