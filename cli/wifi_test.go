package main

import (
	"errors"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	listed := []string{"Cafe", "Office"}
	warning := `warning: Wi-Fi "Cafe" is on wifi_blacklist. Starting would connect both NetBird and Clash on that network.`

	cases := []struct {
		name     string
		scan     Scan
		list     []string
		explicit bool
		yes      bool
		block    bool
		warning  string
	}{
		{name: "empty list", scan: Scan{SSID: "Cafe", Medium: "wifi"}, list: nil},
		{name: "other ssid", scan: Scan{SSID: "Home", Medium: "wifi"}, list: listed},
		{name: "ethernet", scan: Scan{SSID: "Cafe", Medium: "ethernet"}, list: listed},
		{name: "failed scan", scan: Scan{SSID: "Cafe", Medium: "wifi", Err: errors.New("scan failed")}, list: listed},
		{name: "empty ssid", scan: Scan{Medium: "wifi"}, list: listed},
		{name: "auto blocks", scan: Scan{SSID: "Cafe", Medium: "wifi"}, list: listed, block: true},
		{name: "explicit default no", scan: Scan{SSID: "Cafe", Medium: "wifi"}, list: listed, explicit: true, block: true, warning: warning},
		{name: "explicit yes", scan: Scan{SSID: "Cafe", Medium: "wifi"}, list: listed, explicit: true, yes: true, warning: warning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.scan, tc.list, tc.explicit, tc.yes)
			if got.Block != tc.block || got.Warning != tc.warning {
				t.Fatalf("Decide() = %+v, want block=%v warning=%q", got, tc.block, tc.warning)
			}
		})
	}
}

func TestSSIDParsers(t *testing.T) {
	ssid, ok := ssidFromNmcli("no:Other\nyes:Cafe\n")
	if !ok || ssid != "Cafe" {
		t.Fatalf("nmcli: %q %v", ssid, ok)
	}
	if _, ok := ssidFromNmcli("no:Cafe\n"); ok {
		t.Fatal("inactive wifi should not block as wifi")
	}
	ssid, ok = ssidFromAirport("Current Wi-Fi Network: Cafe\n")
	if !ok || ssid != "Cafe" {
		t.Fatalf("airport: %q %v", ssid, ok)
	}
	if _, ok := ssidFromAirport("You are not associated with an AirPort network.\n"); ok {
		t.Fatal("airport ethernet")
	}
	ssid, ok = ssidFromNetsh("    State                  : connected\n    SSID                   : Cafe\n    BSSID                  : aa:bb\n")
	if !ok || ssid != "Cafe" {
		t.Fatalf("netsh: %q %v", ssid, ok)
	}
	if _, ok := ssidFromNetsh("    State                  : disconnected\n    SSID                   : Cafe\n"); ok {
		t.Fatal("netsh disconnected")
	}
	if strings.Contains(warningFor("Cafe"), "\n") {
		t.Fatal("warning is one line")
	}
}

func warningFor(ssid string) string {
	return Decide(Scan{SSID: ssid, Medium: "wifi"}, []string{ssid}, true, false).Warning
}
