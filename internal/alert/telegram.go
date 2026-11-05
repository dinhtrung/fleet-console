// Package alert delivers one Telegram message per decision and edits that message
// on later breaches; it never starts a second message for the same decision.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dinhtrung/fleet-console/internal/store"
)

// Sender is the Telegram surface. The fake in tests records sends and edits.
type Sender interface {
	Send(ctx context.Context, text string) (messageID string, err error)
	Edit(ctx context.Context, messageID, text string) error
}

// Telegram calls the Bot API. Token and ChatID come from the environment; the app
// never stores them in the repository or a log.
type Telegram struct {
	Token   string
	ChatID  string
	BaseURL string
	HTTP    *http.Client
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

// ClientTimeout bounds one Bot API call. Design §5: the transport is bounded, so
// a hung Telegram cannot hold a tick open.
const ClientTimeout = 15 * time.Second

// client is the HTTP client a call uses: the one the caller supplied, or the
// bounded default.
func (t Telegram) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return &http.Client{Timeout: ClientTimeout}
}

func (t Telegram) call(ctx context.Context, method string, body map[string]any) (string, error) {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	client := t.client()
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(base, "/"), t.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var decoded telegramResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("telegram %s: %w", method, err)
	}
	if !decoded.OK || resp.StatusCode >= 400 {
		return "", fmt.Errorf("telegram %s: %s", method, decoded.Description)
	}
	return fmt.Sprint(decoded.Result.MessageID), nil
}

// Send posts one message and returns its id.
func (t Telegram) Send(ctx context.Context, text string) (string, error) {
	return t.call(ctx, "sendMessage", map[string]any{
		"chat_id": t.ChatID, "text": text, "disable_web_page_preview": true,
	})
}

// Edit replaces the text of an existing message.
func (t Telegram) Edit(ctx context.Context, messageID, text string) error {
	_, err := t.call(ctx, "editMessageText", map[string]any{
		"chat_id": t.ChatID, "message_id": messageID, "text": text, "disable_web_page_preview": true,
	})
	return err
}

// EscalationInput is one decision due for an alert.
type EscalationInput struct {
	DecisionID string
	Project    string
	Item       int
	Question   string
	Deadline   time.Time
	// ItemURL links the item whose thread carries the note. Empty when the app
	// does not know the item's number, in which case the message omits the line
	// rather than linking a guess.
	ItemURL string
	// ReplyLine is the exact text the operator comments on the item to answer.
	// It is the same line the note prints, so copying the message works.
	ReplyLine string
}

// Escalation is what happened to the message.
type Escalation struct {
	DecisionID string
	MessageID  string
	Sent       bool // a new message was created
	Edited     bool // the existing message was edited
}

// Escalator sends once per decision and edits thereafter.
type Escalator struct {
	Store    *store.Store
	Sender   Sender
	BoardURL string
	Now      func() time.Time
}

// Text is the one message body: project, item, question, the reply line and the
// links. No inline keyboard: the app cannot receive a button press (design D9), so
// offering one would be a control that does nothing.
func (e *Escalator) Text(in EscalationInput) string {
	link := strings.TrimRight(e.BoardURL, "/") + "/decisions#" + in.DecisionID
	deadline := "no deadline"
	if !in.Deadline.IsZero() {
		deadline = in.Deadline.UTC().Format(time.RFC3339)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s #%d: %s\n", in.Project, in.Item, in.Question)
	fmt.Fprintf(&b, "Deadline: %s\n", deadline)
	if in.ReplyLine != "" {
		fmt.Fprintf(&b, "Reply on the item with: %s\n", in.ReplyLine)
	}
	if in.ItemURL != "" {
		fmt.Fprintf(&b, "Item: %s\n", in.ItemURL)
	}
	fmt.Fprintf(&b, "Board: %s", link)
	return b.String()
}

// Escalate sends the first time and edits afterwards. One decision, one message.
func (e *Escalator) Escalate(ctx context.Context, in EscalationInput) (*Escalation, error) {
	if e.Store == nil || e.Sender == nil {
		return nil, fmt.Errorf("alert: store and sender are required")
	}
	if in.DecisionID == "" {
		return nil, fmt.Errorf("alert: decision id is required")
	}
	text := e.Text(in)
	previous, err := e.Store.EventByKindAndSubject(ctx, "telegram.sent", in.DecisionID)
	if err != nil {
		return nil, err
	}
	if previous == nil {
		messageID, err := e.Sender.Send(ctx, text)
		if err != nil {
			return nil, err
		}
		if _, err := e.Store.AppendEvent(ctx, store.Event{
			Actor: "alert", Kind: "telegram.sent", Subject: in.DecisionID, Detail: messageID,
		}); err != nil {
			return nil, err
		}
		return &Escalation{DecisionID: in.DecisionID, MessageID: messageID, Sent: true}, nil
	}
	if err := e.Sender.Edit(ctx, previous.Detail, text); err != nil {
		return nil, err
	}
	if _, err := e.Store.AppendEvent(ctx, store.Event{
		Actor: "alert", Kind: "telegram.edited", Subject: in.DecisionID, Detail: previous.Detail,
	}); err != nil {
		return nil, err
	}
	return &Escalation{DecisionID: in.DecisionID, MessageID: previous.Detail, Edited: true}, nil
}
