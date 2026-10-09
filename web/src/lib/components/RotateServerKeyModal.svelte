<script lang="ts">
	import { api, type SettingsResult } from '$lib/api';
	import { errorMessage } from '$lib/errors';
	import { setPending } from '$lib/pending.svelte';
	import Result from './Result.svelte';

	let {
		open = $bindable(false),
		onrotated
	}: {
		open?: boolean;
		/** Called once the new key is in place, with the response (the settings, and the change waiting). */
		onrotated: (res: SettingsResult) => void;
	} = $props();

	let dialog = $state<HTMLDialogElement>();
	let error = $state('');
	let rotating = $state(false);

	$effect(() => {
		if (open) {
			error = '';
			dialog?.showModal();
		} else {
			dialog?.close();
		}
	});

	// The dialog closes itself on Escape, or on the backdrop click below; keep `open` in sync
	// either way.
	function onClose() {
		open = false;
	}

	function onBackdropClick(e: MouseEvent) {
		if (e.target === dialog) dialog.close();
	}

	async function rotate() {
		rotating = true;
		error = '';
		try {
			const res = await api.rotateServerKey();
			setPending(res.pending_change);
			dialog?.close();
			onrotated(res);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			rotating = false;
		}
	}
</script>

<dialog
	bind:this={dialog}
	onclose={onClose}
	onclick={onBackdropClick}
	aria-labelledby="rotate-server-key-heading"
	class="m-auto w-full max-w-md rounded-xl border border-neutral-200 bg-white p-0 shadow-lg backdrop:bg-black/40 dark:border-neutral-800 dark:bg-neutral-900"
>
	<div class="flex flex-col gap-3 p-5">
		<h2 id="rotate-server-key-heading" class="font-semibold">Rotate the Server Key</h2>
		<p class="text-sm">
			Give the server a new key? <strong>Every client stops working at once</strong>, because each
			one's config holds the old key. A device can't connect again until you import its new config.
		</p>
		<p class="hint">
			If you're connected through the VPN, this cuts you off. The change is undone after a minute
			unless you keep it, so keep it from the home network, or let it undo. Do this when the
			server's key may have leaked, not to tidy up.
		</p>
		<Result {error} />
		<div class="flex justify-end gap-2">
			<button type="button" class="btn" onclick={() => dialog?.close()}>Cancel</button>
			<button type="button" class="btn btn-danger" disabled={rotating} onclick={rotate}>
				{rotating ? 'Rotating…' : 'Rotate the Key'}
			</button>
		</div>
	</div>
</dialog>
