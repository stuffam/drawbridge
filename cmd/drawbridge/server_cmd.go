package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/stuffam/drawbridge/internal/control"
	"github.com/stuffam/drawbridge/internal/views"
)

const serverUsage = `Usage: drawbridge server show|set|confirm|revert [flags]

  show     Show the server's settings, and a change that's waiting to be kept.
  set      Change settings. Changes apply to the tunnel right away; client configs pick
           up endpoint, port, MTU, DNS, and keepalive changes when they're downloaded
           again.
  confirm  Keep a change that is waiting: one made with set --safe, or from the web UI
           (which always waits for a change that could lock you out).
  revert   Undo that change now, without waiting for its time to run out.

Flags for set:
`

func serverCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || !slices.Contains([]string{"show", "set", "confirm", "revert"}, args[0]) {
		fmt.Fprint(stderr, serverUsage)
		return 2
	}
	sub := args[0]
	flags := newFlagSet("server "+sub, stderr)
	socket := flags.String("control", defaultControl, "daemon control socket `path`")
	endpoint := flags.String("endpoint", "", "public `host[:port]` clients connect to, such as vpn.example.com")
	port := flags.Uint("port", 0, "UDP listen `port`")
	mtu := flags.Int("mtu", 0, "tunnel MTU, 1280–1500")
	dns := flags.String("dns", "", "DNS servers for clients: comma-separated `addresses`, \"server\" (the server's VPN addresses that answer a test query, so a DNS resolver such as AdGuard Home must listen on them), or \"none\"")
	force := flags.Bool("force", false, "with --dns server, use the server's VPN addresses even when nothing answers on them")
	keepalive := flags.Int("keepalive", -1, "clients' PersistentKeepalive in `seconds` (0 turns it off)")
	isolation := flags.Bool("client-isolation", true, "block traffic between clients")
	safe := flags.Bool("safe", false, "undo a change that could lock you out (the listen port, removing an admin source) unless `drawbridge server confirm` keeps it within a minute")
	adminAllow := flags.String("admin-allow", "", "extra sources that may reach the web UI, besides the home network and the VPN: comma-separated `prefixes` inside 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10 (Tailscale), or fc00::/7, or \"none\"")
	flags.Usage = func() { fmt.Fprint(stderr, serverUsage); flags.PrintDefaults() }
	pos, err := parseArgs(flags, args[1:])
	if err != nil {
		return flagStatus(err)
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "drawbridge server %s: unexpected argument %q\n", sub, pos[0])
		return 2
	}
	c := control.NewClient(*socket)

	switch sub {
	case "show":
		s, err := c.Settings(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "drawbridge:", err)
			return 1
		}
		printSettings(stdout, s)
		pending, err := c.Pending(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "drawbridge:", err)
			return 1
		}
		printPending(stdout, pending.PendingChange)
		return 0
	case "confirm", "revert":
		var res views.SettingsResult
		var err error
		if sub == "confirm" {
			res, err = c.ConfirmChange(ctx)
		} else {
			res, err = c.RevertChange(ctx)
		}
		if err != nil {
			fmt.Fprintln(stderr, "drawbridge:", err)
			return 1
		}
		if sub == "confirm" {
			fmt.Fprintln(stdout, "Kept the change.")
		} else {
			fmt.Fprintln(stdout, "Undid the change. The settings are back as they were.")
		}
		return warn(stderr, res.Warning, res.ApplyFailed)
	}

	var p views.SettingsPatch
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	delete(set, "control")
	delete(set, "force")
	delete(set, "safe")
	if len(set) == 0 {
		fmt.Fprint(stderr, "drawbridge server set: nothing to change\n\n")
		flags.Usage()
		return 2
	}
	if set["endpoint"] {
		host, epPort, err := parseEndpoint(*endpoint)
		if err != nil {
			fmt.Fprintln(stderr, "drawbridge:", err)
			return 2
		}
		p.EndpointHost, p.EndpointPort = &host, &epPort
	}
	if set["port"] {
		if *port == 0 || *port > 65535 {
			fmt.Fprintln(stderr, "drawbridge: --port must be 1–65535")
			return 2
		}
		v := uint16(*port)
		p.ListenPort = &v
	}
	if set["mtu"] {
		p.MTU = mtu
	}
	if set["dns"] {
		switch strings.TrimSpace(*dns) {
		case "server":
			check, err := c.DNSCheck(ctx)
			if err != nil {
				fmt.Fprintln(stderr, "drawbridge:", err)
				return 1
			}
			for _, r := range check.Results {
				fmt.Fprintf(stderr, "DNS check: %s: %s\n", r.Address, r.Detail)
			}
			switch {
			case len(check.Usable) > 0:
				p.DNS = &check.Usable
			case *force:
				p.DNSDefault = true
			default:
				fmt.Fprintln(stderr, "drawbridge: no DNS resolver answers on the server's VPN addresses, so clients would get no DNS.\n"+
					"Start a resolver that listens on them, or pass --force to use them anyway.")
				return 1
			}
		case "none", "":
			p.DNS = &[]netip.Addr{}
		default:
			var addrs []netip.Addr
			for _, s := range strings.Split(*dns, ",") {
				a, err := netip.ParseAddr(strings.TrimSpace(s))
				if err != nil {
					fmt.Fprintf(stderr, "drawbridge: --dns: %q isn't an IP address\n", s)
					return 2
				}
				addrs = append(addrs, a)
			}
			p.DNS = &addrs
		}
	}
	if set["keepalive"] {
		p.Keepalive = keepalive
	}
	if set["client-isolation"] {
		p.ClientIsolation = isolation
	}
	if set["admin-allow"] {
		prefixes := []netip.Prefix{}
		if v := strings.TrimSpace(*adminAllow); v != "" && v != "none" {
			for _, s := range strings.Split(v, ",") {
				pfx, err := netip.ParsePrefix(strings.TrimSpace(s))
				if err != nil {
					fmt.Fprintf(stderr, "drawbridge: --admin-allow: %q isn't a prefix such as 100.64.10.0/24\n", s)
					return 2
				}
				prefixes = append(prefixes, pfx)
			}
		}
		p.AdminAllowed = &prefixes
	}

	update := c.UpdateSettings
	if *safe {
		update = c.UpdateSettingsSafely
	}
	res, err := update(ctx, p)
	if err != nil {
		fmt.Fprintln(stderr, "drawbridge:", err)
		var ce *control.Error
		if errors.As(err, &ce) && strings.Contains(ce.Message, "waiting to be kept") {
			fmt.Fprintln(stderr, "Keep it with `drawbridge server confirm`, or undo it with `drawbridge server revert`.")
		}
		return 1
	}
	printSettings(stdout, res.Settings)
	if res.PendingChange != nil {
		fmt.Fprintln(stdout)
		printPending(stdout, res.PendingChange)
	} else if *safe {
		fmt.Fprintln(stdout, "\nNothing here could lock you out, so there is nothing to confirm.")
	}
	return warn(stderr, res.Warning, res.ApplyFailed)
}

