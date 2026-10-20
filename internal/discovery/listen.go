// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package discovery

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/netip"
	"strconv"
	"strings"
)

// tcpListen is the state of a listening socket in /proc/net/tcp.
const tcpListen = "0A"

// Listening returns the TCP ports in the listening state, with the address
// each one is bound to, from /proc/net/tcp and /proc/net/tcp6 of the
// network namespace of the reader. Linking a socket to its process needs
// access to the file descriptors of other users, which the agent does not
// have, so ports are matched to processes by their options and defaults.
// ok is false when neither table can be read.
func Listening(fsys fs.FS) (map[int][]netip.Addr, bool) {
	out := map[int][]netip.Addr{}
	read := false
	for _, name := range []string{"proc/net/tcp", "proc/net/tcp6"} {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			continue
		}
		read = true
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Scan() // header
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 4 || f[3] != tcpListen {
				continue
			}
			addr, port, err := parseSocket(f[1])
			if err != nil {
				continue
			}
			out[port] = append(out[port], addr)
		}
	}
	return out, read
}

// parseSocket decodes "0100007F:1F90", the address in the byte order of
// the kernel, which stores each 32-bit word in host order (little endian
// on the architectures GreenLens supports).
func parseSocket(s string) (netip.Addr, int, error) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.Addr{}, 0, errors.New("no port")
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.Addr{}, 0, errors.New("bad address")
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	addr, _ := netip.AddrFromSlice(raw)
	return addr.Unmap(), int(port), nil
}

// endpoint returns the URL to reach a port bound to addr from this
// machine: a wildcard address is reached on loopback.
func endpoint(addr netip.Addr, port int) string {
	switch {
	case addr.IsUnspecified() && addr.Is4():
		addr = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	case addr.IsUnspecified():
		addr = netip.IPv6Loopback()
	}
	return "http://" + netip.AddrPortFrom(addr, uint16(port)).String() //nolint:gosec // ports come from a 16-bit field
}
