package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
)

type telegramStub struct {
	sends  []map[string]any
	edits  []map[string]any
	server *httptest.Server
}

func newTelegramStub(t *testing.T) *telegramStub {
	t.Helper()
	stub := &telegramStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			stub.sends = append(stub.sends, decoded)
		case strings.HasSuffix(r.URL.Path, "/editMessageText"):
			stub.edits = append(stub.edits, decoded)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func TestEscalationSendsOnceThenEdits(t *testing.T) {
	stub := newTelegramStub(t)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	sender := Telegram{Token: "token", ChatID: "chat", BaseURL: stub.server.URL, HTTP: stub.server.Client()}
	escalator := &Escalator{Store: st, Sender: sender, BoardURL: "https://board.example"}
	in := EscalationInput{
		DecisionID: "qitem-1", Project: "app", Item: 54,
		Question: "Accept the parked change request?", Deadline: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC),
	}
	first, err := escalator.Escalate(context.Background(), in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !first.Sent || first.MessageID != "42" || len(stub.sends) != 1 || len(stub.edits) != 0 {
		t.Fatalf("first = %+v sends=%d edits=%d", first, len(stub.sends), len(stub.edits))
	}
	text, _ := stub.sends[0]["text"].(string)
	for _, want := range []string{"app #54", "Accept the parked change request?", "https://board.example/decisions#qitem-1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("message = %q, missing %q", text, want)
		}
	}

	second, err := escalator.Escalate(context.Background(), in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Edited || second.MessageID != "42" || len(stub.sends) != 1 || len(stub.edits) != 1 {
		t.Fatalf("second = %+v sends=%d edits=%d", second, len(stub.sends), len(stub.edits))
	}
	if got := fmt.Sprint(stub.edits[0]["message_id"]); got != "42" {
		t.Fatalf("edited message id = %v", got)
	}
}
