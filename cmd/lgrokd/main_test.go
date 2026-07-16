package main

import (
	"net/http"
	"testing"
)

func newReq(remoteAddr, xff string) *http.Request {
	r := &http.Request{RemoteAddr: remoteAddr, Header: http.Header{}}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// The password/claim path is security-sensitive: verify hashing is
// deterministic, salted (so equal passwords don't collide across subdomains)
// and that a wrong password never matches.
func TestHashSecret(t *testing.T) {
	if hashSecret("salt", "pw") != hashSecret("salt", "pw") {
		t.Fatal("hash not deterministic")
	}
	if hashSecret("a", "pw") == hashSecret("b", "pw") {
		t.Fatal("salt ignored: same password hashes equal across salts")
	}
	if hashSecret("salt", "pw") == hashSecret("salt", "wrong") {
		t.Fatal("different passwords produced the same hash")
	}
}

func TestClientIP(t *testing.T) {
	r := newReq("10.0.0.9:5555", "203.0.113.7, 70.0.0.1")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("XFF first hop: got %q", got)
	}
	r = newReq("10.0.0.9:5555", "")
	if got := clientIP(r); got != "10.0.0.9" {
		t.Fatalf("RemoteAddr fallback: got %q", got)
	}
}

func TestSyncAdminPasswordRotates(t *testing.T) {
	s := &server{statePath: t.TempDir() + "/s.json"}
	s.loadState()
	s.syncAdminPassword("first")
	if !s.checkAdmin("first") || s.checkAdmin("second") {
		t.Fatal("initial password wrong")
	}
	s.syncAdminPassword("second") // env changed => rotate
	if s.checkAdmin("first") || !s.checkAdmin("second") {
		t.Fatal("rotation failed")
	}
}

func (s *server) checkAdmin(pw string) bool {
	return hashSecret(s.state.AdminSalt, pw) == s.state.AdminHash
}
