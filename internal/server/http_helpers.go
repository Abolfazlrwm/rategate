// Package server
//
// http_helpers.go — small helpers for the REST handlers.
package server

import "strconv"

// strconvFormatInt is a thin alias over strconv.FormatInt so the http.go
// import list stays focused on domain types.
func strconvFormatInt(n int64) string {
	return strconv.FormatInt(n, 10)
}
