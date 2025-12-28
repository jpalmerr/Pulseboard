// Package dashboard provides the embedded web UI assets for PulseBoard.
//
// This package uses Go's embed directive to include the dashboard HTML, CSS,
// and JavaScript at compile time. This enables single-binary deployment
// without external asset files.
//
// The embedded assets are served by the server package at the root path ("/").
// Users of the pulseboard library should not need to interact with this
// package directly.
package dashboard

import "embed"

// Assets is an embedded filesystem containing the dashboard web UI.
//
// The filesystem structure is:
//
//	assets/
//	  index.html    - Main dashboard page with inline CSS and JavaScript
//
// Assets is used by the server package to serve the dashboard. The embed
// directive includes all files in the assets directory at compile time.
//
//go:embed assets/*
var Assets embed.FS
