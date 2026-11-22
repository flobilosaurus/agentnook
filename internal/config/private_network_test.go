package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizePrivateCIDRs(t *testing.T) {
	got, err := NormalizePrivateCIDRs([]string{"10.20.1.2/16", "fd00:0000::1/64", "10.20.0.0/16", "127.0.0.1/32", "::1/128"})
	want := []string{"10.20.0.0/16", "fd00::/64", "127.0.0.1/32", "::1/128"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	for _, value := range []string{"", "10.20.0.1", "localhost/32", "10.0.0.0/33", "::/129", "::ffff:127.0.0.1/128", "fe80::1%en0/64", "10.0.0.0/8\n--net", " 10.0.0.0/8"} {
		if _, err := NormalizePrivateCIDRs([]string{value}); err == nil {
			t.Fatal("invalid CIDR accepted", value)
		}
	}
}

func TestPrivateExceptionsCannotBypassAsk(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		values []string
		valid  bool
	}{
		{"", nil, true}, {"ask", nil, true}, {"full", []string{"10.20.0.0/16"}, true},
		{"", []string{"fd00::/64"}, true}, {"ask", []string{"10.20.0.0/16"}, false},
		{"full", []string{"10.20.1.2/16"}, false},
		{"full", []string{"10.20.0.0/16", "10.20.0.0/16"}, false},
		{"full", []string{"bad"}, false},
	} {
		p := Project{Network: tc.mode, AllowPrivate: tc.values}
		if err := p.ValidateNetwork(); (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}

func TestPrivateExceptionsRoundTripAndConfigValidation(t *testing.T) {
	c := Empty()
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	w := testWorkspace("work", testOptions())
	p := w.Agents["pi"]
	p.Network, p.AllowPrivate = "full", []string{"10.20.0.0/16", "fd00::/64"}
	w.Agents["pi"] = p
	c.Projects["/project"] = w
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(file, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil || !reflect.DeepEqual(got.Projects["/project"].Agents["pi"].AllowPrivate, p.AllowPrivate) {
		t.Fatal(got, err)
	}
	p.Network = "ask"
	w.Agents["pi"] = p
	if err := c.Validate(); err == nil {
		t.Fatal("config accepted broker-bypassing exception under ask")
	}
}
