// Copyright (C) 2024 Akave
// See LICENSE for copying information.

// Package testrand provides utilities for generating random data in tests.
package testrand

import (
	"crypto/rand"
	rand2 "math/rand"
	"testing"
)

// Bytes returns a slice of random bytes of the given size.
func Bytes(t testing.TB, size int64) []byte {
	t.Helper()
	data := make([]byte, size)
	_, err := rand.Read(data)
	if err != nil {
		t.Fatalf("failed to read random data: %v", err)
	}
	return data
}

// BytesD returns a slice of random bytes of the given size with a deterministic seed.
func BytesD(t testing.TB, seed, size int64) []byte {
	t.Helper()
	data := make([]byte, size)
	random := rand2.New(rand2.NewSource(seed))
	_, err := random.Read(data)
	if err != nil {
		t.Fatalf("failed to read random data: %v", err)
	}
	return data
}
