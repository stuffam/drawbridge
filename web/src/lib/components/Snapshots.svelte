<script lang="ts">
	// The snapshots of the database the host keeps for itself (docs/PLAN.md §6.6): one a night,
	// and one before an upgrade changes the database. They're listed here and not offered for
	// download: a snapshot has no passphrase, and the database has the admin's password hash in it.
	import { onMount } from 'svelte';
	import { api, type Snapshots } from '$lib/api';
	import { snapshotLabel } from '$lib/backup';
	import { errorMessage } from '$lib/errors';
	import { formatBytes, formatTime } from '$lib/format';

	let snaps = $state<Snapshots>();
	let error = $state('');

	onMount(async () => {
		try {
			snaps = await api.snapshots();
		} catch (err) {
			error = errorMessage(err);
		}
	});
</script>

<section class="card flex flex-col gap-4" aria-labelledby="snapshots-heading">
	<div>
		<h2 id="snapshots-heading" class="font-semibold">Snapshots on This Host</h2>
		<p class="hint">
			Copies of the database that the host keeps in its own storage: one a night, and one before an
			upgrade changes the database. They guard against a bad change or a bad upgrade. They don't
			guard against losing the host or its card; a backup does. They can't be downloaded here,
			because they have no passphrase.
		</p>
	</div>

	{#if error}
		<p class="alert-error" role="alert">{error}</p>
	{/if}

	{#if snaps}
		{#if !snaps.dir}
			<p class="text-sm">This daemon keeps no snapshots.</p>
		{:else}
			{#if !snaps.nightly}
				<p class="text-sm">
					The nightly snapshot is off on this host (<span class="mono whitespace-nowrap"
						>--snapshot-interval 0</span
					>). The one before an upgrade is still made.
				</p>
			{/if}
			{#if snaps.snapshots.length === 0}
				<p class="text-sm text-neutral-500 dark:text-neutral-400">
					None yet. The first nightly one is made shortly after the daemon starts.
				</p>
			{:else}
				<ul
					class="flex flex-col divide-y divide-neutral-100 text-sm dark:divide-neutral-800"
					data-testid="snapshot-list"
				>
					{#each snaps.snapshots as s (s.name)}
						<li class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-2">
							<div>
								<p class="font-medium">{snapshotLabel(s)}</p>
								<p class="mono text-xs text-neutral-500 dark:text-neutral-400">{s.name}</p>
							</div>
							<p class="text-xs text-neutral-500 dark:text-neutral-400">
								{formatTime(s.made_at)} ·
								<span class="whitespace-nowrap">{formatBytes(s.size)}</span>
							</p>
						</li>
					{/each}
				</ul>
			{/if}
			<p class="hint">
				They're in <span class="mono">{snaps.dir}</span>. To go back to one, stop Drawbridge and run
				<span class="mono whitespace-nowrap">sudo drawbridge backup restore</span> with its path:
				<a
					class="font-medium underline"
					href="https://github.com/stuffam/drawbridge/blob/main/docs/backup-restore.md#snapshots-on-the-host"
					target="_blank"
					rel="noopener external">how</a
				>.
			</p>
		{/if}
	{/if}
</section>
