// Package server provides the HTTP server for the PulseBoard dashboard and API.
//
// This package is internal to PulseBoard and handles all HTTP concerns:
//
//   - Dashboard serving: Serves the embedded HTML/CSS/JS dashboard at "/"
//   - REST API: JSON endpoint at "/api/status" for current status snapshot
//   - Server-Sent Events: Real-time updates at "/api/sse"
//
// The server supports graceful shutdown via context cancellation, with a
// 5-second timeout for in-flight requests.
//
// Users of the pulseboard library should not need to interact with this
// package directly. The server is started automatically by [pulseboard.PulseBoard.Start].
package server
