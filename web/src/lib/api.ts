// A typed client for Drawbridge's API (internal/api/openapi.json on the Go side). The
// field names match the server's JSON exactly, so nothing is renamed in between.

import { backupFileName } from './backup';

/** An error response from the API. */
export class ApiError extends Error {
	constructor(
		/** The HTTP status. */
		readonly status: number,
		message: string,
		/** Seconds to wait before retrying, for a 429. */
		readonly retryAfter?: number
	) {
		super(message);
		this.name = 'ApiError';
	}
}

export interface Settings {
	interface: string;
	listen_port: number;
	endpoint_host: string;
	endpoint_port: number;
	/** host:port as clients see it; empty until endpoint_host is set. */
	endpoint: string;
	public_key: string;
	mtu: number;
	ipv4_subnet: string;
	ipv4_address: string;
	/** Empty when IPv6 is off. */
	ipv6_subnet: string;
	ipv6_address: string;
	dns: string[];
	keepalive: number;
	client_isolation: boolean;
	client_allowed_ips: string[];
	/** Extra sources, besides the home network and the VPN, that may reach this UI. */
	admin_allowed: string[];
}

/** Changes to the settings; omitted fields stay as they are. */
export interface SettingsPatch {
	endpoint_host?: string;
	endpoint_port?: number;
	listen_port?: number;
	mtu?: number;
	dns?: string[];
	/** Reset dns to the server's VPN addresses; use dns for anything else. */
	dns_default?: boolean;
	keepalive?: number;
	client_isolation?: boolean;
	admin_allowed?: string[];
}

export interface SettingsResult {
	settings: Settings;
	warning?: string;
	apply_failed?: boolean;
}

export interface ServerStatus {
	tunnel_up: boolean;
	clients: number;
	paused: number;
	online: number;
	/** Clients whose config changed since it was last handed out (`config_outdated`). */
	outdated: number;
	/**
	 * Bytes the server has received from, and sent to, the clients in the tunnel now: the sum of
	 * their counters. A client's counters start over when it's resumed, so these fall when one is
	 * paused or deleted, and start over when the tunnel restarts.
	 */
	receive_bytes: number;
	send_bytes: number;
	/** Set when the AdGuard Home name sync needs the admin; absent when all is well. */
	adguard_warning?: string;
}

/** A `status` message of the stream: what the server's status and the client list return, together. */
export interface StreamStatus {
	server: ServerStatus;
	clients: Client[];
}

/** What asking one of the server's VPN addresses for DNS found. */
export interface DNSProbe {
	address: string;
	answered: boolean;
	detail: string;
}

/** A host diagnostic's outcome: warn is something that may be wrong, fail is clients affected now. */
export type CheckStatus = 'pass' | 'warn' | 'fail' | 'skip';

/** One host check, the same as a line of `drawbridge doctor`. */
export interface DiagnosticCheck {
	/** A stable slug, such as "forwarding". */
	id: string;
	name: string;
	status: CheckStatus;
	/** What was found. */
	detail: string;
	/** How to fix a warning or a failure. */
	hint?: string;
}

export interface Diagnostics {
	/** In the order to read them: the tunnel first, then the host, then what's around it. */
	checks: DiagnosticCheck[];
}

/** One snapshot of the database that the host keeps for itself. */
export interface SnapshotInfo {
	/** The file's name in the snapshot directory. */
	name: string;
	/** `nightly` is the daily one; `pre-migration` is made before an upgrade changes the database. */
	kind: 'nightly' | 'pre-migration';
	made_at: string;
	/** The database version a pre-migration snapshot holds. Absent for a nightly one. */
	schema?: number;
	/** Bytes. */
	size: number;
}

export interface Snapshots {
	/** Where they are on the host. Empty when this daemon keeps none. */
	dir: string;
	/** Whether the daily snapshot is on. The one before an upgrade is made either way. */
	nightly: boolean;
	/** Newest first. */
	snapshots: SnapshotInfo[];
}

/** A file the server made, with the name it gave it. */
export interface Download {
	blob: Blob;
	name: string;
}

