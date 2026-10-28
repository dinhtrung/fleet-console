// Package store owns the SQLite database: the schema of design §6, its
// migrations, and the single-writer guard. It stores only facts the rig does
// not own; queue rows, decisions and leases stay in the rig.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

// busyTimeoutMS is how long a writer waits for the lock before failing.
const busyTimeoutMS = 5000

// Store is one SQLite database guarded by a single connection. WAL lets
// external readers in; the one connection is the writer story.
type Store struct {
	db   *sql.DB
	path string
}

// Open creates or reopens the database at path, applies pending migrations
// and returns it. The database is usable only through the returned Store.
func Open(ctx context.Context, path string) (*Store, error) {
	return open(ctx, path, "sqlite")
}

// OpenWithDriver opens the same store through another registered driver name.
// It exists so a test can wrap the driver and count queries; production uses Open.
func OpenWithDriver(ctx context.Context, path, driverName string) (*Store, error) {
	if driverName == "" {
		return nil, fmt.Errorf("store: empty driver name")
	}
	return open(ctx, path, driverName)
}

func open(ctx context.Context, path, driverName string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: empty database path")
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()

	db, err := sql.Open(driverName, u.String())
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One connection serializes writers and readers alike. That is the
	// deliberate trade in design §6: correctness over read concurrency.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the connection.
func (s *Store) Close() error { return s.db.Close() }

// Path is the filesystem path of the database.
func (s *Store) Path() string { return s.path }

// Event is one append-only audit row written by the app.
type Event struct {
	Actor      string
	Kind       string
	Subject    string
	Detail     string
	ExitCode   *int
	StdoutHash string
}

// AppendEvent records one event and returns its id.
func (s *Store) AppendEvent(ctx context.Context, e Event) (int64, error) {
	if e.Actor == "" || e.Kind == "" || e.Subject == "" {
		return 0, fmt.Errorf("store: event actor, kind and subject are required")
	}
	var exitCode any
	if e.ExitCode != nil {
		exitCode = *e.ExitCode
	}
	var stdoutHash any
	if e.StdoutHash != "" {
		stdoutHash = e.StdoutHash
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO event (at, actor, kind, subject, detail, exit_code, stdout_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), e.Actor, e.Kind, e.Subject, e.Detail, exitCode, stdoutHash)
	if err != nil {
		return 0, fmt.Errorf("store: append event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: append event: %w", err)
	}
	return id, nil
}
