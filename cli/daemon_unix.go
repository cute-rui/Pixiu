//go:build !windows

package main

import (
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func daemonLikelyUp() bool {
	_, err := os.Stat(unixSocketPath())
	return err == nil
}

func dialDaemon() (*grpc.ClientConn, error) {
	return grpc.NewClient("unix://"+unixSocketPath(), grpc.WithTransportCredentials(insecure.NewCredentials()))
}
