<script lang="ts">
	// Downloads a backup (docs/PLAN.md §6.6): one file with the database and the secret key,
	// encrypted with a passphrase chosen here. It asks for the account's password again, because
	// the file holds every secret the server has, and a logged-in browser alone shouldn't be able
	// to carry them off. Restoring is not here: it replaces the password too, so only the root
	// command line does it.
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { checkPassphrase, MIN_PASSPHRASE } from '$lib/backup';
	import { saveBlob } from '$lib/download';
	import { errorMessage } from '$lib/errors';
	import { formatAgo, formatBytes, formatTime } from '$lib/format';
	import Result from './Result.svelte';

	let { username }: { username: string } = $props();

	let password = $state('');
	let passphrase = $state('');
	let again = $state('');
	let error = $state('');
	let busy = $state(false);
	let saved = $state<{ name: string; size: number }>();
	// When the newest backup was made, by anyone: null when there is none, and undefined until
	// that's known (or when it couldn't be read, which the form doesn't need).
	let last = $state<string | null>();
	let now = $state(Date.now());

	async function loadLast() {
		try {
			const [newest] = await api.events({ kind: 'backup.created', limit: 1 });
			last = newest ? newest.time : null;
			now = Date.now();
		} catch {
			last = undefined;
		}
	}
	onMount(loadLast);

	async function make(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		saved = undefined;
		const problem = checkPassphrase(passphrase, again);
		if (problem) {
			error = problem;
			return;
		}
		busy = true;
		try {
			const file = await api.downloadBackup(password, passphrase);
			saveBlob(file.name, file.blob);
			saved = { name: file.name, size: file.blob.size };
			// Nothing secret stays in the page once the file is out.
			password = passphrase = again = '';
			await loadLast();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<section class="card flex flex-col gap-4" aria-labelledby="backup-heading">
	<div>
		<h2 id="backup-heading" class="font-semibold">Backup</h2>
		<p class="hint">
			One file with the database and the secret key it's encrypted with: everything needed to
			rebuild this server on a new host, clients included. It's encrypted with a passphrase you
			choose here, and that is all that protects it, so keep it somewhere other than this host.
			Restoring one is done from the command line on the host, because it replaces the password too:
			<a
				class="font-medium underline"
				href="https://github.com/stuffam/drawbridge/blob/main/docs/backup-restore.md"
				target="_blank"
				rel="noopener external">how</a
			>.
		</p>
		{#if last !== undefined}
			<p class="mt-1 text-sm" data-testid="last-backup">
				{#if last}
					Last backup: {formatTime(last)}
					<span class="text-neutral-500 dark:text-neutral-400">({formatAgo(last, now)})</span>.
				{:else}
					No backup has been made yet.
				{/if}
			</p>
		{/if}
	</div>

	<form class="flex max-w-xl flex-col gap-3" onsubmit={make}>
		<input type="hidden" autocomplete="username" value={username} />
		<div>
			<label class="label" for="backup-password">Your password</label>
			<input
				class="input"
				id="backup-password"
				type="password"
				autocomplete="current-password"
				required
				bind:value={password}
			/>
			<p class="hint">Again, because the file holds every secret this server has.</p>
		</div>
		<div>
			<label class="label" for="backup-passphrase">Passphrase for the file</label>
			<input
				class="input"
				id="backup-passphrase"
				type="password"
				autocomplete="new-password"
				minlength={MIN_PASSPHRASE}
				required
				bind:value={passphrase}
			/>
			<p class="hint">
				At least {MIN_PASSPHRASE} characters. It can't be recovered: without it the backup can't be opened.
				A password manager is a good place for it.
			</p>
		</div>
		<div>
			<label class="label" for="backup-again">Passphrase again</label>
			<input
				class="input"
				id="backup-again"
				type="password"
				autocomplete="new-password"
				required
				bind:value={again}
			/>
		</div>
		<Result
			{error}
			success={saved
				? `Saved ${saved.name} (${formatBytes(saved.size)}). Keep it, and its passphrase, somewhere other than this host.`
				: ''}
		/>
		<button class="btn btn-primary self-start" type="submit" disabled={busy}>
			{busy ? 'Making the Backup…' : 'Download Backup'}
		</button>
	</form>
</section>
