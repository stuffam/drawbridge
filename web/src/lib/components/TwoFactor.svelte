<script lang="ts">
	// TOTP two-factor authentication (docs/PLAN.md §6.5). Turning it on takes the password again,
	// shows the secret as a QR code, and finishes with the first code from the admin's app. The
	// recovery codes it returns, and the ones "New Recovery Codes" makes, are kept only here, in
	// memory, until the admin says they have them.
	import { onMount } from 'svelte';
	import QRCode from 'qrcode';
	import { api, type TOTPEnrollment, type User } from '$lib/api';
	import { saveText } from '$lib/download';
	import { errorMessage } from '$lib/errors';
	import { groupSecret, LOW_RECOVERY_CODES, recoveryCodesFile } from '$lib/totp';
	import CopyButton from './CopyButton.svelte';
	import Result from './Result.svelte';

	let { username }: { username: string } = $props();

	/** Which form is open: the password to start, the QR code and first code, or a change. */
	type Step = '' | 'password' | 'scan' | 'renew' | 'disable';

	let user = $state<User>();
	let loadError = $state('');
	let step = $state<Step>('');
	let password = $state('');
	let code = $state('');
	let enrollment = $state<TOTPEnrollment>();
	let qr = $state('');
	let codes = $state<string[]>([]);
	let error = $state('');
	let success = $state('');
	let busy = $state(false);

	async function load() {
		try {
			user = (await api.me()).user;
		} catch (err) {
			loadError = errorMessage(err);
		}
	}
	onMount(load);

	function open(next: Step) {
		step = next;
		error = success = '';
		password = code = '';
	}

	function close() {
		step = '';
		enrollment = undefined;
		qr = '';
		password = code = '';
	}

	/** Runs a form's request, shows what went wrong, and closes the form when it went right. */
	async function run(e: SubmitEvent, work: () => Promise<void>) {
		e.preventDefault();
		error = success = '';
		busy = true;
		try {
			await work();
		} catch (err) {
			error = errorMessage(err);
			code = '';
		} finally {
			busy = false;
		}
	}

	const begin = (e: SubmitEvent) =>
		run(e, async () => {
			enrollment = await api.enrollTotp(password);
			qr = await QRCode.toDataURL(enrollment.uri, {
				errorCorrectionLevel: 'M',
				margin: 2,
				width: 224
			});
			password = code = '';
			step = 'scan';
		});

	const finish = (e: SubmitEvent) =>
		run(e, async () => {
			codes = (await api.verifyTotp(code)).codes;
			close();
			await load();
		});

	const renew = (e: SubmitEvent) =>
		run(e, async () => {
			codes = (await api.newRecoveryCodes(password, code)).codes;
			close();
			await load();
		});

	const disable = (e: SubmitEvent) =>
		run(e, async () => {
			await api.disableTotp(password, code);
			close();
			codes = [];
			success = 'Two-factor authentication is off. Your other browsers are logged out.';
			await load();
		});
</script>

