<script lang="ts">
	import { api } from '$lib/api';
	import { errorMessage } from '$lib/errors';
	import { changeLines, pending, secondsLeft, setPending } from '$lib/pending.svelte';

	let now = $state(Date.now());
	let busy = $state(false);
	let error = $state('');

	// Count down while a change waits; there's nothing to tick otherwise.
	$effect(() => {
		if (!pending.change) return;
		now = Date.now();
		const timer = setInterval(() => (now = Date.now()), 250);
		return () => clearInterval(timer);
	});

	let change = $derived(pending.change);
	let left = $derived(change ? secondsLeft(change.expires_in, pending.receivedAt, now) : 0);

	/** Asks what's waiting now, after an answer that says it isn't what the bar showed. */
	async function refresh() {
		try {
			setPending((await api.applyState()).pending_change);
		} catch {
			// The next status from the live feed says.
		}
	}

	async function resolve(action: () => Promise<unknown>) {
		busy = true;
		error = '';
		try {
			await action();
			setPending(undefined);
		} catch (err) {
			// Too late (it was undone), or already kept or undone from somewhere else.
			error = errorMessage(err);
			await refresh();
		} finally {
			busy = false;
		}
	}
</script>

{#if change}
	<div
		class="border-b border-amber-300 bg-amber-50 text-amber-950 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-100"
		data-testid="pending-change"
	>
		<div class="mx-auto flex max-w-5xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3 text-sm">
			<div class="min-w-60 flex-1">
				<p role="alert" class="font-medium">
					A settings change is waiting to be kept.
					<span class="sr-only">It is undone in about a minute unless you keep it.</span>
				</p>
				<ul class="mt-1">
					{#each changeLines(change.changes) as line (line)}
						<li>{line}</li>
					{/each}
				</ul>
				<p class="mt-1 text-xs">
					Made by {change.actor} ({change.via}).
					<span aria-hidden="true" data-testid="pending-countdown">
						{#if left > 0}
							Undone in {left} s unless you keep it.
						{:else}
							Undoing…
						{/if}
					</span>
				</p>
				{#if error}<p class="mt-1 text-xs font-medium" data-testid="pending-error">{error}</p>{/if}
			</div>
			<div class="flex gap-2">
				<button
					type="button"
					class="btn btn-primary"
					disabled={busy || left === 0}
					onclick={() => resolve(() => api.confirmChange())}>Keep Changes</button
				>
				<button
					type="button"
					class="btn"
					disabled={busy || left === 0}
					onclick={() => resolve(() => api.revertChange())}>Undo Now</button
				>
			</div>
		</div>
	</div>
{/if}
