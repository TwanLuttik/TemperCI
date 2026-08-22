//go:build !linux

package ghacache

import (
	"fmt"
	"net"
)

func originalDest(c net.Conn) (string, error) {
	return "", fmt.Errorf("original dest not supported")
}
