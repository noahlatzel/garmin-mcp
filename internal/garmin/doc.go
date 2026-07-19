// Package garmin implements the Garmin Connect workout writer and wellness
// reader: the SSO web-widget auth flow (OAuth token acquire + refresh; see
// auth.go), the exercise catalog harvested from the live builder (catalog.go,
// data/), the pure workout-JSON builders enforcing the hard workout rules (one
// exercise per step; sets/repeats expanded into discrete steps; named exercise
// + reps + optional weight; never FIT files; workout.go, swim.go, target.go),
// token persistence (store.go), the connectapi client (api.go), and the MCP
// tool surface (tools.go, service.go).
//
// The one credentialed login (password + MFA) is human-gated, driven by
// `cmd/garmin login`; the running server only ever refreshes from the stored
// OAuth1 token.
package garmin
