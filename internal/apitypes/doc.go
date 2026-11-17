// Package apitypes holds the JSON request and response bodies of the gateway's
// own /api HTTP contract.
//
// These are wire types: they describe bytes on the wire, not domain state. The
// package imports no other internal package, so any client (the desktop UI
// today, a web console later) can depend on it without pulling in the backend.
// internal/api converts domain values to and from these types at the handler
// boundary; domain packages never import apitypes.
package apitypes
