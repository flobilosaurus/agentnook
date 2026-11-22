package fleet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceAllProjectsKeepsDataHomeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, agent, project, home, extra string
		want                              int
	}{
		{name: "current", agent: "pi", want: 1},
		{name: "child", agent: "opencode", project: "/child", want: 1},
		{name: "other", agent: "claude", project: "/other project", want: 1},
		{name: "foreign-home", agent: "pi", project: "/other", home: "-other"},
		{name: "ordinary", agent: "unknown", project: "/other"},
		{name: "missing-workspace", agent: "pi", project: "/missing"},
		{name: "ambiguous-workspace", agent: "pi", extra: "  Mount: /other -> /workspace\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupSource(t)
			project := filepath.Join(f.project, tc.project)
			verbose := strings.ReplaceAll(f.mounts("run", project, f.source.Paths.Root+tc.home), "/profiles/work/pi", "/profiles/work/"+tc.agent)
			count := 3
			if tc.name == "missing-workspace" {
				verbose = strings.ReplaceAll(verbose, " -> /workspace", " -> /not-workspace")
			}
			if tc.extra != "" {
				verbose += tc.extra
				count++
			}
			f.store(t, []machine{{Name: "run", State: "running", CPUs: 2, MemoryMiB: 2048, MountCount: count}}, verbose)
			snapshot, err := f.source.Load(context.Background())
			if err != nil || len(snapshot.Instances) != tc.want {
				t.Fatalf("%+v %v, want %d instances", snapshot, err, tc.want)
			}
			if tc.want == 1 && (snapshot.Instances[0].Project != project || snapshot.Instances[0].Agent != tc.agent) {
				t.Fatalf("wrong project or agent: %+v", snapshot.Instances[0])
			}
			if _, err := os.Stat(f.source.Paths.Root); !os.IsNotExist(err) {
				t.Fatal("discovery created data home", err)
			}
		})
	}
}
