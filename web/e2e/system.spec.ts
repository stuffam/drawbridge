import { expect, test } from '@playwright/test';
import { X509Certificate } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { connect } from 'node:tls';
import { port, stateDir } from '../playwright.config';
import { admin, cli, cliOnFiles, login, navigate, watchConsole } from './helpers';

// These share one daemon with app.spec.ts (playwright.config.ts: workers: 1), and run after its
// setup test, so the admin account already exists.
test.describe.configure({ mode: 'serial' });

test('the System page runs the host checks, and runs them again on request', async ({ page }) => {
	const problems = watchConsole(page);
	await login(page);
	await navigate(page, 'System');
	await expect(page).toHaveURL(/\/system$/);
	await expect(page.getByRole('heading', { name: 'System' })).toBeVisible();

	// The daemon runs the real checks against the machine the test runs on, so what each one
	// finds varies. That there are 13 in a fixed order, each with a status and what it found, doesn't.
	const section = page.getByRole('region', { name: 'Diagnostics' });
	const rows = section.getByRole('listitem');
	await expect(rows).toHaveCount(13);
	await expect(rows.first()).toContainText('Tunnel');
	await expect(rows.last()).toContainText('TLS certificate');
	for (const row of await rows.all()) {
		await expect(row).toHaveAttribute('data-status', /^(pass|warn|fail|skip)$/);
		await expect(row).toContainText(/Pass|Warning|Failed|Skipped/);
	}
	await expect(section.getByTestId('diagnostics-summary')).toContainText(
		/Checked \d\d:\d\d:\d\d\./
	);

	// A warning or a failure says how to fix it, and a pass doesn't.
	for (const row of await rows.all()) {
		const status = await row.getAttribute('data-status');
		if (status === 'warn' || status === 'fail') await expect(row).toContainText('Fix:');
		else await expect(row).not.toContainText('Fix:');
	}

	// Run again asks the daemon again.
	const again = page.waitForResponse((r) => r.url().endsWith('/api/system/health'));
	await section.getByRole('button', { name: 'Run again' }).click();
	expect((await again).status()).toBe(200);
	await expect(section.getByRole('button', { name: 'Run again' })).toBeEnabled();

	expect(problems).toEqual([]);
});

test('the System page tells a failure from a warning, and says what to do', async ({ page }) => {
	const problems = watchConsole(page);
	await page.route('**/api/system/health', (route) =>
		route.fulfill({
			json: {
				checks: [
					{ id: 'tunnel', name: 'Tunnel', status: 'pass', detail: 'wg0 is up with 3 peers.' },
					{
						id: 'forwarding',
						name: 'Forwarding sysctls',
						status: 'fail',
						detail: "IPv4 forwarding is off, so VPN clients' traffic isn't routed.",
						hint: 'sudo sysctl -w net.ipv4.ip_forward=1.'
					},
					{
						id: 'time-sync',
						name: 'Clock',
						status: 'warn',
						detail: 'The clock may not be synced.',
						hint: 'Install systemd-timesyncd.'
					},
					{ id: 'uplink', name: 'Uplink', status: 'skip', detail: "Couldn't read the routes." }
				]
			}
		})
	);
	await login(page);
	await page.goto('/system');
	const section = page.getByRole('region', { name: 'Diagnostics' });
	await expect(section.getByTestId('diagnostics-summary')).toContainText(
		'1 failed, 1 warning, 1 skipped, 1 passed.'
	);
	const rows = section.getByRole('listitem');
	await expect(rows).toHaveCount(4);
	await expect(rows.nth(1)).toContainText('Failed');
	await expect(rows.nth(1)).toContainText('Fix: sudo sysctl -w net.ipv4.ip_forward=1.');
	await expect(rows.nth(2)).toContainText('Warning');
	await expect(rows.nth(2)).toContainText('Fix: Install systemd-timesyncd.');
	await expect(rows.nth(0)).not.toContainText('Fix:');
	await expect(rows.nth(3)).not.toContainText('Fix:');
	expect(problems).toEqual([]);
});

test('the System page says so when the checks cannot run', async ({ page }) => {
	await page.route('**/api/system/health', (route) =>
		route.fulfill({
			status: 501,
			json: { error: "this daemon can't run the diagnostics" }
		})
	);
	await login(page);
	await page.goto('/system');
	const section = page.getByRole('region', { name: 'Diagnostics' });
	await expect(section.getByRole('alert')).toContainText("can't run the diagnostics");
	await expect(section.getByRole('listitem')).toHaveCount(0);
});

