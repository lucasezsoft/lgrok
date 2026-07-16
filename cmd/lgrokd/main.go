// Command lgrokd is the public tunnel server: clients register on
// /_lgrok/connect (websocket-style upgrade + yamux) and HTTP traffic to
// <sub>.<domain> is reverse-proxied into the matching tunnel.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

var subdomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type server struct {
	domain    string // public base domain as printed in URLs, may include port
	baseHost  string // hostname part of domain
	scheme    string
	token     string
	downloads string
	statePath string

	mu      sync.Mutex
	tunnels map[string]*tunnel
	claims  map[string]claim // subdomain -> password hash (persisted)
}

// claim locks a subdomain to whoever set its password first.
type claim struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

type tunnel struct {
	session *yamux.Session
	proxy   *httputil.ReverseProxy
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	domain := flag.String("domain", "localhost:8080", "base domain: tunnels at <sub>.<domain>, control/downloads at lgrok.<domain>")
	scheme := flag.String("scheme", "http", "scheme of public URLs (http|https)")
	token := flag.String("token", os.Getenv("LGROK_TOKEN"), "auth token required from clients (empty = no auth)")
	downloads := flag.String("downloads", "/srv/dist", "directory with lgrok CLI binaries served at /download/")
	statePath := flag.String("state", "/data/claims.json", "file persisting subdomain password claims")
	flag.Parse()

	s := &server{
		domain:    *domain,
		baseHost:  hostnameOnly(*domain),
		scheme:    *scheme,
		token:     *token,
		downloads: *downloads,
		statePath: *statePath,
		tunnels:   map[string]*tunnel{},
		claims:    map[string]claim{},
	}
	os.MkdirAll(filepath.Dir(s.statePath), 0o700)
	if b, err := os.ReadFile(s.statePath); err == nil {
		json.Unmarshal(b, &s.claims)
		log.Printf("loaded %d subdomain claims from %s", len(s.claims), s.statePath)
	}
	log.Printf("lgrokd listening on %s, tunnel URLs: %s://<sub>.%s", *addr, s.scheme, s.domain)
	srv := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := hostnameOnly(r.Host)
	if t := s.lookup(host); t != nil {
		t.proxy.ServeHTTP(w, r)
		return
	}
	if host != s.baseHost && host != "lgrok."+s.baseHost && strings.HasSuffix(host, "."+s.baseHost) {
		http.Error(w, "lgrok: tunnel not found for "+host, http.StatusNotFound)
		return
	}
	// lgrok.<domain> (fixed), the base domain itself, or localhost/IP: control plane.
	if strings.HasPrefix(r.URL.Path, "/download/") {
		// client installers get this server's address baked in
		if name := strings.TrimPrefix(r.URL.Path, "/download/"); name == "install-client.sh" || name == "install-client.ps1" {
			b, err := os.ReadFile(filepath.Join(s.downloads, name))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(strings.ReplaceAll(string(b), "__LGROK_SERVER__", s.scheme+"://lgrok."+s.domain)))
			return
		}
		http.StripPrefix("/download/", http.FileServer(http.Dir(s.downloads))).ServeHTTP(w, r)
		return
	}
	switch r.URL.Path {
	case "/_lgrok/connect":
		s.handleConnect(w, r)
	case "/_lgrok/ask": // Caddy on_demand_tls ask endpoint
		d := hostnameOnly(r.URL.Query().Get("domain"))
		if d == "lgrok."+s.baseHost || s.lookup(d) != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "unknown domain", http.StatusNotFound)
	default:
		base := s.scheme + "://lgrok." + s.domain
		fmt.Fprintf(w, `lgrok — servidor de túneis ativo (%d túneis abertos)

Instalar o CLI:

  macOS / Linux (terminal):
    curl -fsSL %s/download/install-client.sh | bash

  Windows (PowerShell):
    irm %s/download/install-client.ps1 | iex

Gerar seu link público (peça o token ao administrador):

  lgrok http 3000 --server %s --token <TOKEN>

Instalar seu próprio servidor (em uma VPS Ubuntu):
  curl -fsSL %s/download/install.sh | sudo bash
`, s.count(), base, base, base, base)
	}
}

