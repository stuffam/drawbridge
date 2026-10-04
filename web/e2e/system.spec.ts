import { expect, test } from '@playwright/test';
import { mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { stateDir } from '../playwright.config';
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