const isHealth = (r: { url(): string }) => r.url().endsWith('/api/system/health');

test('the dashboard raises the checks that need attention, and only those', async ({ page }) => {
	const problems = watchConsole(page);
	await page.route('**/api/system/health', (route) =>
		route.fulfill({
			json: {
				checks: [
					// The dashboard has its own, fresher word on the tunnel.
					{ id: 'tunnel', name: 'Tunnel', status: 'fail', detail: 'wg0 is stopped.' },
					{ id: 'uplink', name: 'Uplink', status: 'pass', detail: 'Uplink is eth0.' },
					{
						id: 'forwarding',
						name: 'Forwarding sysctls',
						status: 'fail',
						detail: "IPv4 forwarding is off, so VPN clients' traffic isn't routed.",
						hint: 'sudo sysctl -w net.ipv4.ip_forward=1.'
					},
					{
						id: 'time-sync',
						name: 'Clock',
						status: 'warn',
						detail: 'The clock may not be synced.',
						hint: 'Install systemd-timesyncd.'
					},
					{ id: 'disk-space', name: 'Free disk space', status: 'skip', detail: "Couldn't read it." }
				]
			}
		})
	);
	await login(page);
	const banner = page.getByTestId('diagnostics-warning');
	await expect(banner).toContainText('2 checks need attention');
	await expect(banner).toHaveClass(/alert-error/); // a failure makes it red
	await expect(banner.getByRole('listitem')).toHaveText([
		"Forwarding sysctls: IPv4 forwarding is off, so VPN clients' traffic isn't routed.",
		'Clock: The clock may not be synced.'
	]);
	await expect(banner).not.toContainText('Tunnel');
	await expect(banner).not.toContainText('Uplink');
	await expect(banner).not.toContainText('Free disk space');
	// The fix is on the System page, and the banner leads there.
	await banner.getByRole('link', { name: 'See System' }).click();
	await expect(page).toHaveURL(/\/system$/);
	expect(problems).toEqual([]);
});

test('the dashboard banner is amber for a warning, and absent when all is well', async ({
	page
}) => {
	let checks = [
		{ id: 'time-sync', name: 'Clock', status: 'warn', detail: 'The clock may not be synced.' },
		{ id: 'uplink', name: 'Uplink', status: 'pass', detail: 'Uplink is eth0.' }
	];
	await page.route('**/api/system/health', (route) => route.fulfill({ json: { checks } }));
	const first = page.waitForResponse(isHealth);
	await login(page);
	await first;
	const banner = page.getByTestId('diagnostics-warning');
	await expect(banner).toContainText('1 check needs attention');
	await expect(banner).toContainText('for how to fix it.');
	await expect(banner).toHaveClass(/alert-warning/);

	checks = checks.map((c) => ({ ...c, status: 'pass' }));
	const again = page.waitForResponse(isHealth);
	await page.reload();
	await again;
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
	await expect(banner).toHaveCount(0);
});

test('the dashboard runs the checks again when the settings change', async ({ page }) => {
	let broken = true;
	await page.route('**/api/system/health', (route) =>
		route.fulfill({
			json: {
				checks: [
					{
						id: 'forwarding',
						name: 'Forwarding sysctls',
						status: broken ? 'fail' : 'pass',
						detail: broken ? 'IPv4 forwarding is off.' : 'Forwarding is on.'
					}
				]
			}
		})
	);
	await login(page);
	const banner = page.getByTestId('diagnostics-warning');
	await expect(banner).toContainText('IPv4 forwarding is off.');

	// Fixed at a terminal, which tells nobody; the next settings change makes the dashboard look
	// again. Client isolation is on by default, so turning it off and on again leaves things as
	// they were.
	broken = false;
	try {
		cli('server', 'set', '--client-isolation=false');
		await expect(banner).toHaveCount(0, { timeout: 5000 });
	} finally {
		cli('server', 'set', '--client-isolation=true');
	}
});

test('the dashboard says nothing about checks that cannot run', async ({ page }) => {
	await page.route('**/api/system/health', (route) =>
		route.fulfill({ status: 501, json: { error: "this daemon can't run the diagnostics" } })
	);
	const asked = page.waitForResponse(isHealth);
	await login(page);
	expect((await asked).status()).toBe(501);
	await expect(page.getByTestId('diagnostics-warning')).toHaveCount(0);
	await expect(page.getByRole('alert')).toHaveCount(0); // that is the System page's to say
});

