package main

import (
	"strings"
	"testing"
)

func TestKeychainStoreArgs(t *testing.T) {
	got := keychainStoreArgs("svc", "user@example.com", "rt-secret")
	want := []string{"add-generic-password", "-U", "-s", "svc", "-a", "user@example.com", "-w", "rt-secret"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}
