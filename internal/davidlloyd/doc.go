// Package davidlloyd reads body-composition measurements (notably body weight)
// from a David Lloyd gym account, for the headless scheduled sync in
// cmd/davidlloyd. It is a library, not an MCP server.
//
// Authentication is a one-time bootstrap: `davidlloyd login` reads the
// credentials transiently and persists ONLY the resulting session token, never
// the password, so a leaked store yields an expiring/revocable token. The sync
// uses the stored token and re-prompts for login if it expires.
//
// The package only reads. The weight it returns is written to Garmin Connect by
// cmd/davidlloyd (composing internal/garmin); neither the garmin nor intervals
// running service depends on this package.
package davidlloyd
