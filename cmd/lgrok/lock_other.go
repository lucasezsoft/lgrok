//go:build !unix

package main

// Windows: sem checagem de instância única (best-effort). O lgrok roda igual.
func lockPort(port int) (func(), error) { return func() {}, nil }
