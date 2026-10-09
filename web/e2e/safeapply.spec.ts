import { expect, test, type Page } from '@playwright/test';
import { cli, login, navigate, watchConsole } from './helpers';

// These share one daemon with app.spec.ts (playwright.config.ts: workers: 1), and run after its
// setup test, so the admin account already exists. The daemon runs with --safe-apply-window 10s
// (serve.sh), so a change that isn't kept is undone in ten seconds, not a minute.
test.describe.configure({ mode: 'serial' });

/** The server's listen port now, from the CLI. */
function listenPort(): number {
	const m = cli('server', 'show').match(/Listen port:\s+(\d+)/);
	if (!m) throw new Error('no listen port');
	return Number(m[1]);
}

/** Changes the listen port in Settings, and saves. */
async function savePort(page: Page, port: number) {
	await page.goto('/settings');
	await page.getByLabel('Listen port (UDP)').fill(String(port));
	await page.getByRole('button', { name: 'Save Settings' }).click();
}

const bar = (page: Page) => page.getByTestId('pending-change');

test('a change that could lock the admin out waits to be kept, on every page, until it is', async ({
	page
}) => {
	const problems = watchConsole(page);
	await login(page);
	const before = listenPort();
	const next = before === 51999 ? 51998 : 51999;

	await savePort(page, next);
	await expect(page.getByText(/could lock you out, so it is undone in \d+ seconds/)).toBeVisible();
	await expect(bar(page)).toBeVisible();
	await expect(bar(page)).toContainText(`Listen port: ${before} → ${next}`);
	await expect(bar(page)).toContainText('Made by admin (web)');
	await expect(page.getByTestId('pending-countdown')).toContainText(
		/Undone in \d+ s unless you keep it/
	);
	// It's applied: the CLI sees the new port already.
	expect(listenPort()).toBe(next);

	// The bar follows the admin to other pages, and anything else about the settings is refused.
	await navigate(page, 'Clients');
	await expect(bar(page)).toBeVisible();
	await navigate(page, 'Server Settings');
	await page.getByLabel('MTU').fill('1400');
	await page.getByRole('button', { name: 'Save Settings' }).click();
	await expect(page.getByText(/keep it or undo it first/)).toBeVisible();

	await bar(page).getByRole('button', { name: 'Keep Changes' }).click();
	await expect(bar(page)).toBeHidden();
	expect(listenPort()).toBe(next);
	// Free to change again.
	expect(cli('server', 'show')).not.toContain('Waiting to be kept');
	expect(problems).toEqual([]);
});

test('Undo Now puts the old settings back at once', async ({ page }) => {
	const problems = watchConsole(page);
	await login(page);
	const before = listenPort();
	const next = before === 51999 ? 51998 : 51999;

	await savePort(page, next);
	await expect(bar(page)).toBeVisible();
	await bar(page).getByRole('button', { name: 'Undo Now' }).click();
	await expect(bar(page)).toBeHidden();
	expect(listenPort()).toBe(before);
	// The form shows the settings that are back.
	await expect(page.getByLabel('Listen port (UDP)')).toHaveValue(String(before));
	expect(problems).toEqual([]);
});

test('a change nobody keeps is undone when the time is up', async ({ page }) => {
	const problems = watchConsole(page);
	await login(page);
	const before = listenPort();
	const next = before === 51999 ? 51998 : 51999;

	await savePort(page, next);
	await expect(bar(page)).toBeVisible();
	expect(listenPort()).toBe(next);

	// Ten seconds, and the daemon undoes it by itself; the page notices and says so.
	await expect(bar(page)).toBeHidden({ timeout: 25_000 });
	expect(listenPort()).toBe(before);
	await expect(page.getByLabel('Listen port (UDP)')).toHaveValue(String(before));
	expect(cli('events', '--limit', '5')).toContain('server.settings_expired');
	expect(problems).toEqual([]);
});

