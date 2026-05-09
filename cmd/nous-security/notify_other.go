//go:build !darwin

package main

// Stubs for non-darwin builds. The menubar agent only ships as a
// macOS .app bundle; these exist so `go build ./...` on Linux/CI
// still type-checks without pulling in cgo Objective-C.

func hasBundle() bool { return false }

func requestNotificationAuth() {}

func postNativeNotification(title, body string) {}
