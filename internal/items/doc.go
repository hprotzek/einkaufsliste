// Package items owns entry CRUD and the dedupe rules (spec §6.6), including
// the invariant that every mutation writes its item_events row in the same
// transaction (spec §6.0). Filled in at M3; nothing here yet.
package items
