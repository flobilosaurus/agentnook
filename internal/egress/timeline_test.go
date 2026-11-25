package egress

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTimelineMergesAndPagesWithoutPickingUpAppends(t *testing.T) {
	root := t.TempDir()
	makeArchive := func(name string, times ...int64) *historyWriter {
		t.Helper()
		w, err := createHistory(root, name, "ask", Info{Project: "/fixture", Profile: "work"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.file.Close() })
		for _, sec := range times {
			if err := w.append(HistoryEvent{Time: time.Unix(sec, 0), Decision: "denied"}); err != nil {
				t.Fatal(err)
			}
		}
		return w
	}
	a := makeArchive("nook-run-a", 1, 4, 7)
	b := makeArchive("nook-run-b", 2, 3, 5)
	makeArchive("nook-run-empty")
	read := func(q TimelineQuery, want ...int64) Timeline {
		t.Helper()
		out, err := ReadTimeline(context.Background(), root, q)
		if err != nil {
			t.Fatal(err)
		}
		var times []int64
		for _, row := range out.Records {
			times = append(times, row.Event.Time.Unix())
		}
		if !reflect.DeepEqual(times, want) {
			t.Fatalf("timeline %v, want %v", times, want)
		}
		return out
	}
	first := read(TimelineQuery{Limit: 1}, 7)
	if len(first.Machines) != 3 || len(first.Next) != 2 {
		t.Fatal(first)
	}
	for _, w := range []*historyWriter{a, b} {
		if err := w.append(HistoryEvent{Time: time.Unix(9, 0), Decision: "allowed"}); err != nil {
			t.Fatal(err)
		}
	}
	makeArchive("nook-run-new", 11)
	page := read(TimelineQuery{Limit: 2, Cursor: first.Next}, 5, 4)
	last := read(TimelineQuery{Limit: 20, Cursor: page.Next}, 3, 2, 1)
	if last.Next != nil {
		t.Fatal("non-empty cursor past the end", last.Next)
	}
	read(TimelineQuery{Running: map[string]bool{a.meta.Machine: true}}, 9, 7, 4, 1)
	read(TimelineQuery{Running: map[string]bool{}})
	read(TimelineQuery{Machine: b.meta.Machine, Before: first.Next[b.meta.Machine]}, 5, 3, 2)
	read(TimelineQuery{Limit: 3}, 11, 9, 9)
}

func TestTimelineKeepsAppendOrderAndWaitsForCompleteLine(t *testing.T) {
	root := t.TempDir()
	w, err := createHistory(root, "nook-run-a", "ask", Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.file.Close()
	for _, sec := range []int64{8, 1} {
		if err := w.append(HistoryEvent{Time: time.Unix(sec, 0), Decision: "allowed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.file.WriteString(`{"decision":"denied"}`); err != nil {
		t.Fatal(err)
	}
	page, err := ReadTimeline(context.Background(), root, TimelineQuery{Limit: 1})
	if err != nil || len(page.Records) != 1 || page.Records[0].Event.Sequence != 2 {
		t.Fatal(page, err)
	}
	page, err = ReadTimeline(context.Background(), root, TimelineQuery{Cursor: page.Next})
	if err != nil || len(page.Records) != 1 || page.Records[0].Event.Sequence != 1 || page.Next != nil {
		t.Fatal(page, err)
	}
}

func TestTimelineUsesArchivePermissionChecks(t *testing.T) {
	root := t.TempDir()
	w, err := createHistory(root, "nook-run-a", "ask", Info{})
	if err != nil {
		t.Fatal(err)
	}
	w.file.Close()
	if err := os.Chmod(filepath.Join(w.dir, "events.jsonl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTimeline(context.Background(), root, TimelineQuery{}); err == nil {
		t.Fatal("accepted a non-private history file")
	}
}
