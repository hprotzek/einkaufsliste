// Package auth verifies OIDC identities and issues and rotates our own tokens
// (spec §9). Filled in at M1; nothing here yet.
//
// Google is the only provider in R1, but no code in this package may assume
// provider == "google".
package auth