/** A throwaway certificate for 127.0.0.1 and drawbridge.test, and its key, made by openssl. */
function makeCertificate(): { crt: string; key: string; fingerprint: string } {
	const dir = mkdtempSync(join(tmpdir(), 'drawbridge-cert-'));
	const crt = join(dir, 'fullchain.pem');
	const key = join(dir, 'privkey.pem');
	execFileSync(
		'openssl',
		['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes']
			.concat(['-keyout', key, '-out', crt, '-days', '90', '-subj', '/CN=drawbridge.test'])
			.concat(['-addext', 'subjectAltName=DNS:drawbridge.test,IP:127.0.0.1']),
		{ stdio: 'ignore' }
	);
	return { crt, key, fingerprint: new X509Certificate(readFileSync(crt)).fingerprint256 };
}

/** The SHA-256 fingerprint of the certificate the daemon presents to a new connection right now. */
function served(): Promise<string> {
	return new Promise((resolve, reject) => {
		const socket = connect({ host: '127.0.0.1', port, rejectUnauthorized: false }, () => {
			resolve(socket.getPeerCertificate().fingerprint256);
			socket.end();
		});
		socket.on('error', reject);
	});
}

test('the System page installs your own certificate, which the next connection is shown, and goes back', async ({
	page
}) => {
	const problems = watchConsole(page);
	const mine = makeCertificate();
	await login(page);
	await navigate(page, 'System');
	const section = page.getByRole('region', { name: 'Web UI Certificate' });
	const source = section.getByTestId('certificate-source');
	const fingerprint = section.getByTestId('certificate-fingerprint');
	await expect(source).toHaveText('Self-signed by Drawbridge');
	const self = (await fingerprint.innerText()).trim();
	expect(await served()).toBe(self);
	await expect(
		section.getByRole('button', { name: 'Use the Self-Signed Certificate' })
	).toHaveCount(0);

	// The fields tell a swap at once, before anything is sent.
	const certField = section.getByRole('textbox', { name: 'Certificate' });
	const keyField = section.getByRole('textbox', { name: 'Private key' });
	await certField.fill(readFileSync(mine.key, 'utf8'));
	await expect(section).toContainText('That is a private key');
	await certField.fill('');

	// The certificate from a file, the key pasted; a wrong password installs nothing.
	await section.getByLabel('Choose a certificate file').setInputFiles(mine.crt);
	await expect(certField).toHaveValue(/BEGIN CERTIFICATE/);
	await keyField.fill(readFileSync(mine.key, 'utf8'));
	await section.getByLabel('Your password').fill('not the password');
	await section.getByRole('button', { name: 'Install Certificate' }).click();
	await expect(section.getByRole('alert')).toContainText(/password/i);
	await expect(source).toHaveText('Self-signed by Drawbridge');
	expect(await served()).toBe(self);

	// The right one does, and a connection made now is shown it, with no restart.
	await section.getByLabel('Your password').fill(admin.password);
	await section.getByRole('button', { name: 'Install Certificate' }).click();
	await expect(section.getByRole('status').filter({ hasText: 'Installed.' })).toBeVisible();
	await expect(source).toHaveText('Installed by you');
	await expect(fingerprint).toHaveText(mine.fingerprint);
	await expect(section.getByTestId('certificate-details')).toContainText(
		'drawbridge.test, 127.0.0.1'
	);
	expect(await served()).toBe(mine.fingerprint);
	// Nothing secret stays in the page, and the certificate covers none of the host's names.
	await expect(certField).toHaveValue('');
	await expect(keyField).toHaveValue('');
	await expect(section.getByLabel('Your password')).toHaveValue('');
	await expect(section.getByTestId('certificate-note')).toContainText('covers none of the names');

	// The page still works over the new certificate, and the doctor and the log know about it.
	await page.reload();
	await expect(section.getByTestId('certificate-source')).toHaveText('Installed by you');
	await expect(
		page.getByRole('region', { name: 'Diagnostics' }).getByRole('listitem').last()
	).toContainText('uploaded certificate is valid until');
	const events = await (await page.request.get('/api/events?limit=50')).text();
	expect(events).toContain('"kind":"tls.certificate_installed"');
	expect(events).toContain('auth.certificate_failed');
	expect(events).not.toContain('PRIVATE KEY');

	// Going back.
	await section.getByRole('button', { name: 'Use the Self-Signed Certificate' }).click();
	await expect(
		section.getByRole('status').filter({ hasText: 'Back on the self-signed' })
	).toBeVisible();
	await expect(source).toHaveText('Self-signed by Drawbridge');
	await expect(fingerprint).toHaveText(self);
	expect(await served()).toBe(self);

	expect(problems).toEqual([]);
});