export interface DNSCheck {
	results: DNSProbe[];
	/** The addresses that answered; empty when no resolver runs on the host. */
	usable: string[];
}

/** The saved connection to AdGuard Home. The password can be set, and is never returned. */
export interface AdGuardConnection {
	/** False when nothing is saved; base_url is then the usual address of a local AdGuard Home. */
	configured: boolean;
	base_url: string;
	/** Empty when AdGuard Home has no login. */
	username: string;
	has_password: boolean;
	/** The admin's switch for using the connection at all. */
	enabled: boolean;
	/** Whether Drawbridge writes its clients' names into AdGuard Home. Matters only while enabled. */
	sync_names: boolean;
	sync: AdGuardSync;
}

/**
 * How the name sync is doing. `off`: not turned on. `pending`: no pass has finished yet. `ok`: the
 * last pass finished, and may have conflicts. `error`: it failed, and another is coming. `stopped`:
 * AdGuard Home refused the account, so nothing is asked until the connection changes or a test
 * or Sync now shows it works.
 */
export interface AdGuardSync {
	state: 'off' | 'pending' | 'ok' | 'error' | 'stopped';
	/** When a pass last finished without failing. */
	last_sync: string | null;
	error?: string;
	/** How many clients have their name in AdGuard Home. */
	synced: number;
	/** Clients that couldn't be named there: a name or an address its own client has. */
	conflicts: { client_id: string; client: string; reason: string }[];
}

/** Saves or tests a connection; omitted fields stay as they are. */
export interface AdGuardRequest {
	base_url?: string;
	username?: string;
	/** Replaces the saved password; "" removes it. Needed when the address or username changes. */
	password?: string;
	/** The switches take no password. */
	enabled?: boolean;
	sync_names?: boolean;
}

/** What a test of the connection to AdGuard Home found. A connection that fails is ok: false. */
export interface AdGuardTest {
	ok: boolean;
	error?: string;
	/** AdGuard Home refused the account, and trying again won't help. */
	refused: boolean;
	version?: string;
	running: boolean;
	protection_enabled: boolean;
	/** Null when AdGuard Home's query-log settings couldn't be read. */
	query_log: { enabled: boolean; anonymize_client_ip: boolean } | null;
	/** What asking the server's VPN addresses for DNS found, whatever AdGuard Home said. */
	dns: DNSProbe[];
	warnings: string[];
}

/** One DNS query a client made, from AdGuard Home's query log. */
export interface DNSQuery {
	time: string;
	/** Which of the client's addresses it came from. */
	address: string;
	domain: string;
	/** The record type asked for: A, AAAA, HTTPS, and so on. */
	type: string;
	/** The DNS answer's code: NOERROR, NXDOMAIN, SERVFAIL, and so on. */
	status: string;
	/** AdGuard Home's filters answered, by the rule below. */
	blocked: boolean;
	rule?: string;
	cached: boolean;
	answers: string[];
	elapsed_ms: number;
}

/** A client's recent DNS queries. `off` means the AdGuard Home integration isn't turned on. */
export interface DNSLog {
	state: 'off' | 'ok' | 'error';
	error?: string;
	/** The address of AdGuard Home's API, for a link to its own query log. */
	adguard_url?: string;
	addresses: string[];
	queries: DNSQuery[];
	/** Why an empty list is empty, when AdGuard Home's settings are the reason. */
	warnings: string[];
}

/** A read-only API token. Its secret isn't here: it's shown once, when the token is made. */
export interface APIToken {
	id: string;
	name: string;
	/** The first 8 characters of the secret, to tell one token from another. */
	prefix: string;
	scope: 'read';
	created_at: string;
	/** Null until the token is first used, and then accurate to the hour. */
	last_used_at: string | null;
}

export interface NewAPITokenResult {
	token: APIToken;
	/** The token itself. This response is the only place it ever appears. */
	secret: string;
}

