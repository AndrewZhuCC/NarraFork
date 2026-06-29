// Package id generates URL-safe nanoid-style identifiers matching NarraFork's
// nanoid(21) / nanoid(8) conventions.
package id

import (
	"crypto/rand"
)

const (
	// alphabet is URL-safe and exactly 64 characters. Because 64 is a power of
	// two and 256 % 64 == 0, masking a random byte with 0x3f (& 63) selects a
	// character with perfectly uniform probability and no modulo bias.
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-"
	// DefaultLength matches NarraFork's nanoid(21).
	DefaultLength = 21
	// ShortLength matches NarraFork's nanoid(8).
	ShortLength = 8
)

// New creates a 21-character nanoid.
func New() string {
	return nanoid(DefaultLength)
}

// NewShort creates an 8-character nanoid.
func NewShort() string {
	return nanoid(ShortLength)
}

// nanoid fills size bytes from crypto/rand in a single read and maps each byte
// onto the 64-char alphabet via a bitmask. A crypto/rand failure is
// unrecoverable, so we panic rather than silently emit a weak or empty ID.
func nanoid(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic("id: crypto/rand failed: " + err.Error())
	}
	id := make([]byte, size)
	for i := range buf {
		id[i] = alphabet[buf[i]&63]
	}
	return string(id)
}
