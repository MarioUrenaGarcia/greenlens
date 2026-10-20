// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build !linux || !(amd64 || arm64)

package nvmlhelper

import "github.com/MarioUrenaGarcia/greenlens/internal/nvmlproto"

// Open reports that NVML is not available: NVIDIA publishes it for Linux on
// x86-64 and Arm64 only.
func Open() (Library, error) {
	return nil, &nvmlproto.Error{Call: "dlopen libnvidia-ml.so.1", Return: nvmlproto.ErrLibraryMissing}
}
