package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/netbirdio/netbird/client/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrDaemonDown is printed on stderr when apply or start cannot read the
// local NetBird daemon. The command still writes the runtime config.
var ErrDaemonDown = errors.New("netbird daemon is not running")

func queryDaemon() (NetBirdInfo, error) {
	if !daemonLikelyUp() {
		return NetBirdInfo{}, ErrDaemonDown
	}
	conn, err := dialDaemon()
	if err != nil {
		return NetBirdInfo{}, ErrDaemonDown
	}
	defer conn.Close()
	client := proto.NewDaemonServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg, err := client.GetConfig(ctx, &proto.GetConfigRequest{})
	if err != nil {
		return NetBirdInfo{}, ErrDaemonDown
	}
	st, err := client.Status(ctx, &proto.StatusRequest{GetFullPeerStatus: true})
	if err != nil && status.Code(err) != codes.OK {
		return NetBirdInfo{}, ErrDaemonDown
	}
	info := NetBirdInfo{Interface: cfg.GetInterfaceName()}
	if st != nil && st.GetFullStatus() != nil {
		full := st.GetFullStatus()
		for _, peer := range full.GetPeers() {
			info.Endpoints = append(info.Endpoints,
				peer.GetLocalIceCandidateEndpoint(),
				peer.GetRemoteIceCandidateEndpoint(),
				peer.GetRelayAddress(),
			)
		}
		for _, relay := range full.GetRelays() {
			info.Endpoints = append(info.Endpoints, relay.GetURI())
		}
	}
	return info, nil
}

func unixSocketPath() string {
	const fallback = "/var/run/netbird.sock"
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}
	entries, err := os.ReadDir("/var/run/netbird")
	if err != nil {
		return fallback
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sock") {
			return "/var/run/netbird/" + entry.Name()
		}
	}
	return fallback
}
