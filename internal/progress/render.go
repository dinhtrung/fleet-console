package progress

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxBody is the hard cap: Telegram allows 4096 characters and the header
	// must survive. Truncation never touches the project block, the rig header
	// or its why: line.
	maxBody = 3900
	// entryLimit is how many entries one bucket prints before +N more.
	entryLimit = 5
)

// Render formats the report. It never reads the clock (now is an argument) and
// never fails, which is what makes it a golden file.
func Render(snapshot Snapshot, now time.Time) string {
	seatLimit := len(snapshot.Rig.Seats)
	if body := render(snapshot, now, entryLimit, true, seatLimit); utf8.RuneCountInString(body) <= maxBody {
		return body
	}
	for limit := entryLimit - 1; limit >= 0; limit-- {
		body := render(snapshot, now, limit, true, seatLimit)
		if utf8.RuneCountInString(body) <= maxBody {
			return body
		}
	}
	for limit := seatLimit - 1; limit >= 0; limit-- {
		body := render(snapshot, now, 0, false, limit)
		if utf8.RuneCountInString(body) <= maxBody {
			return body
		}
	}
	return render(snapshot, now, 0, false, 0)
}

func render(snapshot Snapshot, now time.Time, limit int, detail bool, seatLimit int) string {
	var out strings.Builder
	out.WriteString(renderProject(snapshot.Project))
	out.WriteString("\n")
	out.WriteString(renderRig(snapshot.Rig, now, seatLimit))
	out.WriteString("\n")
	out.WriteString(renderBuckets(snapshot, now, limit, detail))
	return out.String()
}

func renderProject(project ProjectBlock) string {
	var out strings.Builder
	out.WriteString("PROJECT\n")
	out.WriteString(pair("key", project.Key, "name", project.Name))
	out.WriteString(pair("forge", project.Forge, "repo", project.Repo))
	out.WriteString("  url: " + project.RepoURL + "\n")
	out.WriteString(pair("branch", project.DefaultBranch, "cap", fmt.Sprintf("%d", project.Cap)))
	out.WriteString(pair("onhold", yesNo(project.OnHold), "act", onOff(project.Act)))
	out.WriteString("  paths: main " + project.MainPath + "\n")
	out.WriteString("         worktrees " + project.WorktreePath + "\n")
	return out.String()
}

// pair writes two fields on one line with the second value column fixed, so
// the block reads as a table rather than as prose.
func pair(firstLabel, firstValue, secondLabel, secondValue string) string {
	line := "  " + firstLabel + ": " + firstValue
	const column = 28
	if len(line) < column {
		line += strings.Repeat(" ", column-len(line))
	} else {
		line += " "
	}
	return line + secondLabel + ": " + secondValue + "\n"
}

func renderRig(block RigBlock, now time.Time, seatLimit int) string {
	names := strings.Join(block.Names, ", ")
	if names == "" {
		names = "(none)"
	}
	var out strings.Builder
	switch {
	case block.RosterErr != "":
		fmt.Fprintf(&out, "RIG %s · seats: roster unavailable: %s\n", names, block.RosterErr)
	case block.Idle:
		fmt.Fprintf(&out, "RIG %s · idle\n", names)
	default:
		fmt.Fprintf(&out, "RIG %s · busy %d/%d\n", names, block.Busy, block.Total)
	}
	if block.RosterErr == "" {
		for index, seat := range block.Seats {
			if index >= seatLimit {
				break
			}
			out.WriteString(renderSeat(seat, now))
		}
	}
	if block.Idle && block.IdleWhy != "" {
		out.WriteString("  why: " + block.IdleWhy + "\n")
	}
	return out.String()
}

func renderSeat(seat SeatLine, now time.Time) string {
	name := shortSession(seat.Session)
	if name == "" {
		name = seat.LogicalID
	}
	if seat.IdleWhy != "" {
		return "  ⏸ " + name + "  " + seat.IdleWhy + "\n"
	}
	line := "  ▶ " + name
	if seat.Item > 0 {
		line += fmt.Sprintf("  #%d %s", seat.Item, shortTitle(seat.Title, 38))
	}
	if age := seatAge(seat, now); age != "" {
		line += "  " + age
	}
	return line + "\n"
}

func renderBuckets(snapshot Snapshot, now time.Time, limit int, detail bool) string {
	var out strings.Builder
	inFlight := trueCount(snapshot.Counts.InFlight, len(snapshot.InFlight))
	pending := trueCount(snapshot.Counts.Pending, len(snapshot.Pending))
	onHold := trueCount(snapshot.Counts.OnHold, len(snapshot.OnHold))
	epics := trueCount(snapshot.Counts.Epics, len(snapshot.Epics))

	fmt.Fprintf(&out, "▶ in progress (%d)\n", inFlight)
	for index, item := range snapshot.InFlight {
		if index >= limit {
			break
		}
		out.WriteString(fmt.Sprintf("  #%d %s\n", item.Number, shortTitle(item.Title, 120)))
		if detail {
			out.WriteString(itemDetail(item, now))
		}
	}
	if inFlight > limit {
		fmt.Fprintf(&out, "  +%d more\n", inFlight-limit)
	}

	fmt.Fprintf(&out, "⏳ pending (%d)\n", pending)
	out.WriteString(bucketLines(snapshot.Pending, limit))

	fmt.Fprintf(&out, "⏸ on hold (%d)\n", onHold)
	out.WriteString(bucketLines(snapshot.OnHold, limit))

	for index, epic := range snapshot.Epics {
		if index >= limit {
			break
		}
		fmt.Fprintf(&out, "epic: #%d %s — %d of %d done\n", epic.Number, shortTitle(epic.Title, 120), epic.Done, epic.Total)
	}
	if epics > limit {
		fmt.Fprintf(&out, "  +%d more\n", epics-limit)
	}
	return out.String()
}

func bucketLines(items []Item, limit int) string {
	var out strings.Builder
	for index, item := range items {
		if index >= limit {
			break
		}
		fmt.Fprintf(&out, "  #%d %s\n", item.Number, shortTitle(item.Title, 120))
	}
	if len(items) > limit {
		fmt.Fprintf(&out, "  +%d more\n", len(items)-limit)
	}
	return out.String()
}

func itemDetail(item Item, now time.Time) string {
	var parts []string
	if item.Seat != "" {
		parts = append(parts, shortSession(item.Seat))
	}
	if item.Branch != "" {
		parts = append(parts, item.Branch)
	}
	if age := itemAge(item, now); age != "" {
		parts = append(parts, age)
	}
	if len(parts) == 0 {
		return ""
	}
	return "      " + strings.Join(parts, " · ") + "\n"
}

func seatAge(seat SeatLine, now time.Time) string {
	if !seat.Since.IsZero() {
		return ageString(now.Sub(seat.Since))
	}
	return seat.Age
}

func itemAge(item Item, now time.Time) string {
	if !item.Since.IsZero() {
		return ageString(now.Sub(item.Since))
	}
	return item.Age
}

func trueCount(count, list int) int {
	if count > 0 {
		return count
	}
	return list
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// onOff is how the act switch prints; the design's block reads `act: on`.
func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

// shortTitle keeps one bucket line readable; truncation only affects a title,
// never the project or rig block.
func shortTitle(title string, limit int) string {
	title = strings.TrimSpace(title)
	runes := []rune(title)
	if len(runes) <= limit {
		return title
	}
	return string(runes[:limit]) + "…"
}
