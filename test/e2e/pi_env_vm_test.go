package e2e

import (
	"context"
	"regexp"
	"testing"
	"time"
)

func (f realVMFixture) testPiEnvironmentForwarding(t *testing.T) {
	testEnvironmentLaunches(t, func(t *testing.T, ctx context.Context, flags []string, want string) {
		cmd := f.command(ctx, append(flags, "--", "--offline", "--no-session")...)
		cmd.Env = append(cmd.Env, exampleEnv+"="+want)
		pi := startTestTerminal(t, cmd)
		pi.expect(t, 90*time.Second, `Pi can explain its own features`)

		// The expected value is not in the echoed input. Delimit it to check the full value.
		pi.send(t, `!printf 'nook-env=<%s>\n' "$`+exampleEnv+`"`+"\r")
		pi.expect(t, 20*time.Second, regexp.QuoteMeta("nook-env=<"+want+">"))
		pi.send(t, "/quit\r")
		pi.wait(t)
	})
}
