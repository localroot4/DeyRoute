//go:build !linux

package wireguard

import "errors"

// openGenetlink is only available on Linux.
func openGenetlink() (nlTransport, error) {
	return nil, errors.New("WireGuard generic netlink is only available on Linux")
}
