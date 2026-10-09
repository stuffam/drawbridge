import { expect, test } from '@playwright/test';
import { cli, login, navigate, watchConsole } from './helpers';

// These share one daemon with app.spec.ts (playwright.config.ts: workers: 1), and run after its
// setup test, so the admin account already exists. This file sorts after logs.spec.ts, which
// reads the whole event log, because it adds events.
test.describe.configure({ mode: 'serial' });

/** The client's public key, from the CLI: the web page and the daemon agree on it. */
function publicKey(name: string): string {
	const m = cli('client', 'show', name).match(/Public key:\s+(\S+)/);
	if (!m) throw new Error('no public key');
	return m[1];
}

/** The server's MTU now, and one that differs from it (an earlier spec changes the MTU too). */
function mtus(): { now: string; other: string } {
	const m = cli('server', 'show').match(/MTU:\s+(\d+)/);
	if (!m) throw new Error('no MTU');
	return { now: m[1], other: m[1] === '1400' ? '1390' : '1400' };
}

test('a config goes out of date when the server changes, and handing it out again clears it', async ({
	page
}) => {
	const problems = watchConsole(page);
	const mtu = mtus();
	cli('client', 'add', 'Stale Probe');
	await login(page);
	await navigate(page, 'Clients');
	await page.getByRole('link', { name: 'Stale Probe' }).click();
	await expect(page.getByRole('heading', { name: 'Stale Probe' })).toBeVisible();

	// Nothing has been handed out, so there's nothing to be out of date.
	await expect(page.getByText('Config handed out')).toBeVisible();
	await expect(page.getByText('No record')).toBeVisible();
	await expect(page.getByText('Config outdated')).toHaveCount(0);

	await page.getByRole('button', { name: 'Show QR Code' }).click();
	await expect(page.getByRole('img', { name: /QR code/ })).toBeVisible();
	await expect(page.getByText('No record')).toHaveCount(0);
	await expect(page.getByText('Config outdated')).toHaveCount(0);

	// The admin changes the server from the CLI. The page hears about it.
	cli('server', 'set', '--mtu', mtu.other);
	await expect(page.getByTestId('config-outdated')).toBeVisible({ timeout: 10_000 });
	await expect(page.getByText('Config outdated', { exact: true })).toBeVisible();

	// The dashboard counts it, and the count opens the list of the clients to fix.
	await page.getByRole('link', { name: 'Drawbridge' }).click();
	const tile = page.getByRole('button', { name: 'View clients with an outdated config' });
	await expect(tile).toContainText('1', { timeout: 10_000 });
	await tile.click();
	await expect(page).toHaveURL(/\/clients\?state=outdated$/);
	const row = page.getByRole('listitem').filter({ hasText: 'Stale Probe' });
	await expect(row).toContainText('Config outdated');
	await page.getByRole('button', { name: 'Clear the config outdated filter' }).click();
	await expect(page.getByRole('link', { name: 'Stale Probe' })).toBeVisible();

	// Showing the QR code again hands out the current config, and the flag goes.
	await page.getByRole('link', { name: 'Stale Probe' }).click();
	await expect(page.getByTestId('config-outdated')).toBeVisible();
	await page.getByRole('button', { name: 'Show QR Code' }).click();
	await expect(page.getByTestId('config-outdated')).toHaveCount(0, { timeout: 10_000 });
	await expect(page.getByText('Config outdated')).toHaveCount(0);

	// Put the server back as it was, and the list has nothing outdated to show.
	cli('server', 'set', '--mtu', mtu.now);
	await expect(page.getByTestId('config-outdated')).toBeVisible({ timeout: 10_000 });
	await page.getByRole('button', { name: 'Show QR Code' }).click();
	await expect(page.getByTestId('config-outdated')).toHaveCount(0, { timeout: 10_000 });
	await page.goto('/clients?state=outdated');
	await expect(page.getByText("No client's config is outdated.")).toBeVisible();

	cli('client', 'delete', '--yes', 'Stale Probe');
	expect(problems).toEqual([]);
});

test("rotating a client's keys asks first, cuts the old keys off, and shows the new QR code", async ({
	page
}) => {
	const problems = watchConsole(page);
	cli('client', 'add', 'Rotate Probe');
	const before = publicKey('Rotate Probe');
	await login(page);
	await navigate(page, 'Clients');
	await page.getByRole('link', { name: 'Rotate Probe' }).click();
	await expect(page.getByRole('heading', { name: 'Rotate Probe' })).toBeVisible();

	// Cancel changes nothing.
	await page.getByRole('button', { name: 'Rotate Keys' }).click();
	const dialog = page.getByRole('dialog', { name: 'Rotate Keys' });
	await expect(dialog).toContainText('stops working at once');
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(dialog).toBeHidden();
	expect(publicKey('Rotate Probe')).toBe(before);

	await page.getByRole('button', { name: 'Rotate Keys' }).click();
	await dialog.getByRole('button', { name: 'Rotate Keys' }).click();
	await expect(dialog).toBeHidden();
	await expect(page.getByText('New keys are in place.')).toBeVisible();
	// The new config's QR code is on screen, for the device that has to import it.
	await expect(page.getByRole('img', { name: /QR code/ })).toBeVisible();
	const after = publicKey('Rotate Probe');
	expect(after).not.toBe(before);
	// (The activity list also shows the new key, in the event's details, hence exact.)
	await expect(page.getByText(after, { exact: true })).toBeVisible();
	await expect(page.getByText(before)).toHaveCount(0);
	// Showing the QR code handed it out, so the new config isn't outdated.
	await expect(page.getByText('Config outdated')).toHaveCount(0);

	// The log says who rotated what.
	await expect(page.getByText("Rotated a client's keys")).toBeVisible();

	// The CLI sees the same client, and the config it prints has the new key pair.
	expect(cli('client', 'config', 'Rotate Probe')).toContain('PrivateKey = ');
	expect(cli('client', 'show', 'Rotate Probe')).toContain(after);

	cli('client', 'delete', '--yes', 'Rotate Probe');
	expect(problems).toEqual([]);
});

test('a rotation by the CLI marks a config that was handed out as outdated', async ({ page }) => {
	const problems = watchConsole(page);
	cli('client', 'add', 'CLI Rotate');
	cli('client', 'config', 'CLI Rotate');
	await login(page);
	await navigate(page, 'Clients');
	const row = page.getByRole('listitem').filter({ hasText: 'CLI Rotate' });
	await expect(row).toBeVisible();
	await expect(row).not.toContainText('Config outdated');
	cli('client', 'rotate-keys', '--yes', 'CLI Rotate');
	await expect(row).toContainText('Config outdated', { timeout: 10_000 });
	cli('client', 'delete', '--yes', 'CLI Rotate');
	await expect(row).toHaveCount(0, { timeout: 10_000 });
	expect(problems).toEqual([]);
});