export interface Peer {
	endpoint?: string;
	last_handshake?: string;
	/** All-time total; resets only when the peer is recreated (a pause and resume, or the
	 *  tunnel restarting). */
	receive_bytes: number;
	send_bytes: number;
	/** When the client's current connection began. Absent when it has none. */
	session_started_at?: string;
	/** Bytes transferred during the client's current connection. Absent when it has none. */
	session_receive_bytes?: number;
	session_send_bytes?: number;
}

export interface Client {
	id: string;
	name: string;
	enabled: boolean;
	ipv4: string;
	/** Empty when IPv6 is off. */
	ipv6: string;
	public_key: string;
	created_at: string;
	/**
	 * When the config was last downloaded or shown as a QR code. Absent when it never was, and for
	 * a client that predates the tracking, until its config is next viewed.
	 */
	config_delivered_at?: string;
	/**
	 * The server's settings or the client's keys changed since then, so the config the client holds
	 * no longer matches and it must import the new one. Absent when false, and in the response to
	 * a change (only the list, a single client, and the live feed carry it).
	 */
	config_outdated?: boolean;
	/** Absent when the client isn't in the tunnel: it's paused, or the tunnel is down. */
	peer?: Peer;
}

export interface ClientResult {
	client: Client;
	warning?: string;
	apply_failed?: boolean;
}

export interface User {
	username: string;
	created_at: string;
	last_login_at?: string;
}

export interface Session {
	id: string;
	created_at: string;
	last_seen_at: string;
	expires_at: string;
	ip: string;
	user_agent: string;
	current: boolean;
}

export interface Me {
	user: User;
	session: Session;
}

export interface DrawbridgeEvent {
	id: number;
	time: string;
	kind: string;
	category: 'admin' | 'system' | 'connection';
	actor: string;
	via: 'web' | 'cli' | 'system';
	source_ip?: string;
	client_id?: string;
	client_name?: string;
	data?: Record<string, string>;
}

export interface EventFilter {
	client?: string;
	category?: 'admin' | 'system' | 'connection';
	/** One kind of event, such as `client.connected`. */
	kind?: string;
	/** Only events at or after this time (an ISO 8601 string). */
	from?: string;
	/** Only events before this time. */
	to?: string;
	before?: number;
	limit?: number;
}

/**
 * 1m reads the last minute's polls, which the daemon keeps in memory. 1h, 12h, and 24h read raw
 * samples; 7d, 30d, and 90d read the hourly rollup.
 */
export type TrafficRange = '1m' | '1h' | '12h' | '24h' | '7d' | '30d' | '90d';

export interface TrafficSample {
	bucket_start: string;
	receive_bytes: number;
	send_bytes: number;
}

/** One series of traffic history, with the window it covers: one client's, or every client's summed. */
export interface TrafficSeries {
	/** How long one sample covers, so a sample's bytes over it is a rate. */
	step_seconds: number;
	/** Where the history ends: every sample is complete, and starts before it. */
	until: string;
	/** Oldest first. A stored bucket in which nothing moved isn't saved: it's zero. */
	samples: TrafficSample[];
}

/** One client's samples in a TrafficHistory. */
export interface ClientTrafficSeries {
	id: string;
	name: string;
	/** Oldest first. A stored bucket in which the client moved nothing isn't saved: it's zero. */
	samples: TrafficSample[];
}

/** Every client's traffic over a range, for the charts page. */
export interface TrafficHistory {
	/** How long one sample covers, so a sample's bytes over it is a rate. */
	step_seconds: number;
	/** Where the history ends: every sample is complete, and ends at or before it. */
	until: string;
	/** Every client, sorted by name. */
	clients: ClientTrafficSeries[];
}

export interface ClientSession {
	id: string;
	started_at: string;
	/** Absent while the session is still open. */
	ended_at?: string;
	endpoint: string;
	receive_bytes: number;
	send_bytes: number;
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

/** Called when a request finds the session has ended, so the app can go to the login. */
let unauthorized: (() => void) | undefined;

/** Sets what happens when a request finds the session has ended. */
export function onUnauthorized(handler: (() => void) | undefined): void {
	unauthorized = handler;
}

/** The fetch function requests use; tests replace it. */
let fetchFn: typeof fetch = (...args) => fetch(...args);

/** Replaces the fetch function, for tests. */
export function setFetch(fn: typeof fetch): void {
	fetchFn = fn;
}

function eventQuery(filter: EventFilter, format?: 'csv'): string {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(filter)) {
		if (v !== undefined && v !== '') q.set(k, String(v));
	}
	if (format) q.set('format', format);
	const qs = q.toString();
	return qs ? '?' + qs : '';
}

