<script lang="ts">
	// The certificate the browser sees for this page (docs/PLAN.md §6.6): the self-signed one the
	// daemon makes, or the admin's own. Installing one takes the account's password again, because
	// a logged-in browser alone shouldn't be able to put a certificate whose key it holds in front
	// of the admin's next login. The daemon checks the pair before anything changes, and the next
	// connection is shown the new certificate with no restart.
	import { onMount } from 'svelte';
	import { api, type Certificate } from '$lib/api';
	import { checkPem, describeValidity, sourceLabels } from '$lib/certificate';
	import { errorMessage } from '$lib/errors';
	import CopyButton from './CopyButton.svelte';
	import Result from './Result.svelte';

	let { username }: { username: string } = $props();

	let cert = $state<Certificate>();
	let loadError = $state('');
	let now = $state(new Date());

	let certificate = $state('');
	let privateKey = $state('');
	let password = $state('');
	let error = $state('');
	let success = $state('');
	let busy = $state(false);
	let resetting = $state(false);

	async function load() {
		try {
			cert = await api.certificate();
			now = new Date();
			loadError = '';
		} catch (err) {
			loadError = errorMessage(err);
		}
	}
	onMount(load);

	let certProblem = $derived(checkPem(certificate, 'certificate'));
	let keyProblem = $derived(checkPem(privateKey, 'key'));

	/** Fills a field from a file the admin picked. Nothing leaves the browser until Install. */
	async function pick(e: Event, set: (text: string) => void) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		error = '';
		if (file.size > 60 * 1024) {
			error = `${file.name} is far bigger than a certificate or a key. Is it the right file?`;
		} else {
			set(await file.text());
		}
		// So that picking the same file again still counts.
		input.value = '';
	}

	async function install(e: SubmitEvent) {
		e.preventDefault();
		error = success = '';
		busy = true;
		try {
			cert = await api.installCertificate(password, certificate, privateKey);
			now = new Date();
			// Nothing secret stays in the page once the daemon has it.
			certificate = privateKey = password = '';
			success =
				'Installed. New connections use it now; reload this page to see what your browser makes of it.';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function reset() {
		error = success = '';
		resetting = true;
		try {
			cert = await api.resetCertificate();
			now = new Date();
			success = 'Back on the self-signed certificate. New connections use it now.';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			resetting = false;
		}
	}
</script>

<section class="card flex flex-col gap-4" aria-labelledby="certificate-heading">
	<div>
		<h2 id="certificate-heading" class="font-semibold">Web UI Certificate</h2>
		<p class="hint">
			What your browser is shown when it opens this page. Drawbridge makes a self-signed one, which
			browsers warn about until you trust it. Install your own (from a certificate authority such as
			Let's Encrypt, or one your devices already trust) and they won't.
		</p>
	</div>

	{#if loadError}
		<p class="alert-error" role="alert">{loadError}</p>
	{:else if cert}
		<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm" data-testid="certificate-details">
			<dt class="text-neutral-500 dark:text-neutral-400">Source</dt>
			<dd data-testid="certificate-source">{sourceLabels[cert.source]}</dd>
			<dt class="text-neutral-500 dark:text-neutral-400">Subject</dt>
			<dd class="break-all">{cert.subject}</dd>
			<dt class="text-neutral-500 dark:text-neutral-400">Issuer</dt>
			<dd class="break-all">{cert.issuer}{cert.self_issued ? ' (itself)' : ''}</dd>
			<dt class="text-neutral-500 dark:text-neutral-400">Names</dt>
			<dd class="break-all">{cert.names.join(', ')}</dd>
			<dt class="text-neutral-500 dark:text-neutral-400">Valid</dt>
			<dd>{describeValidity(cert, now)}</dd>
			<dt class="text-neutral-500 dark:text-neutral-400">SHA-256</dt>
			<dd class="flex items-start gap-2">
				<span class="mono" data-testid="certificate-fingerprint">{cert.fingerprint}</span>
				<CopyButton text={cert.fingerprint} />
			</dd>
		</dl>
		{#each cert.notes as note (note)}
			<p class="alert-warning" role="status" data-testid="certificate-note">{note}</p>
		{/each}
		{#if cert.source === 'uploaded'}
			<div>
				<button class="btn" type="button" onclick={reset} disabled={resetting}>
					{resetting ? 'Going Back…' : 'Use the Self-Signed Certificate'}
				</button>
				<p class="hint">Forgets the installed certificate and its key.</p>
			</div>
		{/if}
	{:else}
		<p class="hint">Loading…</p>
	{/if}

	<form
		class="flex max-w-xl flex-col gap-3 border-t border-neutral-200 pt-4 dark:border-neutral-800"
		onsubmit={install}
	>
		<h3 class="text-sm font-semibold">Install Your Own</h3>
		<input type="hidden" autocomplete="username" value={username} />
		<div>
			<label class="label" for="cert-pem">Certificate</label>
			<textarea
				class="input mono h-32"
				id="cert-pem"
				spellcheck="false"
				autocomplete="off"
				required
				placeholder="-----BEGIN CERTIFICATE-----"
				bind:value={certificate}></textarea>
			<input
				type="file"
				class="file-input mt-1 text-xs text-neutral-600 dark:text-neutral-400"
				aria-label="Choose a certificate file"
				onchange={(e) => pick(e, (text) => (certificate = text))}
			/>
			{#if certProblem}<p class="hint text-amber-700 dark:text-amber-400" role="status">
					{certProblem}
				</p>{/if}
			<p class="hint">
				The server's own certificate first, then the intermediates: a "fullchain" file has them in
				that order.
			</p>
		</div>
		<div>
			<label class="label" for="key-pem">Private key</label>
			<textarea
				class="input mono h-24"
				id="key-pem"
				spellcheck="false"
				autocomplete="off"
				required
				placeholder="-----BEGIN PRIVATE KEY-----"
				bind:value={privateKey}></textarea>
			<input
				type="file"
				class="file-input mt-1 text-xs text-neutral-600 dark:text-neutral-400"
				aria-label="Choose a private key file"
				onchange={(e) => pick(e, (text) => (privateKey = text))}
			/>
			{#if keyProblem}<p class="hint text-amber-700 dark:text-amber-400" role="status">
					{keyProblem}
				</p>{/if}
			<p class="hint">
				Without a passphrase. It's kept on this host, readable only by Drawbridge, and isn't in a
				backup, so keep your own copy.
			</p>
		</div>
		<div>
			<label class="label" for="cert-password">Your password</label>
			<input
				class="input"
				id="cert-password"
				type="password"
				autocomplete="current-password"
				required
				bind:value={password}
			/>
			<p class="hint">Again, because the key will be used to prove this server's identity.</p>
		</div>
		<Result {error} {success} />
		<button class="btn btn-primary self-start" type="submit" disabled={busy}>
			{busy ? 'Installing…' : 'Install Certificate'}
		</button>
		<p class="hint">
			Both are checked first, and nothing changes if either is refused. The daemon doesn't restart,
			but nothing renews an installed certificate: install a new one before it expires. The System
			checks above warn you when it's close.
		</p>
	</form>
</section>
