// Package syncsvc owns the sequence cursor, the changes query and the
// WebSocket hub behind a Broadcaster interface (spec §6.3). Filled in at M5;
// nothing here yet.
//
// The package is named syncsvc rather than sync so that it — and its callers —
// can import the standard library's sync without an alias. The directory name
// stays as spec §5.3 specifies.
package syncsvc