func (s *server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.Header.Get("X-Lgrok-Token") != s.token {
		http.Error(w, "lgrok: invalid token", http.StatusUnauthorized)
		return
	}
	sub := r.Header.Get("X-Lgrok-Subdomain")
	requested := sub != ""
	if !requested {
		sub = randomSub()
	} else if sub == "lgrok" || !subdomainRe.MatchString(sub) {
		http.Error(w, "lgrok: invalid subdomain (use [a-z0-9-]; 'lgrok' is reserved)", http.StatusBadRequest)
		return
	}

	// Named subdomains are locked by password: the first client to bring a
	// password claims the name; afterwards only that password opens it.
	if requested {
		secret := r.Header.Get("X-Lgrok-Secret")
		s.mu.Lock()
		if c, claimed := s.claims[sub]; claimed {
			if subtle.ConstantTimeCompare([]byte(hashSecret(c.Salt, secret)), []byte(c.Hash)) != 1 {
				s.mu.Unlock()
				http.Error(w, "lgrok: subdomínio reservado por outro usuário — senha incorreta", http.StatusForbidden)
				return
			}
		} else if secret != "" {
			salt := randomSub()
			s.claims[sub] = claim{Salt: salt, Hash: hashSecret(salt, secret)}
			s.saveClaims()
			log.Printf("subdomain claimed: %s", sub)
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	if _, taken := s.tunnels[sub]; taken {
		s.mu.Unlock()
		http.Error(w, "lgrok: subdomain already in use: "+sub, http.StatusConflict)
		return
	}
	s.tunnels[sub] = nil // reserve before hijacking
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.tunnels, sub)
		s.mu.Unlock()
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		release()
		http.Error(w, "lgrok: hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		release()
		return
	}
	conn.SetDeadline(time.Time{})
	publicURL := s.scheme + "://" + sub + "." + s.domain
	fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nX-Lgrok-Url: %s\r\n\r\n",
		wsAccept(r.Header.Get("Sec-WebSocket-Key")), publicURL)
	if err := buf.Flush(); err != nil {
		release()
		conn.Close()
		return
	}

	session, err := yamux.Server(conn, nil)
	if err != nil {
		release()
		conn.Close()
		return
	}
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return session.Open()
		},
		MaxIdleConns:    16,
		IdleConnTimeout: 60 * time.Second,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = pr.In.Host
			pr.Out.Host = pr.In.Host
		},
		Transport:     transport,
		FlushInterval: -1, // flush streamed responses (SSE etc.) immediately
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "lgrok: local service unreachable", http.StatusBadGateway)
		},
	}
	s.mu.Lock()
	s.tunnels[sub] = &tunnel{session: session, proxy: proxy}
	s.mu.Unlock()
	log.Printf("tunnel open: %s -> client %s", publicURL, r.RemoteAddr)

	<-session.CloseChan()
	release()
	log.Printf("tunnel closed: %s", publicURL)
}

func (s *server) lookup(host string) *tunnel {
	if host == s.baseHost || !strings.HasSuffix(host, "."+s.baseHost) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tunnels[strings.TrimSuffix(host, "."+s.baseHost)]
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tunnels)
}

// saveClaims persists the claims map; callers must hold s.mu.
func (s *server) saveClaims() {
	b, _ := json.MarshalIndent(s.claims, "", "  ")
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("warn: cannot persist claims: %v", err)
		return
	}
	os.Rename(tmp, s.statePath)
}

func hashSecret(salt, secret string) string {
	h := sha256.Sum256([]byte(salt + ":" + secret))
	return hex.EncodeToString(h[:])
}

func hostnameOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(h)
}

func randomSub() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}
