// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux

package ipmi

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Layouts of include/uapi/linux/ipmi.h on 64-bit Linux. Explicit padding
// keeps the C alignment of the pointer and long fields.
type ipmiMsg struct {
	netfn   uint8
	cmd     uint8
	dataLen uint16
	_       [4]byte
	data    *byte
}

type ipmiReq struct {
	addr    *byte
	addrLen uint32
	_       [4]byte
	msgid   int64
	msg     ipmiMsg
}

type ipmiRecv struct {
	recvType int32
	_        [4]byte
	addr     *byte
	addrLen  uint32
	_        [4]byte
	msgid    int64
	msg      ipmiMsg
}

// struct ipmi_system_interface_addr: int addr_type, short channel, unsigned
// char lun, padded to 8 bytes.
type systemInterfaceAddr struct {
	addrType int32
	channel  int16
	lun      uint8
	_        uint8
}

const (
	systemInterfaceAddrType = 0x0c
	bmcChannel              = 0x0f
	responseRecvType        = 1
	ioctlMagic              = 'i'
	responseTimeoutMS       = 2000
	// recvBufSize holds any DCMI response; longer ones are truncated.
	recvBufSize = 64
)

// ioc builds an ioctl request number with the asm-generic encoding used on
// amd64, arm64 and riscv64.
func ioc(dir, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | ioctlMagic<<8 | nr
}

var (
	ioctlSendCommand     = ioc(2, 13, unsafe.Sizeof(ipmiReq{}))  // _IOR('i', 13, struct ipmi_req)
	ioctlReceiveMsgTrunc = ioc(3, 11, unsafe.Sizeof(ipmiRecv{})) // _IOWR('i', 11, struct ipmi_recv)
)

// SystemOpener opens the IPMI character device.
type SystemOpener struct{}

type device struct {
	f     *os.File
	msgid int64
}

// Open implements Opener.
func (SystemOpener) Open(path string) (Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // fixed device paths under the host root
	if err != nil {
		return nil, err
	}
	return &device{f: f}, nil
}

func (d *device) Close() error { return d.f.Close() }

func (d *device) Request(netfn, cmd byte, data []byte) ([]byte, error) {
	d.msgid++
	addr := systemInterfaceAddr{addrType: systemInterfaceAddrType, channel: bmcChannel}
	payload := append([]byte(nil), data...)
	if len(payload) == 0 {
		payload = []byte{0}
	}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&addr)
	pin.Pin(&payload[0])
	req := ipmiReq{
		addr:    (*byte)(unsafe.Pointer(&addr)), //nolint:gosec // the kernel takes the address as a byte pointer
		addrLen: uint32(unsafe.Sizeof(addr)),
		msgid:   d.msgid,
		msg:     ipmiMsg{netfn: netfn, cmd: cmd, dataLen: uint16(len(data)), data: &payload[0]}, //nolint:gosec // requests are a few bytes
	}
	fd := d.f.Fd()
	if err := ioctl(fd, ioctlSendCommand, unsafe.Pointer(&req)); err != nil { //nolint:gosec // struct ipmi_req passed to the driver
		return nil, fmt.Errorf("send IPMI request: %w", err)
	}
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // file descriptors fit in int32
		n, err := unix.Poll(fds, responseTimeoutMS)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, errors.New("timeout waiting for the BMC")
		}
		var raddr systemInterfaceAddr
		buf := make([]byte, recvBufSize)
		pin.Pin(&raddr)
		pin.Pin(&buf[0])
		recv := ipmiRecv{
			addr:    (*byte)(unsafe.Pointer(&raddr)), //nolint:gosec // the kernel takes the address as a byte pointer
			addrLen: uint32(unsafe.Sizeof(raddr)),
			msg:     ipmiMsg{dataLen: recvBufSize, data: &buf[0]},
		}
		if err := ioctl(fd, ioctlReceiveMsgTrunc, unsafe.Pointer(&recv)); err != nil { //nolint:gosec // struct ipmi_recv passed to the driver
			return nil, fmt.Errorf("receive IPMI response: %w", err)
		}
		// Asynchronous events can arrive on the same device; only the
		// response to this request matters.
		if recv.recvType != responseRecvType || recv.msgid != d.msgid {
			continue
		}
		return buf[:min(int(recv.msg.dataLen), len(buf))], nil
	}
}

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