test('a change made with the CLI waits for the web UI too', async ({ page }) => {
	const problems = watchConsole(page);
	await login(page);
	const before = listenPort();
	const next = before === 51999 ? 51998 : 51999;

	expect(cli('server', 'set', '--port', String(next), '--safe')).toContain('Waiting to be kept');
	await expect(bar(page)).toBeVisible({ timeout: 10_000 });
	// Whoever runs the tests is the CLI's account: root in a container, another user on CI.
	await expect(bar(page)).toContainText(/Made by \S+ \(cli\)/);
	await bar(page).getByRole('button', { name: 'Keep Changes' }).click();
	await expect(bar(page)).toBeHidden();
	expect(listenPort()).toBe(next);

	// Put it back without waiting: the CLI applies at once.
	cli('server', 'set', '--port', String(before));
	expect(listenPort()).toBe(before);
	expect(problems).toEqual([]);
});

/** The server's public key now, from the CLI. */
function serverKey(): string {
	const m = cli('server', 'show').match(/Public key:\s+(\S+)/);
	if (!m) throw new Error('no public key');
	return m[1];
}

test("rotating the server's key asks first, waits to be kept, and Undo Now brings the old key back", async ({
	page
}) => {
	const problems = watchConsole(page);
	await login(page);
	await page.goto('/settings');
	const shown = page.getByTestId('server-public-key');
	const before = serverKey();
	await expect(shown).toHaveText(before);

	// Cancel changes nothing.
	await page.getByRole('button', { name: 'Rotate the Key…' }).click();
	const dialog = page.getByRole('dialog', { name: 'Rotate the Server Key' });
	await expect(dialog).toContainText('Every client stops working');
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(dialog).toBeHidden();
	expect(serverKey()).toBe(before);

	await page.getByRole('button', { name: 'Rotate the Key…' }).click();
	await dialog.getByRole('button', { name: 'Rotate the Key' }).click();
	await expect(dialog).toBeHidden();
	const after = serverKey();
	expect(after).not.toBe(before);
	await expect(shown).toHaveText(after);
	await expect(page.getByText(/could lock you out, so it is undone in \d+ seconds/)).toBeVisible();
	// The bar shows both keys, so the admin can tell what is waiting.
	await expect(bar(page)).toContainText(`Server public key: ${before} → ${after}`);

	await bar(page).getByRole('button', { name: 'Undo Now' }).click();
	await expect(bar(page)).toBeHidden();
	expect(serverKey()).toBe(before);
	await expect(shown).toHaveText(before);
	await expect(page.getByText(/could lock you out/)).toHaveCount(0);
	expect(problems).toEqual([]);
});

test("a kept rotation of the server's key flags the clients that were handed a config", async ({
	page
}) => {
	const problems = watchConsole(page);
	cli('client', 'add', 'Key Probe');
	cli('client', 'config', 'Key Probe'); // hands its config out
	await login(page);
	await navigate(page, 'Clients');
	const row = page.getByRole('listitem').filter({ hasText: 'Key Probe' });
	await expect(row).toBeVisible();
	await expect(row).not.toContainText('Config outdated');

	const before = serverKey();
	await page.goto('/settings');
	await page.getByRole('button', { name: 'Rotate the Key…' }).click();
	await page
		.getByRole('dialog', { name: 'Rotate the Server Key' })
		.getByRole('button', { name: 'Rotate the Key' })
		.click();
	await expect(bar(page)).toBeVisible();
	await bar(page).getByRole('button', { name: 'Keep Changes' }).click();
	await expect(bar(page)).toBeHidden();
	const after = serverKey();
	expect(after).not.toBe(before);
	await expect(page.getByTestId('server-public-key')).toHaveText(after);

	// The client's config names the old key, so it's flagged until it's handed out again.
	await navigate(page, 'Clients');
	await expect(row).toContainText('Config outdated');
	expect(cli('client', 'config', 'Key Probe')).toContain(`PublicKey = ${after}`);
	await expect(row).not.toContainText('Config outdated', { timeout: 10_000 });

	// The log says it, with both keys.
	expect(cli('events', '--limit', '10')).toContain('server.key_rotated');
	cli('client', 'delete', '--yes', 'Key Probe');
	expect(problems).toEqual([]);
});
