// Package version holds the single source of truth for the build version,
// shared by the client (lgrok) and the server (lgrokd). Bump V before
// deploying a new server: clients on an older version are forced to update.
package version

const V = "1.0.0"
