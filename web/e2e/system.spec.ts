import { expect, test } from '@playwright/test';
import { login, navigate, watchConsole } from './helpers';

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
