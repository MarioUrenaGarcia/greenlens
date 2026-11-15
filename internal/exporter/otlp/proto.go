// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package otlp

import (
	"encoding/binary"
	"math"
)

// The few pieces of the protobuf wire format that the metrics messages
// need: varints, 64-bit values and length-delimited fields.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
)

type buf struct{ b []byte }

func (w *buf) tag(field, wire int) {
	w.b = binary.AppendUvarint(w.b, uint64(field)<<3|uint64(wire)) //nolint:gosec // field numbers are small constants
}

func (w *buf) varint(field int, v uint64) {
	w.tag(field, wireVarint)
	w.b = binary.AppendUvarint(w.b, v)
}

func (w *buf) fixed64(field int, v uint64) {
	w.tag(field, wireFixed64)
	w.b = binary.LittleEndian.AppendUint64(w.b, v)
}

func (w *buf) double(field int, v float64) { w.fixed64(field, math.Float64bits(v)) }

func (w *buf) bytes(field int, v []byte) {
	w.tag(field, wireBytes)
	w.b = binary.AppendUvarint(w.b, uint64(len(v)))
	w.b = append(w.b, v...)
}

// str writes a string field; empty strings are the default and are left
// out.
func (w *buf) str(field int, v string) {
	if v != "" {
		w.bytes(field, []byte(v))
	}
}

// msg writes an embedded message built by fn.
func (w *buf) msg(field int, fn func(*buf)) {
	var inner buf
	fn(&inner)
	w.bytes(field, inner.b)
}

// packedFixed64 writes a repeated fixed64 field in its packed form.
func (w *buf) packedFixed64(field int, vs []uint64) {
	if len(vs) == 0 {
		return
	}
	p := make([]byte, 0, 8*len(vs))
	for _, v := range vs {
		p = binary.LittleEndian.AppendUint64(p, v)
	}
	w.bytes(field, p)
}

// packedDouble writes a repeated double field in its packed form.
func (w *buf) packedDouble(field int, vs []float64) {
	bits := make([]uint64, len(vs))
	for i, v := range vs {
		bits[i] = math.Float64bits(v)
	}
	w.packedFixed64(field, bits)
}
