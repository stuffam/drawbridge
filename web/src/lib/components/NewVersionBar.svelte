<script lang="ts">
	import { updated } from '$app/state';

	// The daemon under this page was upgraded: the page in the browser is still the old one, and
	// stays so until it is reloaded, however long it's used (it only reloads by itself when it
	// needs code it hasn't loaded). SvelteKit polls for the new build (vite.config.ts), and the
	// reload is the person's to make, because it would lose what they haven't saved.
</script>

{#if updated.current}
	<div
		class="border-b border-sky-300 bg-sky-50 text-sky-950 dark:border-sky-800 dark:bg-sky-950 dark:text-sky-100"
		data-testid="new-version"
	>
		<div class="mx-auto flex max-w-5xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3 text-sm">
			<p role="status" class="min-w-60 flex-1 font-medium">
				A newer version of Drawbridge is available. Reload this page to use it.
			</p>
			<button type="button" class="btn btn-primary" onclick={() => location.reload()}>Reload</button
			>
		</div>
	</div>
{/if}
