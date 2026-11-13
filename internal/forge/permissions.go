package forge

// FixPermission names the GitHub fine-grained permission that repairs one
// refused write operation (#329). A fine-grained token's permission set is per
// operation — a merge is filed under Contents, not Pull requests — so a fix
// that names the generic pair sends the operator to grant, wait, and hit the
// same 403 on the next real write. The preflight's fix text and the
// permission raise brief both read this one table, so the two cannot drift.
//
// The mapping follows the verbs this app actually writes (docs/forge-token.md,
// "What each write needs"): a comment is `gh issue comment` — issue comments,
// including on a pull request, are Issues writes — while `ready` and opening
// the change request are Pull requests writes, and the merge itself is the
// trap: Contents: write. An operation the table does not know returns ""; the
// caller keeps its generic sentence rather than guessing.
func FixPermission(operation string) string {
	switch operation {
	case "merge":
		return "Contents: write"
	case "ready", "create_pr":
		return "Pull requests: write"
	case "comment", "close", "label":
		return "Issues: write"
	}
	return ""
}
