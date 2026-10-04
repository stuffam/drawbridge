package main

import (
	"context"
	"fmt"
	"io"

	"github.com/stuffam/drawbridge/internal/control"
)

const applyUsage = `Usage: drawbridge apply [--dry-run]

Makes the WireGuard interface and the firewall match the settings and the clients in the
database, and lists what it changed. The daemon does the same every 30 seconds and after every
change, so this is for when you don't want to wait, or want to see the difference: after a
change you made by hand with wg or nft, or to check that nothing has drifted.

With --dry-run it changes nothing and lists what it would change.

It never starts a tunnel that is stopped: use systemctl start drawbridge-tunnel for that.

Flags:
`

func applyCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("apply", stderr)
	socket := flags.String("control", defaultControl, "daemon control socket `path`")
	dryRun := flags.Bool("dry-run", false, "list what would change, and change nothing")
	flags.Usage = func() { fmt.Fprint(stderr, applyUsage); flags.PrintDefaults() }
	pos, err := parseArgs(flags, args)
	if err != nil {
		return flagStatus(err)
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "drawbridge apply: unexpected argument %q\n", pos[0])
		return 2
	}
	res, err := control.NewClient(*socket).Apply(ctx, *dryRun)
	if err != nil {
		fmt.Fprintln(stderr, "drawbridge:", err)
		return 1
	}
	switch {
	case res.TunnelDown:
		fmt.Fprintln(stdout, "The tunnel is stopped, so there is nothing to apply. Start it with: sudo systemctl start drawbridge-tunnel")
	case len(res.Changes) == 0:
		fmt.Fprintln(stdout, "Nothing to change: the tunnel and the firewall match the settings.")
	default:
		if res.DryRun {
			fmt.Fprintln(stdout, "Would change:")
		} else {
			fmt.Fprintln(stdout, "Changed:")
		}
		for _, c := range res.Changes {
			fmt.Fprintln(stdout, "  "+c)
		}
	}
	return 0
}