/** Sends a request, with the CSRF header and the session's cookie. */
function send(method: Method, path: string, body?: unknown): Promise<Response> {
	// Every change carries X-Drawbridge, which a cross-site request can't (the CSRF check).
	const headers: Record<string, string> = { 'X-Drawbridge': '1' };
	let payload: string | undefined;
	if (body !== undefined) {
		headers['Content-Type'] = 'application/json';
		payload = JSON.stringify(body);
	}
	return fetchFn(path, { method, headers, body: payload, credentials: 'same-origin' });
}

/** The error a failed response is, and a lapsed session's trip to the login. */
async function failure(res: Response, method: Method, path: string): Promise<ApiError> {
	const text = await res.text();
	let data: unknown = text;
	if (text && (res.headers.get('Content-Type') ?? '').startsWith('application/json')) {
		data = JSON.parse(text);
	}
	if (res.status === 401 && !path.startsWith('/api/auth/login')) {
		unauthorized?.();
	}
	const message =
		typeof data === 'object' &&
		data !== null &&
		typeof (data as { error?: unknown }).error === 'string'
			? (data as { error: string }).error
			: `${method} ${path} failed with status ${res.status}`;
	const retry = Number(res.headers.get('Retry-After'));
	return new ApiError(res.status, message, Number.isFinite(retry) && retry > 0 ? retry : undefined);
}

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
	const res = await send(method, path, body);
	if (res.status === 204) {
		return undefined as T;
	}
	if (!res.ok) {
		throw await failure(res, method, path);
	}
	const text = await res.text();
	if (text && (res.headers.get('Content-Type') ?? '').startsWith('application/json')) {
		return JSON.parse(text) as T;
	}
	return text as T;
}

const clientPath = (id: string, suffix = '') => `/api/clients/${encodeURIComponent(id)}${suffix}`;

