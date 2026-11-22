package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func catalogEntry() BuiltImage {
	options := testOptions()
	options.Image = "/images/work-v1.smolmachine"

	return BuiltImage{Name: "work-v1", BaseImage: "node:24-bookworm",
		Architecture: "arm64", BuiltAt: "2026-11-17T10:00:00Z", VM: options}
}

func TestImageCatalogRoundTripAndDuplicates(t *testing.T) {
	c := Empty()
	entry := catalogEntry()
	if err := c.AddImage(entry); err != nil {
		t.Fatal(err)
	}
	if found, ok := c.FindImage("WORK-v1"); !ok || found != entry {
		t.Fatalf("case-insensitive lookup: %+v %v", found, ok)
	}
	duplicate := entry
	duplicate.Name = "WORK-V1"
	if err := c.AddImage(duplicate); err == nil {
		t.Fatal("duplicate image name accepted")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	if err := Update(path, func(current *Config) error {
		return current.AddProfile("new-profile")
	}); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil || !reflect.DeepEqual(got.Images, c.Images) {
		t.Fatal("profile transaction lost catalog")
	}
}

func TestRejectInvalidCatalog(t *testing.T) {
	for _, change := range []func(*BuiltImage){
		func(e *BuiltImage) { e.Name = "../escape" },
		func(e *BuiltImage) { e.BaseImage = "" },
		func(e *BuiltImage) { e.Architecture = "unknown" },
		func(e *BuiltImage) { e.BuiltAt = "yesterday" },
		func(e *BuiltImage) { e.VM.Image = "relative.smolmachine" },
		func(e *BuiltImage) { e.VM.Image = "/images/../work.smolmachine" },
		func(e *BuiltImage) { e.VM.Image = "/images/work" },
		func(e *BuiltImage) { e.VM.Agent = "bad/name" },
		func(e *BuiltImage) { e.VM.AgentVersion = "latest" },
		func(e *BuiltImage) { e.VM.Memory = 0 },
	} {
		c := Empty()
		entry := catalogEntry()
		change(&entry)
		if err := c.AddImage(entry); err == nil {
			t.Fatalf("accepted %+v", entry)
		}
		c.Images = []BuiltImage{entry}
		if err := c.Validate(); err == nil {
			t.Fatalf("config accepted %+v", entry)
		}
	}
	c := Empty()
	c.Images = []BuiltImage{catalogEntry(), catalogEntry()}
	if err := c.Validate(); err == nil {
		t.Fatal("config accepted duplicate catalog names")
	}
}
