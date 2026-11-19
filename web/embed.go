// Package web holds the browser client, embedded into the bridge binary.
// Run `make web` (tsc) before building so dist/ exists.
package web

import "embed"

//go:embed index.html stream.html dist
var Files embed.FS
