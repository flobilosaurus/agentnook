package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRealOpenCodeVM(t *testing.T) {
	fixture, _ := realVMSmoke(t, "opencode", "2.3.2")
	t.Run("EnvironmentForwarding", func(t *testing.T) {
		plugin := filepath.Join(fixture.workspace, ".opencode", "plugins", "env-probe")
		if err := os.MkdirAll(plugin, 0o700); err != nil {
			t.Fatal(err)
		}
		const manifest = `{"name":"agentnook-env-probe","type":"module","exports":"./index.mjs"}`
		const source = `import { writeFileSync } from "node:fs";
export default {
  id: "agentnook.env-probe",
  setup(ctx) {
    writeFileSync(ctx.location.directory + "/` + envProbeFile + `", process.env.` + exampleEnv + ` ?? "<unset>");
  },
};`
		for name, contents := range map[string]string{"package.json": manifest, "index.mjs": source} {
			if err := os.WriteFile(filepath.Join(plugin, name), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// Listing agents initializes the workspace plugin without a model request.
		fixture.testEnvironmentProbe(t, "debug", "agents", "--standalone")
	})
}
