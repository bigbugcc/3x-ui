// Real UI + controllers integration, launched by TestPasskeyBrowser. No user DB
// or full panel process is used. Chromium's virtual authenticator handles CTAP.
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';

const [baseURL, artifactDir] = process.argv.slice(2);
if (!baseURL || new URL(baseURL).hostname !== 'localhost') throw new Error('A localhost test server is required');
await mkdir(artifactDir, { recursive: true });
const browser = await chromium.launch({ headless: true });
let page;
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, locale: 'en-US', viewport: { width: 1280, height: 1000 } });
  page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on('pageerror', (error) => console.log('Page error:', error.message));
  page.on('requestfailed', (request) => console.log('Failed request:', request.url(), request.failure()?.errorText));
  await page.route('**/sponsors', (route) => route.fulfill({ json: { success: true, obj: [] } }));
  // The controller harness covers authentication/settings, not PWA deployment.
  await page.route('**/pwa-register.js', (route) => route.fulfill({ contentType: 'application/javascript', body: '' }));
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
  const passwordLogin = async () => {
    await page.goto(baseURL);
    await page.getByPlaceholder('Username', { exact: true }).fill('admin');
    await page.getByPlaceholder('Password', { exact: true }).fill('passkey-test-password');
    await page.getByRole('button', { name: 'Log In', exact: true }).click();
    await page.waitForURL(baseURL + 'panel/');
  };
  const settings = async () => {
    await page.goto(baseURL + 'panel/settings#security');
    await page.getByRole('tab', { name: /Passkeys/ }).click();
    await page.getByPlaceholder('panel.example.com', { exact: true }).waitFor();
  };
  const api = (path, body) => page.evaluate(async ({ path, body }) => {
    const base = window.X_UI_BASE_PATH;
    const token = await (await fetch(base + 'csrf-token')).json();
    return (await fetch(base + path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token.obj, 'X-Requested-With': 'XMLHttpRequest' }, body: JSON.stringify(body) })).json();
  }, { path, body });

  await passwordLogin(); await settings();
  assert.equal(await page.getByPlaceholder('panel.example.com', { exact: true }).inputValue(), '');
  await page.getByRole('switch').first().click();
  await page.getByPlaceholder('panel.example.com', { exact: true }).fill('localhost');
  await page.getByPlaceholder('https://panel.example.com:8443').fill(new URL(baseURL).origin);
  // The harness uses TLS but has no persisted production certificate path.
  await page.locator('.ant-select').click();
  await page.getByText('HTTPS at a reverse proxy', { exact: true }).click();
  await page.getByRole('button', { name: 'Save Passkey configuration', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.locator('input[type="password"]').fill('temporary-secret');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  assert.equal(await page.getByPlaceholder('panel.example.com', { exact: true }).inputValue(), 'localhost');
  await page.getByRole('button', { name: 'Save Passkey configuration', exact: true }).click();
  assert.equal(await dialog.locator('input[type="password"]').inputValue(), '');
  await dialog.locator('input[type="password"]').fill('passkey-test-password');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await page.waitForURL(baseURL);
  await passwordLogin(); await settings();
  await page.getByRole('button', { name: 'Add Passkey', exact: true }).click();
  await dialog.getByRole('textbox').first().fill('Chromium virtual device');
  await dialog.locator('input[type="password"]').fill('passkey-test-password');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await page.getByText('Chromium virtual device', { exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: 'Restart Panel', exact: true }).count(), 0);
  const credentials = (await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials;
  assert.equal(credentials.length, 1); assert.equal(credentials[0].isResidentCredential, true); assert.equal(credentials[0].rpId, 'localhost');
  await page.screenshot({ path: artifactDir + '/settings-desktop.png', fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: artifactDir + '/settings-mobile.png', fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 1000 });
  assert.equal((await api('logout', {})).success, true);
  await page.goto(baseURL);
  await page.getByRole('button', { name: /Sign in with a Passkey/ }).click();
  await page.waitForURL(baseURL + 'panel/');
  await settings();
  await page.getByText('Chromium virtual device', { exact: true }).waitFor();
  await page.getByRole('button', { name: 'Rename', exact: true }).click();
  await dialog.getByRole('textbox').fill('Renamed device');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await page.getByText('Renamed device', { exact: true }).waitFor();
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  await dialog.locator('input[type="password"]').fill('passkey-test-password');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await page.waitForURL(baseURL);
  await passwordLogin(); await settings();
  assert.equal(await page.getByText('Renamed device', { exact: true }).count(), 0);
  await context.addCookies([{ name: 'lang', value: 'zh-CN', url: new URL(baseURL).origin }]);
  await page.reload();
  await page.getByRole('tab', { name: /通行密钥/ }).click();
  await page.getByPlaceholder('panel.example.com', { exact: true }).waitFor();
  await page.getByRole('button', { name: '添加通行密钥', exact: true }).waitFor();
  await page.waitForFunction(() => Array.from(document.querySelectorAll('button')).some((button) => button.textContent?.includes('添加通行密钥') && !button.disabled));
  await page.screenshot({ path: artifactDir + '/settings-desktop-zh-CN.png', fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: artifactDir + '/settings-mobile-zh-CN.png', fullPage: true, animations: 'disabled' });
  console.log('PASS: configuration UI, reauthentication, resident registration, Passkey login, rename, revocation and session invalidation; desktop/mobile screenshots saved');
} catch (error) {
  if (page) { await page.screenshot({ path: artifactDir + '/failure.png', fullPage: true }); console.log('URL:', page.url(), '\nRendered page:', await page.locator('body').innerText()); }
  throw error;
} finally { await browser.close(); }
