<script lang="ts">
	// The host diagnostics: the same checks as `drawbridge doctor`, run on the server when this
	// opens and again whenever the admin asks. A check can take a few seconds (it asks DNS), so
	// the list stays up while it runs again, and the new results replace it together.
	import { onMount } from 'svelte';
	import { api, type CheckStatus, type DiagnosticCheck } from '$lib/api';
	import { needsAttention, statusLabels, summarize } from '$lib/diagnostics';
	import { errorMessage } from '$lib/errors';
	import { formatClock } from '$lib/format';

	let checks = $state<DiagnosticCheck[]>();
	let checkedAt = $state<Date>();
	let running = $state(false);
	let error = $state('');

	async function run() {
		running = true;
		error = '';
		try {
			checks = (await api.diagnostics()).checks;
			checkedAt = new Date();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			running = false;
		}
	}
	onMount(run);

	const dots: Record<CheckStatus, string> = {
		pass: 'bg-emerald-500',
		warn: 'bg-amber-400',
		fail: 'bg-red-500',
		skip: 'bg-neutral-300 dark:bg-neutral-600'
	};
</script>

<section class="card flex flex-col gap-4" aria-labelledby="diagnostics-heading">
	<div class="flex flex-wrap items-start justify-between gap-3">
		<div>
			<h2 id="diagnostics-heading" class="font-semibold">Diagnostics</h2>
			<p class="hint">
				What the VPN needs from this host and its network. Nothing here is changed; each problem
				says how to fix it.
			</p>
		</div>
		<button class="btn" type="button" onclick={run} disabled={running}>
			{running ? 'Checking…' : 'Run again'}
		</button>
	</div>

	{#if error}
		<p class="alert-error" role="alert">{error}</p>
	{/if}

	{#if checks}
		<p class="text-sm" aria-live="polite" data-testid="diagnostics-summary">
			{summarize(checks)}
			{#if checkedAt}
				<span class="text-neutral-500 dark:text-neutral-400"
					>Checked {formatClock(checkedAt, true)}.</span
				>
			{/if}
		</p>
		<ul class="flex flex-col divide-y divide-neutral-200 dark:divide-neutral-800">
			{#each checks as c (c.id)}
				<li class="flex flex-col gap-1 py-3 first:pt-0 last:pb-0" data-status={c.status}>
					<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
						<span class="inline-flex items-center gap-1.5 text-sm font-medium">
							<span class="size-2.5 rounded-full {dots[c.status]}" aria-hidden="true"></span>
							{c.name}
						</span>
						<span class="text-xs text-neutral-500 dark:text-neutral-400"
							>{statusLabels[c.status]}</span
						>
					</div>
					<p class="text-sm text-neutral-700 dark:text-neutral-300">{c.detail}</p>
					{#if c.hint && needsAttention(c)}
						<p class="text-sm">
							<span class="font-medium">Fix:</span>
							{c.hint}
						</p>
					{/if}
				</li>
			{/each}
		</ul>
	{:else if running}
		<p class="hint">Checking the host…</p>
	{/if}
</section>
