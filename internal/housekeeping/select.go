// Package housekeeping reclaims the worktrees a project no longer needs.
//
// The unit of work is one candidate: a worktree the project owner has finished
// with, judged by four facts (an open change request, a live dispatch row, a
// live queue row, a dirty tree). It is written as keep-conditions rather than
// remove-conditions because each new reason to keep is additive, and a worktree
// that cannot be reconstructed from the forge must never be reclaimed.
//
// Nothing here kills a process or vacuums a journal: those live in the
// operator's script, which this package deliberately does not replace.
package housekeeping

// Candidate is one worktree and the four facts that decide its fate. Every fact
// is collected by the caller from the tick's own read and the worktree's own
// git state, so Select stays pure and a table test can own the whole matrix.
type Candidate struct {
	Item   int    // the item number the worktree name encodes
	Path   string // absolute path to the worktree directory
	Branch string // the branch it has checked out; empty when unknown
	// Dirty is `git status --porcelain` non-empty: uncommitted work that cannot
	// be reconstructed, so it is never reclaimed.
	Dirty bool
	// HasOpenCR is an open change request whose head branch is Branch.
	HasOpenCR bool
	// LiveDispatch is a dispatch row for Item that has not ended.
	LiveDispatch bool
	// LiveRow is a non-terminal queue row that names Item.
	LiveRow bool
}

// Reason is why a candidate was kept or skipped. Every non-removal decision
// carries one, from the fixed vocabulary the change set names.
type Reason string

const (
	ReasonOpenCR       Reason = "open-cr"
	ReasonLiveDispatch Reason = "live-dispatch"
	ReasonLiveRow      Reason = "live-row"
	ReasonDirty        Reason = "dirty"
	ReasonForgeStale   Reason = "forge-stale"
	ReasonSymlink      Reason = "symlink"
	ReasonOutsideRoot  Reason = "outside-root"
	// ReasonGitRefused is a removal git itself declined. It is a keep carrying
	// git's own words, which is more useful than a generic failure.
	ReasonGitRefused Reason = "git-refused"
)

// Decision is one candidate's fate. Remove is true only when every keep
// condition was false. Reason is set on a keep or a skip, and Detail carries
// git's own message for a refused removal or the path for a skip.
type Decision struct {
	Candidate Candidate
	Remove    bool
	Reason    Reason
	Detail    string
}

// Select decides each candidate from the four facts, with no I/O and no clock.
// The order is deliberate: an open change request is the costliest fact to get
// wrong, so it is named first when several hold.
func Select(candidates []Candidate) []Decision {
	decisions := make([]Decision, 0, len(candidates))
	for _, candidate := range candidates {
		if reason, detail := keepReason(candidate); reason != "" {
			decisions = append(decisions, Decision{Candidate: candidate, Reason: reason, Detail: detail})
			continue
		}
		decisions = append(decisions, Decision{Candidate: candidate, Remove: true})
	}
	return decisions
}

// keepReason names the first condition that protects a candidate, or "" when it
// is a removal candidate.
func keepReason(candidate Candidate) (Reason, string) {
	switch {
	case candidate.HasOpenCR:
		return ReasonOpenCR, candidate.Branch
	case candidate.LiveDispatch:
		return ReasonLiveDispatch, ""
	case candidate.LiveRow:
		return ReasonLiveRow, ""
	case candidate.Dirty:
		return ReasonDirty, ""
	}
	return "", ""
}
