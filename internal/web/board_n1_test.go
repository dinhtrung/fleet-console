package web

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
	"github.com/gofiber/fiber/v3"
	"modernc.org/sqlite"
)

var boardQueries int64

var registerCounting sync.Once

type countingDriver struct{ inner driver.Driver }

func (d countingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn}, nil
}

type countingConn struct{ driver.Conn }

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	atomic.AddInt64(&boardQueries, 1)
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("counting driver: connection has no QueryContext")
	}
	return queryer.QueryContext(ctx, query, args)
}

func countingStore(t *testing.T) *store.Store {
	t.Helper()
	registerCounting.Do(func() {
		sql.Register("sqlite-counting", countingDriver{inner: &sqlite.Driver{}})
	})
	st, err := store.OpenWithDriver(context.Background(), filepath.Join(t.TempDir(), "fleet.db"), "sqlite-counting")
	if err != nil {
		t.Fatalf("open counting store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedBoard(t *testing.T, st *store.Store, items int) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "rig", ConcurrencyCap: 2, Enabled: true,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	records := make([]store.ItemRecord, 0, items)
	for number := 1; number <= items; number++ {
		records = append(records, store.ItemRecord{
			Number: number, Kind: "task", State: "open", Title: fmt.Sprintf("Task %d", number),
			Labels: "cr:open", BlockedByJSON: "[]", URL: "u", UpdatedAt: now,
		})
	}
	if err := st.ReplaceItems(ctx, projectID, records); err != nil {
		t.Fatalf("items: %v", err)
	}
	for number := 1; number <= items; number++ {
		id, err := st.CreateDispatch(ctx, store.NewDispatch{
			ProjectID: projectID, ItemNumber: number, Attempt: 1, Seat: "seat", Branch: "b",
			Worktree: "w", BaseSHA: "x", CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("dispatch %d: %v", number, err)
		}
		if err := st.EndDispatch(ctx, id, "review"); err != nil {
			t.Fatalf("end dispatch %d: %v", number, err)
		}
	}
}

func boardQueryCount(t *testing.T, items int) int64 {
	t.Helper()
	st := countingStore(t)
	seedBoard(t, st, items)
	app, err := New(Config{Token: "test-token",  Store: st,  ConfigPath: "/tmp/fake-config.yaml"})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	atomic.StoreInt64(&boardQueries, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer test-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("drain: %v", err)
	}
	return atomic.LoadInt64(&boardQueries)
}

// TestBoardQueryCountIsBounded pins the N+1 fix: the read count is the same for a
// small and a 500-item backlog, and small in absolute terms.
func TestBoardQueryCountIsBounded(t *testing.T) {
	small := boardQueryCount(t, 5)
	large := boardQueryCount(t, 500)
	t.Logf("board queries: 5 items=%d, 500 items=%d", small, large)
	if large != small {
		t.Fatalf("queries grew with the backlog: 5 items=%d, 500 items=%d", small, large)
	}
	if large > 10 {
		t.Fatalf("board used %d queries, want <= 10", large)
	}
}
