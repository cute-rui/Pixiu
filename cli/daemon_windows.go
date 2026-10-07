//go:build windows

package main

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func daemonLikelyUp() bool {
	return true
}

func dialDaemon() (*grpc.ClientConn, error) {
	paths := []string{
		`\\.\pipe\ProtectedPrefix\Administrators\netbird`,
		`\\.\pipe\netbird`,
	}
	return grpc.NewClient("passthrough:///netbird-daemon-pipe",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var last error
			for _, path := range paths {
				dialCtx, cancel := context.WithTimeout(ctx, time.Second)
				conn, err := winio.DialPipeContext(dialCtx, path)
				cancel()
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		}),
	)
}
