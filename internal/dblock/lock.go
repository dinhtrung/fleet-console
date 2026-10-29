// Package dblock enforces D1: exactly one process may tick a database.
//
// The lock is an exclusive flock on "<database>.lock". flock dies with the
// process, so a killed daemon leaves a lock file behind but never a held lock:
// startup opens and locks the file, and a stale file does not block.
package dblock

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
)

// ErrHeld matches Acquire's "another instance owns the database" error.
var ErrHeld = errors.New("database lock is held")

// HeldError names the database another process is ticking.
type HeldError struct {
	Database string
}

func (e *HeldError) Error() string { return "another instance owns " + e.Database }

// Unwrap lets errors.Is(err, ErrHeld) identify the contention.
func (e *HeldError) Unwrap() error { return ErrHeld }

// Lock is one held flock. Release is idempotent.
type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire takes an exclusive, non-blocking flock on database+".lock". A held
// lock returns *HeldError immediately; it never waits and has no timeout.
func Acquire(database string) (*Lock, error) {
	if database == "" {
		return nil, errors.New("dblock: database path is empty")
	}
	path := database + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("dblock: %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &HeldError{Database: database}
		}
		return nil, fmt.Errorf("dblock: lock %s: %w", path, err)
	}
	return &Lock{file: file}, nil
}

// Release unlocks and closes the file. Closing alone would also release the
// flock; the explicit unlock makes the intent visible.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.file == nil {
			return
		}
		unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		closeErr := l.file.Close()
		l.file = nil
		l.err = errors.Join(unlockErr, closeErr)
	})
	return l.err
}