test('the command line installs and removes a certificate the same way', async ({ page }) => {
	const mine = makeCertificate();
	await login(page);
	const out = cli('tls', 'install', '--cert', mine.crt, '--key', mine.key);
	expect(out).toContain('now serves your certificate');
	expect(out).toContain(`SHA-256:     ${mine.fingerprint}`);
	expect(await served()).toBe(mine.fingerprint);
	try {
		await navigate(page, 'System');
		await expect(page.getByTestId('certificate-source')).toHaveText('Installed by you');
		expect(cli('tls', 'show')).toContain(mine.fingerprint);
	} finally {
		cli('tls', 'reset');
	}
	expect(cli('tls', 'show')).toContain('self-signed by Drawbridge');
	expect(await served()).not.toBe(mine.fingerprint);
});

test('the System page downloads a backup that restores, and asks for the password again', async ({
	page
}) => {
	const problems = watchConsole(page);
	await login(page);
	await navigate(page, 'System');
	const section = page.getByRole('region', { name: 'Backup', exact: true });
	const password = section.getByLabel('Your password', { exact: true });
	const passphrase = section.getByLabel('Passphrase for the file', { exact: true });
	const again = section.getByLabel('Passphrase again', { exact: true });
	const submit = section.getByRole('button', { name: 'Download Backup' });
	await expect(section.getByTestId('last-backup')).toContainText('No backup has been made yet.');

	let asked = 0;
	page.on('request', (r) => {
		if (r.url().endsWith('/api/system/backup')) asked++;
	});
	const secret = 'a passphrase to keep safe';

	// Typed twice differently, or too short: refused here, and nothing is sent.
	await password.fill(admin.password);
	await passphrase.fill(secret);
	await again.fill(secret + 'x');
	await submit.click();
	await expect(section.getByRole('alert')).toContainText("passphrases don't match");
	await passphrase.fill('short');
	await again.fill('short');
	await submit.click();
	expect(asked).toBe(0);

	// The wrong password gets no file, says so, and leaves what was typed for another try.
	await passphrase.fill(secret);
	await again.fill(secret);
	await password.fill('not the password');
	await submit.click();
	await expect(section.getByRole('alert')).toContainText('password is wrong');
	await expect(passphrase).toHaveValue(secret);
	expect(asked).toBe(1);

	// The right one downloads a file, and the page forgets all three secrets.
	await password.fill(admin.password);
	const downloaded = page.waitForEvent('download');
	await submit.click();
	const download = await downloaded;
	expect(download.suggestedFilename()).toMatch(/^drawbridge-\d{8}-\d{6}\.backup$/);
	await expect(section.getByRole('status')).toContainText(`Saved ${download.suggestedFilename()}`);
	await expect(password).toHaveValue('');
	await expect(passphrase).toHaveValue('');
	await expect(again).toHaveValue('');
	await expect(section.getByTestId('last-backup')).toContainText(
		/Last backup: \d+ \w+ \d\d:\d\d:\d\d/
	);

	// What the browser saved is the whole file, encrypted, and the real restore command opens it
	// with the passphrase and finds the daemon's own key in it. The daemon is left alone: the
	// restore goes to scratch files, and is told no control socket to look for.
	const file = await download.path();
	expect(statSync(file).size).toBeGreaterThan(1000);
	expect(readFileSync(file).subarray(0, 18).toString()).toBe('drawbridge-backup\n');
	const scratch = mkdtempSync(join(tmpdir(), 'drawbridge-restore-'));
	const restore = (pass: string) => {
		const passFile = join(scratch, 'passphrase');
		writeFileSync(passFile, pass, { mode: 0o600 });
		return cliOnFiles(
			'backup',
			'restore',
			file,
			'--db',
			join(scratch, 'drawbridge.db'),
			'--secret-key',
			join(scratch, 'secret.key'),
			'--owner',
			'none',
			'--passphrase-file',
			passFile,
			'--control',
			join(scratch, 'no-daemon.sock')
		);
	};
	expect(() => restore('not the passphrase')).toThrow(/wrong passphrase/);
	expect(restore(secret)).toContain('Restored the backup made');
	expect(
		readFileSync(join(scratch, 'secret.key')).equals(readFileSync(join(stateDir, 'secret.key')))
	).toBe(true);

	// It's in the log, with who made it, and neither secret is.
	const events = await (await page.request.get('/api/events?limit=200')).text();
	expect(events).toContain('"kind":"backup.created"');
	expect(events).toContain('auth.backup_failed');
	expect(events).not.toContain(secret);
	expect(events).not.toContain(admin.password);
	expect(problems).toEqual([]);
});

