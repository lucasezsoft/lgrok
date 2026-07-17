// Command lgrokd is the public tunnel server: clients register on
// /_lgrok/connect (websocket-style upgrade + yamux) and HTTP traffic to
// <sub>.<domain> is reverse-proxied into the matching tunnel. An admin
// dashboard lives at lgrok.<domain>/admin (HTTP Basic Auth).
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
	"html"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"
	"lgrok/internal/version"
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
	state   state // persisted to statePath
}

// state is everything that must survive restarts, saved as one JSON file.
type state struct {
	AdminSalt string           `json:"admin_salt,omitempty"`
	AdminHash string           `json:"admin_hash,omitempty"`
	Claims    map[string]claim `json:"claims"`  // subdomain -> password lock
	Blocked   map[string]bool  `json:"blocked"` // blocked origin IPs
}

// claim locks a subdomain to whoever set its password first.
type claim struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

type tunnel struct {
	session  *yamux.Session
	proxy    *httputil.ReverseProxy
	clientIP string
	since    time.Time
	reqs     atomic.Int64
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	domain := flag.String("domain", "localhost:8080", "base domain: tunnels at <sub>.<domain>, control/downloads at lgrok.<domain>")
	scheme := flag.String("scheme", "http", "scheme of public URLs (http|https)")
	token := flag.String("token", os.Getenv("LGROK_TOKEN"), "auth token required from clients (empty = no auth)")
	downloads := flag.String("downloads", "/srv/dist", "directory with lgrok CLI binaries served at /download/")
	statePath := flag.String("state", "/data/state.json", "JSON file persisting claims, blocks and admin password")
	flag.Parse()

	s := &server{
		domain:    *domain,
		baseHost:  hostnameOnly(*domain),
		scheme:    *scheme,
		token:     *token,
		downloads: *downloads,
		statePath: *statePath,
		tunnels:   map[string]*tunnel{},
	}
	s.loadState()
	s.syncAdminPassword(os.Getenv("LGROK_ADMIN_PASS"))

	log.Printf("lgrokd %s listening on %s, tunnel URLs: %s://<sub>.%s", version.V, *addr, s.scheme, s.domain)
	srv := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := hostnameOnly(r.Host)
	if t := s.lookup(host); t != nil {
		t.reqs.Add(1)
		t.proxy.ServeHTTP(w, r)
		return
	}
	if host != s.baseHost && host != "lgrok."+s.baseHost && strings.HasSuffix(host, "."+s.baseHost) {
		http.Error(w, "lgrok: tunnel not found for "+host, http.StatusNotFound)
		return
	}
	// lgrok.<domain> (fixed), the base domain itself, or localhost/IP: control plane.
	if strings.HasPrefix(r.URL.Path, "/admin") {
		s.handleAdmin(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/download/") {
		s.serveDownload(w, r)
		return
	}
	switch r.URL.Path {
	case "/health":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":%q,"tunnels":%d}`+"\n", version.V, s.count())
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

Gerar seu link público:

  lgrok http 3000
`, s.count(), base, base)
	}
}

func (s *server) serveDownload(w http.ResponseWriter, r *http.Request) {
	// client installers get this server's address (and token) baked in
	if name := strings.TrimPrefix(r.URL.Path, "/download/"); name == "install-client.sh" || name == "install-client.ps1" {
		b, err := os.ReadFile(filepath.Join(s.downloads, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		out := strings.ReplaceAll(string(b), "__LGROK_SERVER__", s.scheme+"://lgrok."+s.domain)
		out = strings.ReplaceAll(out, "__LGROK_TOKEN__", s.token)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(out))
		return
	}
	http.StripPrefix("/download/", http.FileServer(http.Dir(s.downloads))).ServeHTTP(w, r)
}

func (s *server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.Header.Get("X-Lgrok-Token") != s.token {
		http.Error(w, "lgrok: invalid token", http.StatusUnauthorized)
		return
	}
	ip := clientIP(r)
	s.mu.Lock()
	blocked := s.state.Blocked[ip]
	s.mu.Unlock()
	if blocked {
		http.Error(w, "lgrok: seu acesso foi bloqueado pelo administrador", http.StatusForbidden)
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
		if c, claimed := s.state.Claims[sub]; claimed {
			if subtle.ConstantTimeCompare([]byte(hashSecret(c.Salt, secret)), []byte(c.Hash)) != 1 {
				s.mu.Unlock()
				http.Error(w, "lgrok: subdomínio reservado por outro usuário — senha incorreta", http.StatusForbidden)
				return
			}
		} else if secret != "" {
			salt := randomSub()
			s.state.Claims[sub] = claim{Salt: salt, Hash: hashSecret(salt, secret)}
			s.saveState()
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
	s.tunnels[sub] = &tunnel{session: session, proxy: proxy, clientIP: ip, since: time.Now()}
	s.mu.Unlock()
	log.Printf("tunnel open: %s -> client %s", publicURL, ip)

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

// --- persistence -----------------------------------------------------------

func (s *server) loadState() {
	s.state = state{Claims: map[string]claim{}, Blocked: map[string]bool{}}
	os.MkdirAll(filepath.Dir(s.statePath), 0o700)
	if b, err := os.ReadFile(s.statePath); err == nil {
		json.Unmarshal(b, &s.state)
		if s.state.Claims == nil {
			s.state.Claims = map[string]claim{}
		}
		if s.state.Blocked == nil {
			s.state.Blocked = map[string]bool{}
		}
		log.Printf("state loaded: %d claims, %d blocked IPs", len(s.state.Claims), len(s.state.Blocked))
	}
}

// saveState persists s.state; callers must hold s.mu.
func (s *server) saveState() {
	b, _ := json.MarshalIndent(s.state, "", "  ")
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("warn: cannot persist state: %v", err)
		return
	}
	os.Rename(tmp, s.statePath)
}

// syncAdminPassword makes the env var the source of truth: if set and it
// differs from what's stored, (re)hash and persist it. Rotating the admin
// password is just editing .env and restarting.
func (s *server) syncAdminPassword(pass string) {
	if pass == "" {
		if s.state.AdminHash == "" {
			log.Printf("warn: no admin password set (LGROK_ADMIN_PASS) — /admin disabled")
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.AdminSalt != "" && subtle.ConstantTimeCompare(
		[]byte(hashSecret(s.state.AdminSalt, pass)), []byte(s.state.AdminHash)) == 1 {
		return // unchanged
	}
	s.state.AdminSalt = randomSub()
	s.state.AdminHash = hashSecret(s.state.AdminSalt, pass)
	s.saveState()
	log.Printf("admin password set/updated")
}

// --- admin dashboard -------------------------------------------------------

func (s *server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	configured := s.state.AdminHash != ""
	s.mu.Unlock()
	if !configured {
		http.Error(w, "lgrok: painel admin não configurado (defina LGROK_ADMIN_PASS)", http.StatusServiceUnavailable)
		return
	}
	if !s.adminAuthed(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="lgrok admin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.URL.Path {
	case "/admin", "/admin/":
		s.renderAdmin(w)
	case "/admin/block":
		s.blockIP(r.FormValue("ip"))
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	case "/admin/unblock":
		s.mu.Lock()
		delete(s.state.Blocked, r.FormValue("ip"))
		s.saveState()
		s.mu.Unlock()
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	case "/admin/delete":
		s.deleteSubdomain(r.FormValue("sub"))
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) adminAuthed(r *http.Request) bool {
	_, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	s.mu.Lock()
	salt, hash := s.state.AdminSalt, s.state.AdminHash
	s.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(hashSecret(salt, pass)), []byte(hash)) == 1
}

// blockIP adds an IP to the blocklist and tears down its live tunnels.
func (s *server) blockIP(ip string) {
	if ip == "" {
		return
	}
	s.mu.Lock()
	s.state.Blocked[ip] = true
	s.saveState()
	var kill []*yamux.Session
	for _, t := range s.tunnels {
		if t != nil && t.clientIP == ip {
			kill = append(kill, t.session)
		}
	}
	s.mu.Unlock()
	for _, sess := range kill { // close outside the lock: release() also locks
		sess.Close()
	}
	log.Printf("admin blocked IP %s (%d tunnels closed)", ip, len(kill))
}

// deleteSubdomain frees a reserved name and closes its live tunnel.
func (s *server) deleteSubdomain(sub string) {
	if sub == "" {
		return
	}
	s.mu.Lock()
	delete(s.state.Claims, sub)
	s.saveState()
	var sess *yamux.Session
	if t := s.tunnels[sub]; t != nil {
		sess = t.session
	}
	s.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
	log.Printf("admin deleted subdomain %s", sub)
}

type adminRow struct {
	Sub, URL, IP, Age string
	Reqs              int64
}

func (s *server) renderAdmin(w http.ResponseWriter) {
	s.mu.Lock()
	var rows []adminRow
	for sub, t := range s.tunnels {
		if t == nil {
			continue
		}
		rows = append(rows, adminRow{
			Sub:  sub,
			URL:  s.scheme + "://" + sub + "." + s.domain,
			IP:   t.clientIP,
			Age:  time.Since(t.since).Truncate(time.Second).String(),
			Reqs: t.reqs.Load(),
		})
	}
	claimed := len(s.state.Claims)
	var blocked []string
	for ip := range s.state.Blocked {
		blocked = append(blocked, ip)
	}
	s.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Reqs > rows[j].Reqs })
	sort.Strings(blocked)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, adminHead, len(rows), claimed, len(blocked))
	if len(rows) == 0 {
		fmt.Fprint(w, `<tr><td colspan="6" class="empty">nenhum túnel ativo</td></tr>`)
	}
	for _, r := range rows {
		fmt.Fprintf(w, `<tr>
<td><a href="%s" target="_blank">%s</a></td>
<td>%s</td><td class="num">%d</td><td>%s</td>
<td><form method="post" action="/admin/block" onsubmit="return confirm('Bloquear o IP %s? Todos os túneis dele caem.')"><input type="hidden" name="ip" value="%s"><button class="danger">bloquear IP</button></form></td>
<td><form method="post" action="/admin/delete" onsubmit="return confirm('Deletar o subdomínio %s e liberar o nome?')"><input type="hidden" name="sub" value="%s"><button class="danger">deletar subdomínio</button></form></td>
</tr>`,
			html.EscapeString(r.URL), html.EscapeString(r.Sub), html.EscapeString(r.IP), r.Reqs, html.EscapeString(r.Age),
			html.EscapeString(r.IP), html.EscapeString(r.IP),
			html.EscapeString(r.Sub), html.EscapeString(r.Sub))
	}
	fmt.Fprint(w, `</tbody></table>`)

	fmt.Fprint(w, `<h2>IPs bloqueados</h2>`)
	if len(blocked) == 0 {
		fmt.Fprint(w, `<p class="empty">nenhum</p>`)
	} else {
		fmt.Fprint(w, `<table><tbody>`)
		for _, ip := range blocked {
			fmt.Fprintf(w, `<tr><td>%s</td><td><form method="post" action="/admin/unblock"><input type="hidden" name="ip" value="%s"><button>desbloquear</button></form></td></tr>`,
				html.EscapeString(ip), html.EscapeString(ip))
		}
		fmt.Fprint(w, `</tbody></table>`)
	}
	fmt.Fprint(w, `</main></body></html>`)
}

const adminHead = `<!doctype html><html lang="pt-br"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>lgrok · admin</title>
<style>
:root{color-scheme:light dark}
body{font:15px/1.5 system-ui,sans-serif;margin:0;background:#0f1115;color:#e6e6e6}
main{max-width:960px;margin:0 auto;padding:24px}
h1{font-size:20px;margin:0 0 4px} h2{font-size:16px;margin:32px 0 8px;color:#9aa}
.sub{color:#8a93a2;margin:0 0 20px}
.stats{display:flex;gap:12px;margin:16px 0}
.stat{background:#1a1e26;border:1px solid #262b36;border-radius:10px;padding:12px 16px;flex:1}
.stat b{display:block;font-size:22px} .stat span{color:#8a93a2;font-size:12px}
table{width:100%%;border-collapse:collapse;background:#1a1e26;border:1px solid #262b36;border-radius:10px;overflow:hidden}
th,td{text-align:left;padding:10px 12px;border-bottom:1px solid #262b36;font-size:13px}
th{color:#8a93a2;font-weight:600} tr:last-child td{border-bottom:0}
td.num{font-variant-numeric:tabular-nums;font-weight:700} td.empty,.empty{color:#8a93a2;text-align:center}
a{color:#6ea8fe;text-decoration:none} a:hover{text-decoration:underline}
button{font:inherit;font-size:12px;padding:4px 10px;border-radius:6px;border:1px solid #333a47;background:#232833;color:#e6e6e6;cursor:pointer}
button:hover{background:#2c323f} button.danger{border-color:#5a2a2a;color:#ff9b9b}
button.danger:hover{background:#3a1f1f} form{margin:0}
</style></head><body><main>
<h1>lgrok · painel do administrador</h1>
<p class="sub">Atualize a página para ver os números mais recentes.</p>
<div class="stats">
<div class="stat"><b>%d</b><span>túneis ativos</span></div>
<div class="stat"><b>%d</b><span>subdomínios reservados</span></div>
<div class="stat"><b>%d</b><span>IPs bloqueados</span></div>
</div>
<h2>Túneis ativos</h2>
<table><thead><tr><th>subdomínio</th><th>IP de origem</th><th>requisições</th><th>ativo há</th><th></th><th></th></tr></thead><tbody>`

// --- helpers ---------------------------------------------------------------

// clientIP returns the real origin IP, honoring X-Forwarded-For set by Caddy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
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
