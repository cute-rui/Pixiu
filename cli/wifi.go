package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Scan is one look at the current network. A non-nil Err, an empty SSID,
// ethernet, or any medium other than wifi does not block.
type Scan struct {
	SSID   string
	Medium string
	Err    error
}

// Decision is what pixiu start should do with a blacklist.
type Decision struct {
	Block   bool
	Warning string
}

// Decide reports whether a connect may proceed.
//
// An empty list, a different SSID, ethernet, or a failed scan does not block.
// A listed SSID does not auto-connect. An explicit connect warns and, unless
// the user confirms, does not start. The default confirmation is no.
func Decide(scan Scan, blacklist []string, explicit, confirmYes bool) Decision {
	if scan.Err != nil || scan.SSID == "" || !strings.EqualFold(scan.Medium, "wifi") {
		return Decision{}
	}
	if !ssidListed(scan.SSID, blacklist) {
		return Decision{}
	}
	if !explicit {
		return Decision{Block: true}
	}
	warning := fmt.Sprintf("warning: Wi-Fi %q is on wifi_blacklist. Starting would connect both NetBird and Clash on that network.", scan.SSID)
	if !confirmYes {
		return Decision{Block: true, Warning: warning}
	}
	return Decision{Warning: warning}
}

func ssidListed(ssid string, blacklist []string) bool {
	for _, item := range blacklist {
		if item == ssid {
			return true
		}
	}
	return false
}

func scanOS() Scan {
	switch runtime.GOOS {
	case "linux":
		return scanLinux()
	case "darwin":
		return scanDarwin()
	case "windows":
		return scanWindows()
	default:
		return Scan{Medium: "unknown", Err: fmt.Errorf("wifi scan is not implemented on %s", runtime.GOOS)}
	}
}

func scanLinux() Scan {
	out, err := exec.Command("nmcli", "-t", "-f", "ACTIVE,SSID", "dev", "wifi").Output()
	if err == nil {
		ssid, ok := ssidFromNmcli(string(out))
		if !ok {
			return Scan{Medium: "ethernet"}
		}
		return Scan{SSID: ssid, Medium: "wifi"}
	}
	out, err = exec.Command("iwgetid", "-r").Output()
	if err != nil {
		return Scan{Medium: "unknown", Err: err}
	}
	ssid := strings.TrimSpace(string(out))
	if ssid == "" {
		return Scan{Medium: "ethernet"}
	}
	return Scan{SSID: ssid, Medium: "wifi"}
}

func ssidFromNmcli(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		active, ssid, ok := strings.Cut(line, ":")
		if ok && active == "yes" && ssid != "" {
			return ssid, true
		}
	}
	return "", false
}

func scanDarwin() Scan {
	out, err := exec.Command("networksetup", "-getairportnetwork", "en0").Output()
	if err != nil {
		return Scan{Medium: "unknown", Err: err}
	}
	ssid, ok := ssidFromAirport(string(out))
	if !ok {
		return Scan{Medium: "ethernet"}
	}
	return Scan{SSID: ssid, Medium: "wifi"}
}

func ssidFromAirport(out string) (string, bool) {
	const prefix = "Current Wi-Fi Network: "
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, prefix); ok && name != "" {
			return name, true
		}
	}
	return "", false
}

func scanWindows() Scan {
	out, err := exec.Command("netsh", "wlan", "show", "interfaces").Output()
	if err != nil {
		return Scan{Medium: "unknown", Err: err}
	}
	ssid, ok := ssidFromNetsh(string(out))
	if !ok {
		return Scan{Medium: "ethernet"}
	}
	return Scan{SSID: ssid, Medium: "wifi"}
}

func ssidFromNetsh(out string) (string, bool) {
	var ssid string
	connected := false
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if strings.EqualFold(key, "State") && strings.EqualFold(value, "connected") {
			connected = true
		}
		if strings.EqualFold(key, "SSID") && value != "" && !strings.EqualFold(key, "BSSID") {
			ssid = value
		}
	}
	if !connected || ssid == "" {
		return "", false
	}
	return ssid, true
}
