// Package ui holds the templ views for the BrambleGate dashboard: layout,
// pages, and small components. It has no business logic — handlers in
// internal/gui call into *gui.Service and pass the results here to render.
package ui

//go:generate go run ../../../cmd/gentailwind
//go:generate go tool templ generate ./...
