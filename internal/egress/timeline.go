package egress

import (
	"container/heap"
	"context"
	"errors"
	"path/filepath"
)

// TimelineQuery is for the interactive view only. The JSON interface keeps
// using HistoryQuery and its single-machine byte cursor.
type TimelineQuery struct {
	Machine string
	Before  int64            // Initial --before for a single machine.
	Cursor  map[string]int64 // Remaining complete records by machine, nil for latest.
	Running map[string]bool  // Non-nil limits the view to confirmed running VMs.
	Limit   int
}

type HistoryRecord struct {
	Machine MachineHistory
	Event   HistoryEvent
}

type Timeline struct {
	Machines []MachineHistory
	Records  []HistoryRecord
	Next     map[string]int64 // Nil when no older records remain.
}

type timelineHead struct {
	record HistoryRecord
	before int64
}

type timelineHeads []timelineHead

func (h timelineHeads) Len() int { return len(h) }
func (h timelineHeads) Less(i, j int) bool {
	a, b := h[i].record, h[j].record
	if a.Event.Time.Equal(b.Event.Time) {
		return a.Machine.Machine < b.Machine.Machine
	}
	return a.Event.Time.After(b.Event.Time)
}
func (h timelineHeads) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *timelineHeads) Push(value any) { *h = append(*h, value.(timelineHead)) }
func (h *timelineHeads) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	(*h)[last] = timelineHead{}
	*h = (*h)[:last]
	return value
}

// ReadTimeline merges the heads of the per-VM logs, keeping append order per
// VM even if event timestamps go backward. It reads one bounded window per VM
// and at most Limit more windows, without keeping all event files open.
func ReadTimeline(ctx context.Context, root string, q TimelineQuery) (Timeline, error) {
	dir, machines, err := historyCatalog(root)
	result := Timeline{Machines: machines}
	if err != nil {
		return result, err
	}
	read := func(machine MachineHistory, before int64) (HistoryPage, int64, error) {
		if err := ctx.Err(); err != nil {
			return HistoryPage{}, 0, err
		}
		f, err := secureHistoryFile(filepath.Join(dir, machine.Machine, "events.jsonl"))
		if err != nil {
			return HistoryPage{}, 0, err
		}
		defer f.Close()
		return readHistoryWindow(f, HistoryQuery{Machine: machine.Machine, Before: before, Limit: 1})
	}
	var heads timelineHeads
	positions := make(map[string]int64)
	found := q.Machine == ""
	for _, machine := range machines {
		if q.Machine != "" && machine.Machine != q.Machine {
			continue
		}
		found = true
		if q.Running != nil && !q.Running[machine.Machine] {
			continue
		}
		before := q.Before
		if q.Cursor != nil {
			before = q.Cursor[machine.Machine]
			if before == 0 {
				continue
			}
		}
		page, top, err := read(machine, before)
		if err != nil {
			return result, err
		}
		if len(page.Events) > 0 {
			heads = append(heads, timelineHead{HistoryRecord{machine, page.Events[0]}, page.NextBefore})
			positions[machine.Machine] = top
		}
	}
	if !found {
		return result, errors.New("unknown archived machine")
	}
	heap.Init(&heads)
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	for heads.Len() > 0 && len(result.Records) < limit {
		head := heap.Pop(&heads).(timelineHead)
		result.Records = append(result.Records, head.record)
		machine := head.record.Machine
		if head.before == 0 {
			delete(positions, machine.Machine)
			continue
		}
		positions[machine.Machine] = head.before
		if len(result.Records) == limit {
			break
		}
		page, _, err := read(machine, head.before)
		if err != nil {
			return result, err
		}
		if len(page.Events) > 0 {
			heap.Push(&heads, timelineHead{HistoryRecord{machine, page.Events[0]}, page.NextBefore})
		} else {
			delete(positions, machine.Machine)
		}
	}
	if len(positions) != 0 {
		result.Next = positions
	}
	return result, nil
}
