// Command lgrok exposes a local port on a public URL via an lgrok server:
//
//	lgrok http 3000 --server https://tunnel.example.com --token SECRET
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 3 || os.Args[1] != "http" {
		fmt.Fprintln(os.Stderr, "usage: lgrok http <local-port> [--server URL] [--subdomain NAME] [--token TOKEN]")
		os.Exit(2)
	}
	port, err := strconv.Atoi(os.Args[2])
	if err != nil || port < 1 || port > 65535 {
		log.Fatalf("lgrok: invalid port %q", os.Args[2])
	}

	fs := flag.NewFlagSet("http", flag.ExitOnError)
	server := fs.String("server", envOr("LGROK_SERVER", "http://localhost:8080"), "lgrok server URL")
	token := fs.String("token", os.Getenv("LGROK_TOKEN"), "auth token")
	sub := fs.String("subdomain", "", "requested subdomain (default: random)")
	localHost := fs.String("local-host", "127.0.0.1", "local host to forward to")
	fs.Parse(os.Args[3:])
	localAddr := net.JoinHostPort(*localHost, strconv.Itoa(port))

	subdomain := *sub
	backoff := time.Second
	for {
		publicURL, session, fatal, err := connect(*server, *token, subdomain)
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
func connect(rawURL, token, sub string) (publicURL string, session *yamux.Session, fatal bool, err error) {
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
		// wrong token / bad subdomain won't fix themselves; a subdomain
		// conflict can be a stale session the server will reap, so retry it
		f := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest
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

func wsKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
