package config

import "testing"

func TestValidateNetworkModes(t *testing.T) {
	for _, mode := range []string{"", "full", "ask", "ASK", "off", "all"} {
		err := ValidateNetwork(mode)
		valid := mode == "" || mode == "full" || mode == "ask"
		if (err == nil) != valid {
			t.Fatal(mode, err)
		}
	}
}
