// Command lgrok exposes a local port on a public URL via an lgrok server:
//
//	lgrok http 3000
//
// On the first run it asks for the company token, the desired subdomain and a
// password that locks the subdomain to this user; everything is saved to
// ~/.lgrok.json so the next runs need no flags at all.
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/term"
)

type config struct {
	Server    string `json:"server,omitempty"`
	Token     string `json:"token,omitempty"`
	Subdomain string `json:"subdomain,omitempty"`
	Secret    string `json:"secret,omitempty"`
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 3 || os.Args[1] != "http" {
		fmt.Fprintln(os.Stderr, "usage: lgrok http <local-port> [--server URL] [--subdomain NAME] [--token TOKEN] [--secret SENHA]")
		os.Exit(2)
	}
	port, err := strconv.Atoi(os.Args[2])
	if err != nil || port < 1 || port > 65535 {
		log.Fatalf("lgrok: invalid port %q", os.Args[2])
	}

	fs := flag.NewFlagSet("http", flag.ExitOnError)
	serverFlag := fs.String("server", "", "lgrok server URL")
	tokenFlag := fs.String("token", "", "company auth token")
	subFlag := fs.String("subdomain", "", "subdomain (default: saved config or random)")
	secretFlag := fs.String("secret", "", "subdomain password")
	localHost := fs.String("local-host", "127.0.0.1", "local host to forward to")
	fs.Parse(os.Args[3:])
	localAddr := net.JoinHostPort(*localHost, strconv.Itoa(port))

	// precedence: flag > environment > saved config > default
	cfg := loadConfig()
	server := pick(*serverFlag, os.Getenv("LGROK_SERVER"), cfg.Server, "http://localhost:8080")
	token := pick(*tokenFlag, os.Getenv("LGROK_TOKEN"), cfg.Token)
	subdomain := pick(*subFlag, cfg.Subdomain)
	secret := pick(*secretFlag, os.Getenv("LGROK_SECRET"), cfg.Secret)

	// first-run questionnaire (interactive terminals only)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if token == "" {
			token = promptSecret("Token da empresa (peça ao administrador)")
		}
		if subdomain == "" {
			subdomain = promptLine(fmt.Sprintf("Subdomínio desejado (ex.: meuapp.%s — vazio = aleatório)", baseDomain(server)))
		}
		if secret == "" && subdomain != "" {
			secret = promptSecret("Senha do subdomínio (criada na 1ª vez, exigida nas seguintes)")
		}
	}

	saved := false
	backoff := time.Second
	for {
		publicURL, session, fatal, err := connect(server, token, subdomain, secret)
		if err != nil {
			if fatal {
				log.Fatalf("lgrok: %v", err)
			}
			log.Printf("lgrok: connect failed: %v (retrying in %s)", err, backoff)
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if u, err := url.Parse(publicURL); err == nil {
			// pin the assigned subdomain so reconnects keep the same URL
			subdomain = strings.SplitN(u.Hostname(), ".", 2)[0]
		}
		if !saved {
			saved = true
			nc := config{Server: server, Token: token, Subdomain: subdomain, Secret: secret}
			if nc != cfg {
				if err := saveConfig(nc); err == nil {
					log.Printf("lgrok: configuração salva em %s — da próxima vez rode só: lgrok http %d", configPath(), port)
				}
			}
		}
		log.Printf("lgrok: forwarding %s -> %s", publicURL, localAddr)
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				break
			}
			go proxy(stream, localAddr)
		}
		log.Printf("lgrok: connection lost, reconnecting...")
	}
}

// connect dials the server, performs the upgrade handshake and returns the
// public URL plus the yamux session. fatal=true means retrying won't help.
func connect(rawURL, token, sub, secret string) (publicURL string, session *yamux.Session, fatal bool, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, true, err
	}
	addr := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}
	var conn net.Conn
	if u.Scheme == "https" {
		conn, err = tls.Dial("tcp", addr, nil)
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return "", nil, false, err
	}

	req, err := http.NewRequest("GET", u.Scheme+"://"+u.Host+"/_lgrok/connect", nil)
	if err != nil {
		conn.Close()
		return "", nil, true, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", wsKey())
	if token != "" {
		req.Header.Set("X-Lgrok-Token", token)
	}
	if sub != "" {
		req.Header.Set("X-Lgrok-Subdomain", sub)
	}
	if secret != "" {
		req.Header.Set("X-Lgrok-Secret", secret)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return "", nil, false, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return "", nil, false, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		conn.Close()
		// wrong token/password or bad subdomain won't fix themselves; a
		// subdomain conflict can be a stale session the server will reap
		f := resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusBadRequest
		return "", nil, f, fmt.Errorf("server refused: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	session, err = yamux.Client(&bufConn{conn, br}, nil)
	if err != nil {
		conn.Close()
		return "", nil, false, err
	}
	return resp.Header.Get("X-Lgrok-Url"), session, false, nil
}

// proxy pipes one tunneled stream into the local service.
func proxy(stream net.Conn, localAddr string) {
	local, err := net.Dial("tcp", localAddr)
	if err != nil {
		stream.Close()
		return
	}
	go func() {
		io.Copy(stream, local)
		stream.Close()
	}()
	io.Copy(local, stream)
	local.Close()
}

// bufConn replays bytes the handshake reader may have buffered past the 101.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func configPath() string {
	if p := os.Getenv("LGROK_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".lgrok.json"
	}
	return filepath.Join(home, ".lgrok.json")
}

func loadConfig() config {
	var c config
	if b, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func saveConfig(c config) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(configPath(), append(b, '\n'), 0o600)
}

// pick returns the first non-empty value.
func pick(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// baseDomain extracts the mother domain from the server URL for the example
// shown in the questionnaire (lgrok.suaempresa.com -> suaempresa.com).
func baseDomain(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		return strings.TrimPrefix(u.Hostname(), "lgrok.")
	}
	return "suaempresa.com"
}

var stdin = bufio.NewReader(os.Stdin)

func promptLine(q string) string {
	fmt.Fprintf(os.Stderr, "%s: ", q)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptSecret(q string) string {
	fmt.Fprintf(os.Stderr, "%s: ", q)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func wsKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}
