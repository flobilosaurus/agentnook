package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestParseMountCanonicalizesDirectoriesAndModes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, shared := filepath.Join(root, "project"), filepath.Join(root, "shared with spaces")
	for _, dir := range []string{project, shared} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	for _, tc := range []struct {
		value string
		ro    bool
	}{
		{shared + ":/mnt/shared", false},
		{"../shared with spaces:/mnt/shared:ro", true},
		{link + ":/mnt/shared/", false},
		{"~/shared with spaces:/mnt/shared:ro", true},
	} {
		mount, err := ParseMount(tc.value, project)
		if err != nil || mount.Host != shared || mount.Guest != "/mnt/shared" || mount.ReadOnly != tc.ro {
			t.Fatalf("%q: %+v %v", tc.value, mount, err)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"", shared, ":/mnt/shared", shared + ":", shared + ":relative",
		shared + ":/mnt/shared:rw", shared + ":/mnt/shared:ro:extra",
		shared + ":/root/.pi", shared + ":/mnt/../root/.pi", shared + ":/mnt/bad\n",
		file + ":/mnt/shared", filepath.Join(root, "missing") + ":/mnt/shared",
	} {
		if _, err := ParseMount(value, project); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestValidateActiveMountSources(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Paths{Root: filepath.Join(root, "data")}
	shared := filepath.Join(root, "shared")
	private := filepath.Join(p.Root, "profiles", "work", ".pi")
	for _, dir := range []string{shared, private} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.ValidateMounts([]config.Mount{{Host: shared, Guest: "/mnt/shared"}}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "private-alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{root, p.Root, private, alias, filepath.Join(root, "missing")} {
		if err := p.ValidateMounts([]config.Mount{{Host: host, Guest: "/mnt/shared", ReadOnly: true}}); err == nil {
			t.Errorf("accepted unsafe or unavailable source %q", host)
		}
	}
	if err := os.Remove(shared); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, shared); err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateMounts([]config.Mount{{Host: shared, Guest: "/mnt/shared"}}); err == nil {
		t.Fatal("retargeted persisted source accepted")
	}
}