test('the System page says why a backup could not be made', async ({ page }) => {
	await page.route('**/api/system/backup', (route) =>
		route.fulfill({ status: 501, json: { error: "this daemon can't make backups" } })
	);
	await login(page);
	await page.goto('/system');
	const section = page.getByRole('region', { name: 'Backup', exact: true });
	await section.getByLabel('Your password', { exact: true }).fill(admin.password);
	await section.getByLabel('Passphrase for the file', { exact: true }).fill('a long enough one');
	await section.getByLabel('Passphrase again', { exact: true }).fill('a long enough one');
	await section.getByRole('button', { name: 'Download Backup' }).click();
	await expect(section.getByRole('alert')).toContainText("can't make backups");
});

test('the System page lists the snapshots the host keeps, and says what they are for', async ({
	page
}) => {
	const problems = watchConsole(page);
	await page.route('**/api/system/snapshots', (route) =>
		route.fulfill({
			json: {
				dir: '/var/lib/drawbridge/backups',
				nightly: true,
				snapshots: [
					{
						name: 'nightly-20261004-030000.db',
						kind: 'nightly',
						made_at: '2026-10-04T03:00:00Z',
						size: 2_500_000
					},
					{
						name: 'pre-migration-v4-20261002-101500.db',
						kind: 'pre-migration',
						schema: 4,
						made_at: '2026-10-02T10:15:00Z',
						size: 1_900_000
					}
				]
			}
		})
	);
	await login(page);
	await page.goto('/system');
	const section = page.getByRole('region', { name: 'Snapshots on this host' });
	const rows = section.getByRole('listitem');
	await expect(rows).toHaveCount(2);
	await expect(rows.first()).toContainText('Nightly');
	await expect(rows.first()).toContainText('nightly-20261004-030000.db');
	await expect(rows.first()).toContainText('2.5 MB');
	await expect(rows.last()).toContainText('Before an upgrade (database version 4)');
	await expect(section).toContainText('/var/lib/drawbridge/backups');
	await expect(section).toContainText('sudo drawbridge backup restore');
	// They can't be downloaded: no link or button on a snapshot.
	await expect(rows.getByRole('link')).toHaveCount(0);
	await expect(rows.getByRole('button')).toHaveCount(0);
	expect(problems).toEqual([]);
});

test('the System page says so when the nightly snapshot is off, or none are kept', async ({
	page
}) => {
	await login(page);
	await page.route('**/api/system/snapshots', (route) =>
		route.fulfill({ json: { dir: '/var/lib/drawbridge/backups', nightly: false, snapshots: [] } })
	);
	await page.goto('/system');
	const section = page.getByRole('region', { name: 'Snapshots on this host' });
	await expect(section).toContainText('The nightly snapshot is off');
	await expect(section).toContainText('None yet');

	await page.unroute('**/api/system/snapshots');
	await page.route('**/api/system/snapshots', (route) =>
		route.fulfill({ json: { dir: '', nightly: false, snapshots: [] } })
	);
	await page.goto('/system');
	await expect(section).toContainText('This daemon keeps no snapshots.');
	await expect(section).not.toContainText('sudo drawbridge');
});

test('the System page shows the snapshots of the real daemon', async ({ page }) => {
	const problems = watchConsole(page);
	await login(page);
	await page.goto('/system');
	const section = page.getByRole('region', { name: 'Snapshots on this host' });
	await expect(section).toContainText(/drawbridge-e2e\/backups|None yet|nightly-\d{8}-\d{6}\.db/);
	await expect(section.getByRole('alert')).toHaveCount(0);
	expect(problems).toEqual([]);
});
