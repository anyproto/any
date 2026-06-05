// Package tests holds integration tests for bobrik-watch's program storage.
//
// The actual tests live in files tagged `//go:build integration` because they
// require a running `any` server (they create an "integration_test" space and
// exercise it over HTTP). This untagged file exists so the package is always
// resolvable by `go list` / tooling even when the integration tag is off.
//
// Run the suite:
//
//	go test -tags integration ./cmd/bobrik-watch/tests/ -v
//
// See README.md for the full story and the companion engine probe.
package tests
