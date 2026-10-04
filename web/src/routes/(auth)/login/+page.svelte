<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api';
	import { errorMessage } from '$lib/errors';
	import Result from '$lib/components/Result.svelte';

	let username = $state('');
	let password = $state('');
	// An account with 2FA on asks for a code once the password is right: a second step of the same
	// form, which keeps the password in memory to send again with the code.
	let code = $state('');
	let needCode = $state(false);
	let error = $state('');
	let busy = $state(false);

	onMount(async () => {
		try {
			if ((await api.setupStatus()).needed) await goto(resolve('/setup'));
		} catch {
			// The form reports connection problems when it's used.
		}
	});

	/** Where to go after logging in: the page that sent us here, if it's on this site. */
	function next(): string {
		const n = page.url.searchParams.get('next');
		if (n) {
			const url = new URL(n, page.url.origin);
			if (url.origin === page.url.origin) return url.pathname + url.search;
		}
		return resolve('/');
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			await api.login(username, password, needCode ? code : '');
			// next() is a path on this site; it's checked above.
			// eslint-disable-next-line svelte/no-navigation-without-resolve
			await goto(next());
		} catch (err) {
			if (err instanceof ApiError && err.code === 'totp_required' && !needCode) {
				// The password was right. This isn't a failure, only the next step.
				needCode = true;
			} else {
				error = errorMessage(err);
				if (needCode) {
					code = '';
				} else {
					password = '';
				}
			}
		} finally {
			busy = false;
		}
	}

	function startOver() {
		needCode = false;
		code = password = error = '';
	}
</script>

<svelte:head><title>Log In · Drawbridge</title></svelte:head>

<form class="card flex flex-col gap-4" onsubmit={submit}>
	<h2 class="text-lg font-semibold">Log In</h2>
	{#if needCode}
		<p class="text-sm text-neutral-600 dark:text-neutral-400">
			Logging in as <strong>{username}</strong>. Enter the code your authenticator app shows.
		</p>
		<div>
			<label class="label" for="code">Authentication code</label>
			<!-- svelte-ignore a11y_autofocus -->
			<input
				class="input mono"
				id="code"
				name="code"
				autocomplete="one-time-code"
				autocapitalize="characters"
				spellcheck="false"
				autofocus
				required
				bind:value={code}
			/>
			<p class="hint">
				Six digits. If you can't get at the app, a recovery code works once in its place.
			</p>
		</div>
		<Result {error} />
		<button class="btn btn-primary" type="submit" disabled={busy}>
			{busy ? 'Logging In…' : 'Log In'}
		</button>
		<button class="btn" type="button" onclick={startOver}>Back</button>
		<p class="hint">
			Lost the app and the recovery codes? On the server, <code
				>sudo drawbridge admin disable-2fa</code
			> turns two-factor authentication off.
		</p>
	{:else}
		<div>
			<label class="label" for="username">Username</label>
			<input
				class="input"
				id="username"
				name="username"
				autocomplete="username"
				required
				bind:value={username}
			/>
		</div>
		<div>
			<label class="label" for="password">Password</label>
			<input
				class="input"
				id="password"
				name="password"
				type="password"
				autocomplete="current-password"
				required
				bind:value={password}
			/>
		</div>
		<Result {error} />
		<button class="btn btn-primary" type="submit" disabled={busy}>
			{busy ? 'Logging In…' : 'Log In'}
		</button>
		<p class="hint">
			Locked out? On the server, <code>sudo drawbridge admin reset-password</code> sets a new password.
		</p>
	{/if}
</form>
