package forge

import "strings"

// SameSHA says whether two spellings of a commit id name the same commit: the
// same string, or one an unambiguous prefix of the other, compared
// case-insensitively. Seven is git's own minimum abbreviation, and a prefix of
// the full sha cannot realistically be another commit's abbreviation. Anything
// shorter is not an abbreviation — it is a typo.
//
// This is the repo's one sha matcher (epic #316): a reviewer writes
// `head cca2508c` while the record carries the full 40-char sha, and every
// comparison between the two spellings goes through here, so no package keeps
// a private copy that can drift.
func SameSHA(note, head string) bool {
	note, head = strings.ToLower(note), strings.ToLower(head)
	if len(note) < 7 || len(head) < 7 {
		return false
	}
	return strings.HasPrefix(head, note) || strings.HasPrefix(note, head)
}