export const api = {
	setupStatus: () => request<{ needed: boolean }>('GET', '/api/setup'),
	setup: (token: string, username: string, password: string) =>
		request<Me>('POST', '/api/setup', { token, username, password }),
	login: (username: string, password: string) =>
		request<Me>('POST', '/api/auth/login', { username, password }),
	logout: () => request<void>('POST', '/api/auth/logout'),
	me: () => request<Me>('GET', '/api/auth/me'),
	changePassword: (current_password: string, new_password: string) =>
		request<void>('POST', '/api/auth/password', { current_password, new_password }),
	apiTokens: () => request<APIToken[]>('GET', '/api/auth/tokens'),
	createApiToken: (name: string, password: string) =>
		request<NewAPITokenResult>('POST', '/api/auth/tokens', { name, password }),
	revokeApiToken: (id: string) =>
		request<void>('DELETE', `/api/auth/tokens/${encodeURIComponent(id)}`),
	sessions: () => request<Session[]>('GET', '/api/auth/sessions'),
	revokeSession: (id: string) =>
		request<void>('DELETE', `/api/auth/sessions/${encodeURIComponent(id)}`),

	server: () => request<Settings>('GET', '/api/server'),
	updateServer: (patch: SettingsPatch) => request<SettingsResult>('PATCH', '/api/server', patch),
	status: () => request<ServerStatus>('GET', '/api/server/status'),
	dnsCheck: () => request<DNSCheck>('GET', '/api/server/dns-check'),
	diagnostics: () => request<Diagnostics>('GET', '/api/system/health'),
	snapshots: () => request<Snapshots>('GET', '/api/system/snapshots'),
	/**
	 * Makes a backup and returns the file. It takes the account's password again, and the
	 * passphrase the file is encrypted with. The server makes the whole file before it sends any
	 * of it, so what comes back is complete or an error.
	 */
	downloadBackup: async (password: string, passphrase: string): Promise<Download> => {
		const path = '/api/system/backup';
		const res = await send('POST', path, { password, passphrase });
		if (!res.ok) {
			throw await failure(res, 'POST', path);
		}
		const name = backupFileName(res.headers.get('Content-Disposition'));
		const cut = new ApiError(0, 'The backup was cut short on the way. Try again.');
		let blob: Blob;
		try {
			blob = await res.blob();
		} catch {
			throw cut;
		}
		// The server announces the length, so a body that ended early is one to refuse.
		const want = Number(res.headers.get('Content-Length'));
		if (blob.size === 0 || (want > 0 && blob.size !== want)) {
			throw cut;
		}
		return { blob, name };
	},

	adguard: () => request<AdGuardConnection>('GET', '/api/integrations/adguard'),
	saveAdGuard: (r: AdGuardRequest) =>
		request<AdGuardConnection>('PUT', '/api/integrations/adguard', r),
	removeAdGuard: () => request<AdGuardConnection>('DELETE', '/api/integrations/adguard'),
	syncAdGuard: () => request<AdGuardSync>('POST', '/api/integrations/adguard/sync'),
	clientDnsLog: (id: string, limit?: number) =>
		request<DNSLog>('GET', clientPath(id, '/dns-log' + (limit ? `?limit=${limit}` : ''))),
	testAdGuard: (r: AdGuardRequest) =>
		request<AdGuardTest>('POST', '/api/integrations/adguard/test', r),

	clients: () => request<Client[]>('GET', '/api/clients'),
	client: (id: string) => request<Client>('GET', clientPath(id)),
	addClient: (name: string) => request<ClientResult>('POST', '/api/clients', { name }),
	renameClient: (id: string, name: string) =>
		request<ClientResult>('PATCH', clientPath(id), { name }),
	pauseClient: (id: string) => request<ClientResult>('POST', clientPath(id, '/pause')),
	resumeClient: (id: string) => request<ClientResult>('POST', clientPath(id, '/resume')),
	/**
	 * Gives the client new keys. The old ones stop working at once, so the client is cut off until
	 * it imports the new config.
	 */
	rotateClientKeys: (id: string) => request<ClientResult>('POST', clientPath(id, '/rotate-keys')),
	deleteClient: (id: string) => request<ClientResult>('DELETE', clientPath(id)),
	/** The client's WireGuard config. Every download is recorded in the event log. */
	clientConfig: (id: string) => request<string>('GET', clientPath(id, '/config')),

	events: (filter: EventFilter = {}) =>
		request<DrawbridgeEvent[]>('GET', '/api/events' + eventQuery(filter)),
	/** Every event that matches the filter, as the text of a CSV file. */
	eventsCsv: (filter: EventFilter = {}) =>
		request<string>(
			'GET',
			'/api/events' + eventQuery({ ...filter, before: undefined, limit: undefined }, 'csv')
		),

	/** Every client's traffic history, summed: the dashboard's bandwidth chart. */
	traffic: (range: TrafficRange = '24h') =>
		request<TrafficSeries>('GET', '/api/traffic?range=' + range),
	/** Every client's traffic history, one series per client: the charts page. */
	trafficByClient: (range: TrafficRange = '24h') =>
		request<TrafficHistory>('GET', '/api/traffic/clients?range=' + range),
	/** One client's traffic history. */
	clientTraffic: (id: string, range: TrafficRange = '24h') =>
		request<TrafficSeries>('GET', clientPath(id, '/traffic?range=' + range)),
	/** One client's connection history, newest first: open and closed sessions alike. */
	clientSessions: (id: string, before?: string, limit?: number) => {
		const q = new URLSearchParams();
		if (before) q.set('before', before);
		if (limit) q.set('limit', String(limit));
		const qs = q.toString();
		return request<ClientSession[]>('GET', clientPath(id, '/sessions' + (qs ? '?' + qs : '')));
	}
};
