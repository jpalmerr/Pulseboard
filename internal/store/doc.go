// Package store provides storage and pub/sub functionality for status results.
//
// This package is internal to PulseBoard and manages the in-memory storage of
// endpoint status results. It implements a publish-subscribe pattern for
// real-time updates to connected dashboard clients.
//
// The main components are:
//
//   - [Store]: Interface defining storage and subscription operations
//   - [MemoryStore]: In-memory implementation of Store with pub/sub
//   - [StatusResult]: Storage representation of an endpoint's status
//
// The store is designed for concurrent access with proper synchronization.
// Subscribers receive updates via channels with non-blocking sends (slow
// subscribers will miss updates rather than block the system).
//
// Users of the pulseboard library should not need to interact with this
// package directly. Storage is managed internally by PulseBoard.
package store
