package tui

func networkBranch(last bool) (branch, indent string) {
	if last {
		return "└─ ", "   "
	}
	return "├─ ", "│  "
}

func networkProfileBranch(profile, summary string, last bool) (line, indent string) {
	branch, indent := networkBranch(last)
	return branch + accent.Render("Profile "+fleetText(unknown(profile))) + " · " + summary, indent
}

// Fit a contiguous range around the selected item. The first item pays for its
// ancestors even when the earlier part of its branch is outside the viewport.
func networkTreeWindow[T any](items []T, selected, budget, firstCost int, rowCost func(T, T) int) (start, end int) {
	if len(items) == 0 {
		return 0, 0
	}
	start = max(0, selected)
	end, used := start+1, firstCost
	for start > 0 {
		cost := rowCost(items[start-1], items[start])
		if used+cost > budget {
			break
		}
		start--
		used += cost
	}
	for end < len(items) {
		cost := rowCost(items[end-1], items[end])
		if used+cost > budget {
			break
		}
		end++
		used += cost
	}
	return start, end
}
