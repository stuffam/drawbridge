# Architecture decision records

Each record explains one decision from docs/PLAN.md §3: the context, the decision, and its
consequences. They're numbered in the order they were written. To reverse a decision, add a new
record that supersedes the old one and update the old one's status, rather than rewriting history.
To refine one without reversing it, add a dated **Amended** note to its record and set its status
to "Accepted, amended". Either way, update docs/PLAN.md §3 and §16 in the same PR.

| Record | Decision | Status |
|---|---|---|
| [0001](0001-go-backend.md) | Go for the backend (D1) | Accepted |
| [0002](0002-svelte-web-ui.md) | Svelte 5 and SvelteKit for the web UI (D2) | Accepted |
| [0003](0003-sqlite-datastore.md) | SQLite as the datastore (D3) | Accepted |
| [0004](0004-database-source-of-truth.md) | The database is the source of truth (D4) | Accepted |
| [0005](0005-netlink-not-wg-quick.md) | Drive WireGuard over netlink, not wg-quick (D5) | Accepted |
| [0006](0006-nftables-own-table.md) | nftables with a table Drawbridge owns (D6) | Accepted |
| [0007](0007-unprivileged-cap-net-admin.md) | Run unprivileged, with only CAP_NET_ADMIN (D7) | Accepted, amended |
| [0008](0008-separate-tunnel-and-ui-units.md) | Separate systemd units for the tunnel and the UI (D8) | Accepted |
| [0009](0009-server-sent-events.md) | Server-Sent Events for live updates (D9) | Accepted |
| [0010](0010-deb-with-nfpm.md) | Distribute as a .deb built with nfpm (D10) | Accepted |
| [0011](0011-admin-ui-lan-and-vpn-only.md) | Admin UI on the home network and VPN only (D11) | Accepted, amended |
| [0012](0012-adguard-home-client-dns.md) | A resolver on the host, such as AdGuard Home, as the clients' DNS (D12) | Accepted, amended |
| [0013](0013-docs-site-zensical.md) | A documentation site built with Zensical, on GitHub Pages (D13) | Accepted, amended |
