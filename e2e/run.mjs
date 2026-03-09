import fs from 'node:fs';
import path from 'node:path';
import { chromium } from 'playwright';

const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:18100';
const artifactDir = path.resolve('output/playwright/e2e');
fs.mkdirSync(artifactDir, { recursive: true });

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

async function screenshot(page, name) {
  await page.screenshot({ path: path.join(artifactDir, name), fullPage: true });
}

async function login(context, username, password, screenshotName) {
  const page = await context.newPage();
  await page.goto(`${baseURL}/admin`, { waitUntil: 'networkidle' });
  assert(page.url().endsWith('/admin/login'), `expected redirect to login, got ${page.url()}`);
  await page.getByLabel('Username').fill(username);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Enter Console' }).click();
  await page.waitForURL(`${baseURL}/admin`, { waitUntil: 'networkidle' });
  if (screenshotName) {
    await screenshot(page, screenshotName);
  }
  return page;
}

async function logout(page) {
  await page.getByRole('button', { name: 'Logout' }).click();
  await page.waitForURL(`${baseURL}/admin/login`, { waitUntil: 'networkidle' });
}

async function createUser(page, username, password) {
  await page.goto(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
  await page.getByLabel('Username').fill(username);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Create User' }).click();
  await page.waitForURL(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
  assert(await page.locator('body').textContent().then(t => t.includes(username)), `expected user ${username} to be listed`);
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  try {
    const adminContext = await browser.newContext();
    const adminPage = await login(adminContext, 'admin', 'AdminPass123!', '01-admin-dashboard.png');

    await adminPage.goto(`${baseURL}/admin/instances`, { waitUntil: 'networkidle' });
    await adminPage.getByLabel('Name').fill('Mock LS');
    await adminPage.getByLabel('Survey Base URL').fill('http://127.0.0.1:19080/surveys');
    await adminPage.getByLabel('RemoteControl URL').fill('http://mock-ls:19080/jsonrpc');
    await adminPage.getByLabel('RPC Transport').selectOption('jsonrpc');
    await adminPage.getByLabel('Username').fill('api-user');
    await adminPage.getByLabel('Secret Env Name').fill('LS6_RPC_PASSWORD');
    await adminPage.getByRole('button', { name: 'Create Instance' }).click();
    await adminPage.waitForURL(`${baseURL}/admin/instances`, { waitUntil: 'networkidle' });
    assert(await adminPage.locator('body').textContent().then(t => t.includes('Mock LS')), 'expected instance to be listed');
    await screenshot(adminPage, '02-admin-instances.png');

    await createUser(adminPage, 'alice', 'UserPassword123!');
    await createUser(adminPage, 'bob', 'UserPassword123!');
    await screenshot(adminPage, '03-admin-users.png');
    await logout(adminPage);

    const aliceContext = await browser.newContext();
    const alicePage = await login(aliceContext, 'alice', 'UserPassword123!', '04-alice-dashboard.png');
    await alicePage.goto(`${baseURL}/admin/routes`, { waitUntil: 'networkidle' });
    await alicePage.getByLabel('Name').fill('Alpha Route');
    await alicePage.getByLabel('Slug').fill('alpha-route');
    await alicePage.getByLabel('Description').fill('Owned by alice');
    await alicePage.getByLabel('Instance').selectOption({ label: 'Mock LS (jsonrpc)' });
    await alicePage.getByLabel('Survey IDs').fill('111\n222');
    await alicePage.getByLabel('Algorithm').selectOption('least_completed');
    await alicePage.getByRole('button', { name: 'Create Route' }).click();
    await alicePage.waitForURL(/\/admin\/routes\/\d+$/, { waitUntil: 'networkidle' });
    const routeMatch = alicePage.url().match(/\/admin\/routes\/(\d+)$/);
    assert(routeMatch, `could not extract route id from ${alicePage.url()}`);
    const routeID = routeMatch[1];
    await screenshot(alicePage, '05-alice-route-detail.png');

    let response = await alicePage.goto(`${baseURL}/admin/instances`, { waitUntil: 'networkidle' });
    assert(response && response.status() === 403, `expected alice instances access to be 403, got ${response ? response.status() : 'no response'}`);
    response = await alicePage.goto(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
    assert(response && response.status() === 403, `expected alice users access to be 403, got ${response ? response.status() : 'no response'}`);

    const simPage = await aliceContext.newPage();
    const simResponse = await simPage.goto(`${baseURL}/api/routes/${routeID}/simulate`, { waitUntil: 'networkidle' });
    assert(simResponse && simResponse.status() === 200, `expected simulation 200, got ${simResponse ? simResponse.status() : 'no response'}`);
    const simBody = await simPage.locator('body').textContent();
    for (const forbidden of ['secret_ref', 'remotecontrol', 'LS6_RPC_PASSWORD', 'api-user']) {
      assert(!simBody.includes(forbidden), `simulation response leaked ${forbidden}`);
    }
    await simPage.close();

    const bobContext = await browser.newContext();
    const bobPage = await login(bobContext, 'bob', 'UserPassword123!', '06-bob-dashboard.png');
    await bobPage.goto(`${baseURL}/admin/routes`, { waitUntil: 'networkidle' });
    const bobBody = await bobPage.locator('body').textContent();
    assert(!bobBody.includes('Alpha Route'), 'bob should not see alice route');
    response = await bobPage.goto(`${baseURL}/admin/routes/${routeID}`, { waitUntil: 'networkidle' });
    assert(response && response.status() === 403, `expected bob route detail access to be 403, got ${response ? response.status() : 'no response'}`);

    const publicContext = await browser.newContext();
    const publicPage = await publicContext.newPage();
    await publicPage.goto(`${baseURL}/r/alpha-route?token=abc123&src=mail`, { waitUntil: 'networkidle' });
    const finalURL = new URL(publicPage.url());
    assert(finalURL.host === '127.0.0.1:19080', `unexpected redirect host ${finalURL.host}`);
    assert(finalURL.pathname === '/surveys/222', `expected redirect to survey 222, got ${finalURL.pathname}`);
    assert(finalURL.searchParams.get('token') === 'abc123', 'token query param missing after redirect');
    assert(finalURL.searchParams.get('src') === 'mail', 'src query param missing after redirect');
    const publicBody = await publicPage.locator('body').textContent();
    assert(publicBody.includes('Mock Survey 222'), 'expected mock survey body after redirect');
    await screenshot(publicPage, '07-public-redirect.png');

    await alicePage.goto(`${baseURL}/admin/routes/${routeID}`, { waitUntil: 'networkidle' });
    const detailBody = await alicePage.locator('body').textContent();
    assert(detailBody.includes('redirected'), 'expected recent decision entry after public redirect');
    await screenshot(alicePage, '08-alice-route-detail-after-redirect.png');

    console.log('E2E OK');
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error(err.stack || String(err));
  process.exit(1);
});