<section class="card flex flex-col gap-4" aria-labelledby="totp-heading">
	<div>
		<h2 id="totp-heading" class="font-semibold">Two-Factor Authentication</h2>
		<p class="hint">
			With it on, logging in takes a six-digit code from an authenticator app on your phone as well
			as the password, so a leaked password isn't enough. Turning it on or off logs out your other
			browsers.
		</p>
	</div>

	{#if codes.length > 0}
		<div class="alert-warning flex flex-col gap-3" role="status" data-testid="recovery-codes">
			<p class="font-medium">Save these recovery codes now. They won't be shown again.</p>
			<p class="text-sm">
				Each one works once, in place of the code from your app, if you lose your phone. Keep them
				somewhere safe that isn't on the phone.
			</p>
			<ul class="mono grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
				{#each codes as c (c)}
					<li>{c}</li>
				{/each}
			</ul>
			<div class="flex flex-wrap items-center gap-2">
				<CopyButton text={codes.join('\n')} label="Copy" />
				<button
					class="btn px-2 py-1 text-xs"
					type="button"
					onclick={() =>
						saveText('drawbridge-recovery-codes.txt', recoveryCodesFile(username, codes))}
				>
					Download
				</button>
			</div>
			<button class="btn self-start" type="button" onclick={() => (codes = [])}>
				I've Saved Them
			</button>
		</div>
	{/if}

	<Result error={loadError} />
	{#if user}
		{#if user.totp_enabled}
			<div class="flex flex-col gap-1 text-sm">
				<p>
					<span class="font-medium text-emerald-600">On.</span> A login needs the password and a code.
				</p>
				<p
					class={user.recovery_codes_left <= LOW_RECOVERY_CODES
						? 'font-medium text-amber-600'
						: 'text-neutral-600 dark:text-neutral-400'}
					data-testid="recovery-left"
				>
					{user.recovery_codes_left}
					{user.recovery_codes_left === 1 ? 'recovery code' : 'recovery codes'} left.
					{#if user.recovery_codes_left <= LOW_RECOVERY_CODES}Make new ones soon.{/if}
				</p>
			</div>
			{#if step === ''}
				<div class="flex flex-wrap gap-2">
					<button class="btn" type="button" onclick={() => open('renew')}>
						New Recovery Codes…
					</button>
					<button class="btn" type="button" onclick={() => open('disable')}>Turn Off…</button>
				</div>
			{:else if step === 'renew' || step === 'disable'}
				<form class="flex max-w-xl flex-col gap-3" onsubmit={step === 'renew' ? renew : disable}>
					<h3 class="text-sm font-semibold">
						{step === 'renew' ? 'New Recovery Codes' : 'Turn Off Two-Factor Authentication'}
					</h3>
					<p class="hint">
						{step === 'renew'
							? 'The ones you have now stop working.'
							: 'Logging in will need only the password.'}
						This takes your password and a code, so a stolen login can't do it.
					</p>
					<input type="hidden" autocomplete="username" value={username} />
					<div>
						<label class="label" for="totp-password">Your password</label>
						<input
							class="input"
							id="totp-password"
							type="password"
							autocomplete="current-password"
							required
							bind:value={password}
						/>
					</div>
					<div>
						<label class="label" for="totp-code">Code</label>
						<input
							class="input mono"
							id="totp-code"
							autocomplete="one-time-code"
							autocapitalize="characters"
							spellcheck="false"
							required
							bind:value={code}
						/>
						<p class="hint">From your app, or an unused recovery code.</p>
					</div>
					<Result {error} />
					<div class="flex gap-2">
						<button
							class={step === 'renew' ? 'btn btn-primary' : 'btn btn-danger'}
							type="submit"
							disabled={busy}
						>
							{step === 'renew' ? 'Make New Codes' : 'Turn Off'}
						</button>
						<button class="btn" type="button" onclick={close}>Cancel</button>
					</div>
				</form>
			{/if}
		{:else if step === ''}
			<div>
				<button class="btn btn-primary" type="button" onclick={() => open('password')}>
					Turn On…
				</button>
			</div>
		{:else if step === 'password'}
			<form class="flex max-w-xl flex-col gap-3" onsubmit={begin}>
				<h3 class="text-sm font-semibold">Turn On Two-Factor Authentication</h3>
				<input type="hidden" autocomplete="username" value={username} />
				<div>
					<label class="label" for="totp-password">Your password</label>
					<input
						class="input"
						id="totp-password"
						type="password"
						autocomplete="current-password"
						required
						bind:value={password}
					/>
					<p class="hint">
						Again, because whoever turns this on decides what the second factor is.
					</p>
				</div>
				<Result {error} />
				<div class="flex gap-2">
					<button class="btn btn-primary" type="submit" disabled={busy}>
						{busy ? 'Starting…' : 'Continue'}
					</button>
					<button class="btn" type="button" onclick={close}>Cancel</button>
				</div>
			</form>
		{:else if step === 'scan' && enrollment}
			<form class="flex max-w-xl flex-col gap-3" onsubmit={finish}>
				<h3 class="text-sm font-semibold">Add It to Your App</h3>
				<p class="text-sm">
					Scan this with an authenticator app (any that makes standard six-digit codes: Aegis,
					Google Authenticator, 1Password, Bitwarden, and so on), then enter the code it shows. It
					isn't on until you do.
				</p>
				<div class="flex flex-wrap items-center gap-4">
					<img
						class="rounded-lg bg-white"
						src={qr}
						width="224"
						height="224"
						alt="QR code that adds Drawbridge to an authenticator app"
					/>
					<div class="flex min-w-0 flex-col gap-2 text-sm">
						<p>Or type this key into the app:</p>
						<code
							class="mono rounded bg-neutral-100 px-2 py-1 break-words dark:bg-neutral-800"
							data-testid="totp-secret">{groupSecret(enrollment.secret)}</code
						>
						<div><CopyButton text={enrollment.secret} label="Copy Key" /></div>
					</div>
				</div>
				<div>
					<label class="label" for="totp-first-code">Code from the app</label>
					<input
						class="input mono"
						id="totp-first-code"
						inputmode="numeric"
						autocomplete="one-time-code"
						spellcheck="false"
						required
						bind:value={code}
					/>
				</div>
				<Result {error} />
				<div class="flex gap-2">
					<button class="btn btn-primary" type="submit" disabled={busy}>
						{busy ? 'Checking…' : 'Turn On'}
					</button>
					<button class="btn" type="button" onclick={close}>Cancel</button>
				</div>
			</form>
		{/if}
		<Result {success} />
	{/if}
</section>
