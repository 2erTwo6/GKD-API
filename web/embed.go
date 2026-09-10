package web

import "embed"

// Dist holds the built frontend (web/dist), embedded at compile time.
// Run `make build-web` before `go build`.
//
//go:embed all:dist
var Dist embed.FS
