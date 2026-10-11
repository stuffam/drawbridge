import { expect, test } from '@playwright/test';
import { login } from './helpers';

// What a page that is already open does when the daemon under it has been upgraded. SvelteKit asks
// for _app/version.json once a minute (`kit.version.pollInterval`), and the name in it is the commit
// the build was made from (web/vite.config.ts), so a newer build has a different one. The fake
// clock moves past the minute without waiting for it.
const versionFile = '**/_app/version.json';
const minuteAndAHalf = 90_000;

const bar = (page: import('@playwright/test').Page) => page.getByTestId('new-version');

test('a bar offers a reload when the server has a newer build, and Reload brings it', async ({
	page
}) => {
	await page.clock.install();
	await login(page);
	await page.evaluate(() => {
		(window as unknown as { marker?: string }).marker = 'the page before the reload';
	});
	await expect(bar(page)).toHaveCount(0);

	// The server's build is another one now, as it is after an upgrade.
	await page.route(versionFile, (route) => route.fulfill({ json: { version: 'a-newer-build' } }));
	await page.clock.fastForward(minuteAndAHalf);
	await expect(bar(page)).toBeVisible();
	await expect(bar(page)).toContainText('A newer version of Drawbridge is available');

	// Reload goes to the new page, which doesn't have what the old one had in memory.
	await Promise.all([
		page.waitForEvent('load'),
		page.getByRole('button', { name: 'Reload' }).click()
	]);
	expect(
		await page.evaluate(() => (window as unknown as { marker?: string }).marker)
	).toBeUndefined();
});

test('there is no bar while the server is the build the page came from', async ({ page }) => {
	await page.clock.install();
	await login(page);
	// The page does ask: one answer arrives, and it's the same build.
	const answered = page.waitForResponse(versionFile);
	await page.clock.fastForward(minuteAndAHalf);
	expect((await answered).ok()).toBe(true);
	await expect(bar(page)).toHaveCount(0);
});

test('the login page offers it too', async ({ page }) => {
	await page.clock.install();
	await page.route(versionFile, (route) => route.fulfill({ json: { version: 'a-newer-build' } }));
	await page.goto('/login');
	await expect(page.getByRole('button', { name: 'Log In' })).toBeVisible();
	await page.clock.fastForward(minuteAndAHalf);
	await expect(bar(page)).toBeVisible();
});

// WCAG contrast of what the bar says, and of its button, against what each is painted on.
test('the bar is readable in light and dark', async ({ page }) => {
	await page.clock.install();
	await page.route(versionFile, (route) => route.fulfill({ json: { version: 'a-newer-build' } }));
	await page.goto('/login');
	for (const mode of ['light', 'dark']) {
		await page.evaluate((m) => localStorage.setItem('drawbridge-theme', m), mode);
		await page.reload();
		await expect(page.getByRole('button', { name: 'Log In' })).toBeVisible();
		await page.clock.fastForward(minuteAndAHalf);
		await expect(bar(page)).toBeVisible();

		const ratios = await bar(page).evaluate((el) => {
			// Any CSS color (Tailwind's are oklch) to a luminance, by painting it.
			const lum = (css: string) => {
				const ctx = document.createElement('canvas').getContext('2d')!;
				ctx.fillStyle = css;
				ctx.fillRect(0, 0, 1, 1);
				const [r, g, b] = [...ctx.getImageData(0, 0, 1, 1).data.slice(0, 3)].map((v) => {
					const c = v / 255;
					return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
				});
				return 0.2126 * r + 0.7152 * g + 0.0722 * b;
			};
			const ratio = (fg: string, bg: string) => {
				const [hi, lo] = [lum(fg), lum(bg)].sort((a, b) => b - a);
				return (hi + 0.05) / (lo + 0.05);
			};
			const text = el.querySelector('p')!;
			const button = el.querySelector('button')!;
			return {
				text: ratio(getComputedStyle(text).color, getComputedStyle(el).backgroundColor),
				button: ratio(getComputedStyle(button).color, getComputedStyle(button).backgroundColor)
			};
		});
		expect(ratios.text, `the message in ${mode} mode`).toBeGreaterThan(4.5);
		expect(ratios.button, `the button in ${mode} mode`).toBeGreaterThan(4.5);
	}
});
