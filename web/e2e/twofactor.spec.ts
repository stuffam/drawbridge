import { expect, test, type Page } from '@playwright/test';
import { cli, admin, login, logOut, totpCode, watchConsole } from './helpers';

// The tests share one daemon with app.spec.ts (playwright.config.ts: workers: 1), and run after
// its setup test, so the admin account already exists. This file sorts last, and each test leaves
// two-factor authentication off, because a login in a spec that ran after would need a code.
test.describe.configure({ mode: 'serial' });

const section = (page: Page) => page.getByRole('region', { name: 'Two-Factor Authentication' });

/** Turns 2FA on through the Account page, and returns the secret and the recovery codes. */
async function turnOn(page: Page) {
	await page.goto('/account');
	const box = section(page);
	await box.getByRole('button', { name: 'Turn On…' }).click();
	await box.getByLabel('Your password').fill(admin.password);
	await box.getByRole('button', { name: 'Continue' }).click();
	const secret = (await box.getByTestId('totp-secret').innerText()).replace(/\s+/g, '');
	expect(secret).toMatch(/^[A-Z2-7]{32}$/);
	await box.getByLabel('Code from the app').fill(totpCode(secret));
	await box.getByRole('button', { name: 'Turn On', exact: true }).click();
	const recovery = box.getByTestId('recovery-codes');
	await expect(recovery).toContainText("They won't be shown again");
	const codes = await recovery.getByRole('listitem').allInnerTexts();
	expect(codes).toHaveLength(10);
	for (const c of codes) expect(c).toMatch(/^[A-Z2-9]{5}-[A-Z2-9]{5}-[A-Z2-9]{5}$/);
	await recovery.getByRole('button', { name: "I've saved them" }).click();
	await expect(recovery).toHaveCount(0);
	return { secret, codes };
}

/** The second step of the login page, after the password. */
async function passwordStep(page: Page) {
	await page.goto('/login');
	await page.getByLabel('Username').fill(admin.username);
	await page.getByLabel('Password').fill(admin.password);
	await page.getByRole('button', { name: 'Log In' }).click();
	await expect(page.getByLabel('Authentication code')).toBeVisible();
}

