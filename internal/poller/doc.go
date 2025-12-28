// Package poller provides concurrent HTTP polling functionality for PulseBoard.
//
// This package is internal to PulseBoard and handles the periodic polling of
// HTTP endpoints. It implements a worker pool pattern for concurrent requests
// with configurable concurrency limits.
//
// The main components are:
//
//   - [Client]: HTTP client wrapper with timeout and size limits
//   - [Scheduler]: Manages periodic polling of endpoints with worker pool
//   - [StatusResult]: Result of polling a single endpoint
//   - [EndpointInfo]: Configuration for an endpoint to poll
//
// Users of the pulseboard library should not need to interact with this
// package directly. Configuration is done through the main pulseboard package.
package poller
