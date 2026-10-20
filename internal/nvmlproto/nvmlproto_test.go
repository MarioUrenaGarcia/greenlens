// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package nvmlproto

import "testing"

func TestPCIAddress(t *testing.T) {
	for in, want := range map[string]string{
		"00000000:01:00.0":     "0000:01:00.0",
		"0000:3B:00.0":         "0000:3b:00.0",
		"00000001:D8:1f.7\x00": "0001:d8:1f.7",
	} {
		if got, err := PCIAddress(in); err != nil || got != want {
			t.Errorf("PCIAddress(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "01:00.0", "0000:01:00", "0000:1ff:00.0", "0000:01:20.0", "0000:01:00.8", "zz:01:00.0"} {
		if got, err := PCIAddress(in); err == nil {
			t.Errorf("PCIAddress(%q) = %q, want an error", in, got)
		}
	}
}

func TestErrorText(t *testing.T) {
	e := &Error{Call: "nvmlInit_v2", Return: ErrDriverNotLoad, Text: "Driver Not Loaded"}
	if got := e.Error(); got != "nvmlInit_v2: Driver Not Loaded (9)" {
		t.Errorf("%q", got)
	}
	if got := (&Error{Call: "dlopen", Return: ErrLibraryMissing}).Error(); got != "dlopen returned 12" {
		t.Errorf("%q", got)
	}
}
