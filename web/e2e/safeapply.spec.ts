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
	await page.getByRole('button', { name: 'Save settings' }).click();
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
	await page.getByRole('button', { name: 'Save settings' }).click();
	await expect(page.getByText(/keep it or undo it first/)).toBeVisible();

	await bar(page).getByRole('button', { name: 'Keep changes' }).click();
	await expect(bar(page)).toBeHidden();
	expect(listenPort()).toBe(next);
	// Free to change again.
	expect(cli('server', 'show')).not.toContain('Waiting to be kept');
	expect(problems).toEqual([]);
});

test('Undo now puts the old settings back at once', async ({ page }) => {
	const problems = watchConsole(page);
	await login(page);
	const before = listenPort();
	const next = before === 51999 ? 51998 : 51999;

	await savePort(page, next);
	await expect(bar(page)).toBeVisible();
	await bar(page).getByRole('button', { name: 'Undo now' }).click();
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
	await bar(page).getByRole('button', { name: 'Keep changes' }).click();
	await expect(bar(page)).toBeHidden();
	expect(listenPort()).toBe(next);

	// Put it back without waiting: the CLI applies at once.
	cli('server', 'set', '--port', String(before));
	expect(listenPort()).toBe(before);
	expect(problems).toEqual([]);
});
