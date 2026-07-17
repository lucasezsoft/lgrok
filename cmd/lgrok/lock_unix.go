//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockPort garante uma única instância do lgrok por porta nesta máquina/usuário.
// Usa flock: o SO libera sozinho quando o processo morre (sem lock preso).
func lockPort(port int) (func(), error) {
	p := filepath.Join(os.TempDir(), fmt.Sprintf("lgrok-%d-%d.lock", os.Geteuid(), port))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, nil // sem lock não é fatal
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("já existe um lgrok rodando na porta %d nesta máquina", port)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
