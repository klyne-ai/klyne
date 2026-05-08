// Package klyne contains the embedded SvelteKit UI build that the
// daemon serves under "/".
//
// This file lives at the module root so that the //go:embed directive can
// reference the sibling "ui/build" tree directly. Go's embed cannot cross
// up to a parent directory, but a top-level file in its own package
// (package klyne) treats the module root as its package directory and
// can therefore embed any descendent.
//
// W13/W14 build the UI into ui/build/. The Makefile target `build-ui`
// guarantees that directory exists (creating an empty stub when the UI
// source tree is absent) so this file always compiles.
package klyne

import "embed"

// UI is the SvelteKit static build output (ui/build/) embedded into the
// binary. internal/app/staticfs.go strips the "ui/build/" prefix before
// serving requests over HTTP.
//
//go:embed all:ui/build
var UI embed.FS
