package id

import (
	"crypto/rand"
	"fmt"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz-"

func Generate(size int) (string, error) {
	if size <= 0 {
		size = 21
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	out := make([]byte, size)
	for i, b := range buf {
		out[i] = alphabet[int(b)&63]
	}
	return string(out), nil
}

func Must(size int) string {
	v, err := Generate(size)
	if err != nil {
		panic(err)
	}
	return v
}

func New() string {
	return Must(21)
}

func Short() string {
	return Must(8)
}
