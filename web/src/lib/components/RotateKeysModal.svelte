<script lang="ts">
	import { api } from '$lib/api';
	import { errorMessage } from '$lib/errors';
	import Result from './Result.svelte';

	let {
		open = $bindable(false),
		id,
		name,
		onrotated
	}: {
		open?: boolean;
		/** The client getting new keys. */
		id: string;
		name: string;
		/** Called once the new keys are in place, with a warning if applying them failed. */
		onrotated: (warning: string) => void;
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
			const res = await api.rotateClientKeys(id);
			dialog?.close();
			onrotated(res.warning ?? '');
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
	aria-labelledby="rotate-keys-heading"
	class="m-auto w-full max-w-sm rounded-xl border border-neutral-200 bg-white p-0 shadow-lg backdrop:bg-black/40 dark:border-neutral-800 dark:bg-neutral-900"
>
	<div class="flex flex-col gap-3 p-5">
		<h2 id="rotate-keys-heading" class="font-semibold">Rotate Keys</h2>
		<p class="text-sm">
			Give <strong>{name}</strong> new keys? The config on its device stops working at once, and the device
			can't connect until you import the new one.
		</p>
		<p class="hint">
			Do this when a device is lost or its config leaked. The new config's QR code is shown next.
		</p>
		<Result {error} />
		<div class="flex justify-end gap-2">
			<button type="button" class="btn" onclick={() => dialog?.close()}>Cancel</button>
			<button type="button" class="btn btn-danger" disabled={rotating} onclick={rotate}>
				{rotating ? 'Rotating…' : 'Rotate keys'}
			</button>
		</div>
	</div>
</dialog>
