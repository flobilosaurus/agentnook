// Process-level smolvm stand-in. No Docker, network, VM or real credentials.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func value(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func save(path string, data any) {
	content, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		panic(err)
	}
}
func logBuild(args []string) {
	file, err := os.OpenFile(os.Getenv("AGENTNOOK_TEST_CAPTURE")+".build", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(args); err != nil {
		panic(err)
	}
}
func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		version := os.Getenv("AGENTNOOK_TEST_SMOLVM_VERSION")
		if version == "" {
			version = "smolvm 1.36.1"
		}
		if os.Getenv("AGENTNOOK_TEST_ACTION") == "old-runtime" {
			version = "smolvm 1.35.4"
		}
		fmt.Println(version)
		return
	}
	if len(args) < 2 {
		os.Exit(99)
	}
	capture := os.Getenv("AGENTNOOK_TEST_CAPTURE")
	name := value(args, "--name")
	build := strings.HasPrefix(name, "nook-build-") || args[0] == "pack"
	if args[0] == "machine" && args[1] == "run" { // packed-state verification
		logBuild(args)
		return
	}
	if build {
		logBuild(args)
	}
	dir := capture + ".machines"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic(err)
	}
	state := filepath.Join(dir, name+".json")
	switch args[0] + " " + args[1] {
	case "machine create":
		save(state, args)
	case "machine data-dir":
		path := filepath.Join(dir, name+".audit")
		if err := os.MkdirAll(path, 0o700); err != nil {
			panic(err)
		}
		fmt.Println(path)
	case "machine start", "machine stop":
		if _, err := os.Stat(state); err != nil {
			panic(err)
		}
		if !build && args[1] == "stop" && os.Getenv("AGENTNOOK_TEST_ACTION") == "network-denial" {
			file, err := os.OpenFile(filepath.Join(dir, name+".audit", "egress-denials.log"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				panic(err)
			}
			_, err = file.WriteString("[2026-08-13T02:31:06Z]: egress policy denied resolve private.example\n")
			file.Close()
			if err != nil {
				panic(err)
			}
		}
	case "machine delete":
		_ = os.Remove(state)
		_ = os.RemoveAll(filepath.Join(dir, name+".audit"))
	case "pack create":
		if os.Getenv("AGENTNOOK_TEST_ACTION") == "pack-fail" {
			os.Exit(8)
		}
		output := value(args, "--output")
		if output == "" || strings.HasSuffix(output, ".smolmachine") {
			os.Exit(98)
		}
		if err := os.WriteFile(output, []byte("test-only-stub"), 0o700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(output+".smolmachine", []byte("test-only-artifact:"+output), 0o600); err != nil {
			panic(err)
		}
	case "machine exec":
		if build {
			switch os.Getenv("AGENTNOOK_TEST_ACTION") {
			case "build-fail":
				os.Exit(7)
			case "tools-fail":
				for _, arg := range args {
					if arg == "agentnook-mise" {
						os.Exit(9)
					}
				}
			}
			return
		}
		content, err := os.ReadFile(state)
		if err != nil {
			panic(err)
		}
		var created []string
		if err := json.Unmarshal(content, &created); err != nil {
			panic(err)
		}
		// Capture the effective launch, excluding generated lifecycle identities.
		launch := []string{"machine", "run"}
		for i := 2; i < len(created) && created[i] != "--"; i++ {
			if created[i] == "--name" || created[i] == "--label" {
				i++
				continue
			}
			launch = append(launch, created[i])
		}
		for _, arg := range args {
			if arg == "--interactive" || arg == "--tty" {
				launch = append(launch, arg)
			}
		}
		for i, arg := range args {
			if arg == "--" {
				launch = append(launch, args[i:]...)
				break
			}
		}
		save(capture, launch)
		if os.Getenv("AGENTNOOK_TEST_ACTION") == "network-denial" {
			if err := os.WriteFile(filepath.Join(dir, name+".audit", "egress-denials.log"), []byte("[2026-08-13T02:31:05Z]: egress policy denied connect to 100.96.0.1:1455\n"), 0o600); err != nil {
				panic(err)
			}
		}
		for _, arg := range launch {
			if !strings.HasSuffix(arg, ":/root") {
				continue
			}
			auth := filepath.Join(strings.TrimSuffix(arg, ":/root"), ".pi", "agent", "auth.json")
			switch os.Getenv("AGENTNOOK_TEST_ACTION") {
			case "login":
				if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
					panic(err)
				}
				if err := os.WriteFile(auth, []byte("{\"fake-provider\":{\"type\":\"api_key\",\"key\":\"test-only\"}}\n"), 0o600); err != nil {
					panic(err)
				}
			case "read":
				data, err := os.ReadFile(auth)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(91)
				}
				fmt.Print(string(data))
			}
		}
		code, _ := strconv.Atoi(os.Getenv("AGENTNOOK_TEST_EXIT"))
		os.Exit(code)
	default:
		fmt.Fprintln(os.Stderr, "unsupported smolvm arguments", args)
		os.Exit(99)
	}
}
