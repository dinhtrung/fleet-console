package act

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dinhtrung/fleet-console/internal/rig"
	"github.com/dinhtrung/fleet-console/internal/store"
)

// noteRefusal records a daemon-refused human raise on the audit ledger. A
// refused queue create used to be invisible: the raise answered its error and
// the skip line carried the code, but nothing durable said a human question
// had been tried and refused. The refusal is now one `queue.refused` event per
// (subject, code), actor `act`, detail `code=<code>; missing=<csv>` — the
// trailing `;` is the needle's terminator, so `code=human_route_fields_required;`
// can never match a longer code the way a bare prefix would.
//
// noteRefusal's own error is ignored by every caller: a ledger failure must
// not mask the refusal it was recording. The original error stays the raise's
// answer either way, so the skip line keeps carrying the code through
// RefusalError.Error().
func noteRefusal(ctx context.Context, st *store.Store, item int, err error) error {
	return noteRefusalSubject(ctx, st, fmt.Sprintf("#%d", item), err)
}

// noteRefusalSubject is noteRefusal with the subject spelled by the caller: a
// raise that is about a change request rather than an item keys its refusal on
// `cr-<n>`, so it never reads as a fact about an item with the same number.
func noteRefusalSubject(ctx context.Context, st *store.Store, subject string, err error) error {
	var refused *rig.RefusalError
	if st == nil || err == nil || !errors.As(err, &refused) {
		return nil
	}
	kind := "queue.refused"
	needle := fmt.Sprintf("code=%s;", refused.Code)
	exists, err := st.HasEvent(ctx, kind, subject, needle)
	if err != nil || exists {
		return err
	}
	detail := fmt.Sprintf("code=%s; missing=%s", refused.Code, strings.Join(refused.Missing, ","))
	_, werr := st.AppendEvent(ctx, store.Event{Actor: "act", Kind: kind, Subject: subject, Detail: detail})
	return werr
}
