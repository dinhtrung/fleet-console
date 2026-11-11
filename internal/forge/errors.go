package forge

import "errors"

// ErrPermission means the credential cannot perform this operation at all: the
// forge refused a write not because the forge hiccuped but because the token may
// not do it (#258). Retrying the same call cannot succeed, and the fix is a
// credential or a permission grant, so the operator must see this class named
// instead of an anonymous adapter error.
var ErrPermission = errors.New("forge: permission denied")

// PermissionError is one forge write the credential may not perform. Operation
// is the short name the write method declares (comment, ready, merge, close,
// label, create_pr); Forge is the adapter's kind ("github" / "gitlab"); Detail
// carries the adapter's refusal sentence byte-for-byte, because the operator
// alert quotes it verbatim.
type PermissionError struct {
	Operation string
	Forge     string
	Detail    string
}

func (e *PermissionError) Error() string {
	text := "forge: permission denied: " + e.Forge + " " + e.Operation
	if e.Detail != "" {
		text += ": " + e.Detail
	}
	return text
}

// Unwrap anchors the type on ErrPermission, so errors.Is(err, ErrPermission)
// works through any further wrapping.
func (e *PermissionError) Unwrap() error { return ErrPermission }

// IsPermission reports whether err is a forge write the credential may not
// perform — a 403 the token caused, a fine-grained token's 404 on a named
// repository, a GitLab scope refusal — as opposed to a transient condition
// (5xx, rate limit, timeout) a later call may survive.
func IsPermission(err error) bool { return errors.Is(err, ErrPermission) }