test('turn on 2FA, log in with a code, make new recovery codes, and turn it off', async ({
	page
}) => {
	const problems = watchConsole(page);
	await login(page);
	await page.goto('/account');
	const box = section(page);
	await expect(box).toContainText('Turn On…');

	// It takes the password again, and a wrong one starts nothing.
	await box.getByRole('button', { name: 'Turn On…' }).click();
	await box.getByLabel('Your password').fill('not the password');
	await box.getByRole('button', { name: 'Continue' }).click();
	await expect(box.getByRole('alert')).toContainText('current password is wrong');
	await expect(box.getByTestId('totp-secret')).toHaveCount(0);
	await box.getByRole('button', { name: 'Cancel' }).click();

	// The first code proves the app. A wrong one leaves it off.
	await box.getByRole('button', { name: 'Turn On…' }).click();
	await box.getByLabel('Your password').fill(admin.password);
	await box.getByRole('button', { name: 'Continue' }).click();
	const secret = (await box.getByTestId('totp-secret').innerText()).replace(/\s+/g, '');
	await expect(box.getByRole('img', { name: /QR code/ })).toBeVisible();
	await box.getByLabel('Code from the app').fill(totpCode(secret, 5));
	await box.getByRole('button', { name: 'Turn On', exact: true }).click();
	await expect(box.getByRole('alert')).toContainText('wrong');
	await expect(box.getByTestId('recovery-codes')).toHaveCount(0);
	await box.getByLabel('Code from the app').fill(totpCode(secret));
	await box.getByRole('button', { name: 'Turn On', exact: true }).click();
	const recovery = box.getByTestId('recovery-codes');
	await expect(recovery).toContainText("They won't be shown again");
	const codes = await recovery.getByRole('listitem').allInnerTexts();
	expect(codes).toHaveLength(10);
	await recovery.getByRole('button', { name: "I've saved them" }).click();
	await expect(box).toContainText('On.');
	await expect(box.getByTestId('recovery-left')).toContainText('10 recovery codes left');

	// Logging in asks for the code after the password, and wrong ones don't get in.
	await logOut(page);
	await passwordStep(page);
	await page.getByLabel('Authentication code').fill(totpCode(secret, 5));
	await page.getByRole('button', { name: 'Log In' }).click();
	await expect(page.getByRole('alert')).toContainText('wrong');
	await expect(page.getByLabel('Authentication code')).toHaveValue('');
	// The next step's code: the one that proved the app is spent.
	await page.getByLabel('Authentication code').fill(totpCode(secret, 1));
	await page.getByRole('button', { name: 'Log In' }).click();
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();

	// A recovery code works once. New ones void the old.
	await page.goto('/account');
	await box.getByRole('button', { name: 'New Recovery Codes…' }).click();
	await box.getByLabel('Your password').fill(admin.password);
	await box.getByLabel('Code', { exact: true }).fill(codes[0]);
	await box.getByRole('button', { name: 'Make New Codes' }).click();
	const renewed = box.getByTestId('recovery-codes');
	await expect(renewed).toContainText("They won't be shown again");
	const fresh = await renewed.getByRole('listitem').allInnerTexts();
	expect(fresh).toHaveLength(10);
	expect(fresh).not.toContain(codes[1]);
	await box.getByRole('button', { name: "I've saved them" }).click();
	await expect(box.getByTestId('recovery-left')).toContainText('10 recovery codes left');

	await logOut(page);
	await passwordStep(page);
	await page.getByLabel('Authentication code').fill(codes[1]);
	await page.getByRole('button', { name: 'Log In' }).click();
	await expect(page.getByRole('alert')).toContainText('wrong');
	await page.getByLabel('Authentication code').fill(fresh[0]);
	await page.getByRole('button', { name: 'Log In' }).click();
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();

	// Turning it off takes the password and a code, and the log says all of it.
	await page.goto('/account');
	await box.getByRole('button', { name: 'Turn Off…' }).click();
	await box.getByLabel('Your password').fill(admin.password);
	await box.getByLabel('Code', { exact: true }).fill('000000');
	await box.getByRole('button', { name: 'Turn Off', exact: true }).click();
	await expect(box.getByRole('alert')).toContainText('wrong');
	await box.getByLabel('Code', { exact: true }).fill(fresh[1]);
	await box.getByRole('button', { name: 'Turn Off', exact: true }).click();
	await expect(box).toContainText('Two-factor authentication is off');
	await expect(box).toContainText('Turn On…');

	await logOut(page);
	await login(page);
	const events = cli('events', '--limit', '50');
	for (const kind of [
		'auth.totp_enabled',
		'auth.totp_disabled',
		'auth.totp_failed',
		'auth.recovery_code_used',
		'auth.recovery_codes_renewed'
	]) {
		expect(events).toContain(kind);
	}
	// What the log may say about the second factor is that it happened, never what it was.
	for (const secretText of [secret, ...codes, ...fresh]) expect(events).not.toContain(secretText);
	expect(problems).toEqual([]);
});

test('an admin who lost the app and the codes gets back in from the command line', async ({
	page,
	browser,
	baseURL
}) => {
	await login(page);
	const { codes } = await turnOn(page);

	// Another browser is logged in with a recovery code, and is out after the command.
	const other = await browser.newPage({ ignoreHTTPSErrors: true, baseURL });
	try {
		await passwordStep(other);
		await other.getByLabel('Authentication code').fill(codes[0]);
		await other.getByRole('button', { name: 'Log In' }).click();
		await expect(other.getByRole('heading', { name: 'Dashboard' })).toBeVisible();

		expect(cli('admin', 'disable-2fa')).toContain('is off for "admin"');
		expect(cli('admin', 'disable-2fa')).toContain('was already off');

		await other.reload();
		await expect(other.getByRole('button', { name: 'Log In' })).toBeVisible();
	} finally {
		await other.close();
	}

	// The password alone is enough again.
	await login(page);
	await page.goto('/account');
	await expect(section(page)).toContainText('Turn On…');
});
