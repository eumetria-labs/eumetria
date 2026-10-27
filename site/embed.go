// Package site holds the page's static files, compiled into eumetria so
// the server binary is self-contained.
//
// Only the files and folders named below are embedded. Everything a sync
// generates (stats.json, photos/, routes/, days/) may lie next to them in a
// local build (the smoke test) and must never end up in the binary; embed_test.go
// checks that, and that no page file was forgotten.
package site

import "embed"

//go:embed index.html style.css sw.js manifest.webmanifest robots.txt
//go:embed js fonts icons vendor
var Files embed.FS
