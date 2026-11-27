package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

const networkUsage = `Usage: nook network [--machine NAME]
       nook network --json [--machine NAME] [--before BYTE_CURSOR] [--limit N]

Show persistent per-VM outbound network history, including ended machines, in this AGENTNOOK_HOME.
Opens the Approvals tab in nook watch. It needs at least 48 columns and 27 rows;
JSON has no size requirement. Approvals groups requests and recent decisions by
profile and VM. Press 3 for the read-only Audit tab.
History includes ended VMs by default; f toggles running VMs only.
--machine opens Audit filtered to the named VM; x clears the filter.
History keys: up/down or j/k select events; PgUp older; PgDn newer; Home latest.
Every recorded event shows ALLOWED or DENIED and its policy reason.
ALLOWED means policy permission, NOT a successful connection or HTTP response.
ask records engine decisions (TCP/DNS/UDP/ICMP when reported).
full records DENIALS ONLY: smolvm does not expose its allowed traffic.
No retrospective history before this feature. No automatic history deletion.
JSON works without a terminal, runtime or running VM; pages have at most 200 events.
Use page.machine and page.next_before with --machine and --before for older events.

Approve pending TCP connections for nook --network ask sessions in this AGENTNOOK_HOME.
Run in a separate terminal; the agent keeps its own terminal input.
Requires smolvm >= 1.36.1 for protected VM launches, not for this view.
Profile and VM headings are always visible within the list, not selectable.
Up/down or j/k selects requests or events. Expiry never retargets a selection.
Approval keys work only on an explicitly selected pending request.
Keys: a allow once, s allow exact destination IP/port for this VM launch, d deny.
      q, Esc or Ctrl+C close only the view, not the VMs or brokers.
Unanswered requests are denied after 12 seconds (smolvm's limit is 15 seconds).
Retry the guest connection after a timeout. No headless automatic approvals.
Session grants expire when the VM ends and never span VM launches or profiles.
HTTP Host and TLS SNI names are guest-supplied hints, not verified identities or grant keys.
This approves TCP connections, NOT individual HTTP requests, URLs or payloads.
Host/LAN and cloud metadata are blocked before approval; private exceptions require full.
DNS remains host-managed and is not interactively mediated; other UDP/ICMP is denied.
smolvm-internal services are exempt; its own target protection still applies.
A permitted connection can carry many requests or tunnel data to its allowed endpoint.
No TLS interception or data-loss-prevention guarantee. Session grants cover any host at that IP/port.
The owner-only registry stores control tokens, never agent credentials or guest payloads.
`

func (a App) networkCommand(args []string) int {
	fs := flag.NewFlagSet("network", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() { fmt.Fprint(a.Out, networkUsage) }
	asJSON := fs.Bool("json", false, "print history and pending approvals as JSON")
	machine := fs.String("machine", "", "initial archived machine")
	before := fs.Int64("before", 0, "read before a page byte cursor")
	limit := fs.Int("limit", 20, "events per JSON page (1-200)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *before < 0 || *limit < 1 || *limit > 200 || *before != 0 && *machine == "" {
		return a.fail(2, errors.New("invalid network history arguments; use nook network --help"))
	}
	if !*asJSON && !a.Interactive() {
		return a.fail(1, errors.New("network needs terminal input and output; use --json for stored history; unanswered connections remain denied"))
	}
	p, err := paths.Resolve()
	if err != nil {
		return a.fail(1, err)
	}
	initial := egress.HistoryQuery{Machine: *machine, Before: *before, Limit: *limit}
	load := func(queries ...egress.HistoryQuery) (egress.Survey, error) {
		query := initial
		if len(queries) > 0 {
			query = queries[0]
		}
		survey, liveErr := egress.Load(p.Root)
		if liveErr != nil {
			survey.Warning = "Live approvals unavailable: " + liveErr.Error()
		}
		var err error
		survey.History, err = egress.ReadHistory(p.Root, query)
		return survey, err
	}
	if *asJSON {
		survey, err := load()
		if err != nil {
			return a.fail(1, err)
		}
		if err := json.NewEncoder(a.Out).Encode(survey); err != nil {
			return a.fail(1, err)
		}
		return 0
	}
	if a.Network != nil {
		err = a.Network(p.Root, load, egress.Decide)
	} else {
		start := tui.WatchApprovals
		if *machine != "" {
			start = tui.WatchAudit
		}
		err = a.runWatch(p, start, initial)
	}
	if err != nil {
		return a.fail(1, fmt.Errorf("network: %w", err))
	}
	return 0
}