// printPending says that a change is waiting to be kept, what it is, and what to do about it.
func printPending(w io.Writer, p *views.PendingChangeView) {
	if p == nil {
		return
	}
	keys := make([]string, 0, len(p.Changes))
	for k := range p.Changes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	fmt.Fprintf(w, "\nWaiting to be kept (made by %s via %s):\n", p.Actor, p.Via)
	for _, k := range keys {
		fmt.Fprintf(w, "  %s: %s\n", strings.ReplaceAll(k, "_", " "), p.Changes[k])
	}
	fmt.Fprintf(w, "It is undone in %d s unless you keep it.\n", p.ExpiresIn)
	fmt.Fprintln(w, "Keep it:  drawbridge server confirm")
	fmt.Fprintln(w, "Undo it:  drawbridge server revert")
}

// parseEndpoint splits "host", "host:port", "IPv6", or "[IPv6]:port". A port of 0 means
// the listen port.
func parseEndpoint(s string) (string, uint16, error) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String(), 0, nil
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return s, 0, nil
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("--endpoint: %q isn't a valid port", portStr)
	}
	return host, uint16(port), nil
}

func printSettings(w io.Writer, s views.SettingsView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = "not set; run: drawbridge server set --endpoint <FQDN>"
	}
	ipv6 := "off"
	if s.IPv6Subnet.IsValid() {
		ipv6 = fmt.Sprintf("%s in %s", s.IPv6Address, s.IPv6Subnet)
	}
	dns := joinAddrs(s.DNS)
	if dns == "" {
		dns = "none"
	}
	keepalive := "off"
	if s.Keepalive > 0 {
		keepalive = fmt.Sprintf("%d s", s.Keepalive)
	}
	allowed := make([]string, len(s.ClientAllowedIPs))
	for i, p := range s.ClientAllowedIPs {
		allowed[i] = p.String()
	}
	adminAllowed := "none"
	if len(s.AdminAllowed) > 0 {
		parts := make([]string, len(s.AdminAllowed))
		for i, p := range s.AdminAllowed {
			parts[i] = p.String()
		}
		adminAllowed = strings.Join(parts, ", ")
	}
	rows := [][2]string{
		{"Interface", s.Interface},
		{"Listen port", fmt.Sprintf("%d (UDP)", s.ListenPort)},
		{"Endpoint", endpoint},
		{"Public key", s.PublicKey},
		{"IPv4", fmt.Sprintf("%s in %s", s.IPv4Address, s.IPv4Subnet)},
		{"IPv6", ipv6},
		{"MTU", strconv.Itoa(s.MTU)},
		{"DNS", dns},
		{"Keepalive", keepalive},
		{"Client isolation", onOff(s.ClientIsolation)},
		{"Client AllowedIPs", strings.Join(allowed, ", ")},
		{"Admin sources", adminAllowed},
	}
	for _, r := range rows {
		fmt.Fprintf(tw, "%s:\t%s\n", r[0], r[1])
	}
	_ = tw.Flush()
}

func joinAddrs(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// warn prints a warning from the daemon, and returns 1 if applying the change failed.
func warn(stderr io.Writer, warning string, failed bool) int {
	if warning != "" {
		fmt.Fprintln(stderr, "warning:", warning)
	}
	if failed {
		return 1
	}
	return 0
}
