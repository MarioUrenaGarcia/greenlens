// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux && (amd64 || arm64 || riscv64)

package ipmi

import (
	"testing"
	"unsafe"
)

// The kernel structures have these sizes on LP64 targets; a mismatch would
// make the ioctl numbers, which encode the size, wrong.
func TestStructLayout(t *testing.T) {
	if s := unsafe.Sizeof(ipmiMsg{}); s != 16 {
		t.Errorf("ipmi_msg is %d bytes, want 16", s)
	}
	if s := unsafe.Sizeof(ipmiReq{}); s != 40 {
		t.Errorf("ipmi_req is %d bytes, want 40", s)
	}
	if s := unsafe.Sizeof(ipmiRecv{}); s != 48 {
		t.Errorf("ipmi_recv is %d bytes, want 48", s)
	}
	if s := unsafe.Sizeof(systemInterfaceAddr{}); s != 8 {
		t.Errorf("ipmi_system_interface_addr is %d bytes, want 8", s)
	}
	if ioctlSendCommand != 0x8028690d || ioctlReceiveMsgTrunc != 0xc030690b {
		t.Errorf("ioctl numbers %#x %#x", ioctlSendCommand, ioctlReceiveMsgTrunc)
	}
}
