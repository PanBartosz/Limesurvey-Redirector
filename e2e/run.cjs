const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(path.resolve(__dirname, '.runner/node_modules/playwright'));

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
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Enter Console' }).click();
  await page.waitForURL(`${baseURL}/admin`, { waitUntil: 'networkidle' });
  if (screenshotName) {
    await screenshot(page, screenshotName);
  }
  return page;
}

async function expectLoginFailure(context, username, password) {
  const page = await context.newPage();
  await page.goto(`${baseURL}/admin/login`, { waitUntil: 'networkidle' });
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Enter Console' }).click();
  await page.waitForLoadState('networkidle');
  assert(page.url().endsWith('/admin/login'), `expected failed login to stay on login, got ${page.url()}`);
  await page.close();
}

async function logout(page) {
  await page.getByRole('button', { name: 'Logout' }).click();
  await page.waitForURL(`${baseURL}/admin/login`, { waitUntil: 'networkidle' });
}

async function createUser(page, username, password) {
  await page.goto(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Create User' }).click();
  await page.waitForURL(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
  assert(await page.locator('body').textContent().then(t => t.includes(username)), `expected user ${username} to be listed`);
}

async function fillTargetRow(page, index, surveyID, weight) {
  await page.locator('input[name="target_survey_id"]').nth(index).fill(String(surveyID));
  await page.locator('input[name="target_weight"]').nth(index).fill(String(weight));
}

async function main() {
  const browser = await chromium.launch({ headless: true, channel: 'chrome' });
  try {
    const adminContext = await browser.newContext();
    const adminPage = await login(adminContext, 'admin', 'AdminPass123!', '01-admin-dashboard.png');

    await adminPage.goto(`${baseURL}/admin/instances`, { waitUntil: 'networkidle' });
    await adminPage.getByRole('textbox', { name: 'Name', exact: true }).fill('Mock LS');
    await adminPage.getByRole('textbox', { name: 'Survey Base URL', exact: true }).fill('http://127.0.0.1:19080/surveys');
    await adminPage.getByRole('textbox', { name: 'RemoteControl URL', exact: true }).fill('http://mock-ls:19080/jsonrpc');
    await adminPage.getByRole('combobox', { name: 'RPC Transport' }).selectOption('jsonrpc');
    await adminPage.getByRole('textbox', { name: 'Username', exact: true }).fill('api-user');
    await adminPage.getByRole('textbox', { name: 'Secret Env Name', exact: true }).fill('LS6_RPC_PASSWORD');
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
    await alicePage.getByRole('textbox', { name: 'Name', exact: true }).fill('Alpha Route');
    await alicePage.getByRole('textbox', { name: 'Slug', exact: true }).fill('alpha-route');
    await alicePage.getByRole('textbox', { name: 'Description', exact: true }).fill('Owned by alice');
    await alicePage.getByRole('combobox', { name: 'Instance' }).selectOption({ label: 'Mock LS (jsonrpc)' });
    await alicePage.getByRole('combobox', { name: 'Algorithm' }).selectOption('weighted_completed');
    await fillTargetRow(alicePage, 0, 111, 1);
    await alicePage.getByRole('button', { name: 'Add Survey' }).click();
    await fillTargetRow(alicePage, 1, 222, 3);
    await alicePage.getByRole('button', { name: 'Create Route' }).click();
    await alicePage.waitForURL(/\/admin\/routes\/\d+$/, { waitUntil: 'networkidle' });
    const routeMatch = alicePage.url().match(/\/admin\/routes\/(\d+)$/);
    assert(routeMatch, `could not extract route id from ${alicePage.url()}`);
    const routeID = routeMatch[1];
    await screenshot(alicePage, '05-alice-route-detail.png');

    await alicePage.locator('input[name="name"]').fill('Alpha Route Updated');
    await alicePage.locator('input[name="slug"]').fill('alpha-route-updated');
    await alicePage.locator('textarea[name="description"]').fill('Owned by alice, updated');
    await alicePage.getByRole('combobox', { name: 'Algorithm' }).selectOption('weighted_fuzzy');
    await alicePage.locator('input[name="fuzzy_threshold"]').fill('4');
    await fillTargetRow(alicePage, 0, 111, 1);
    await fillTargetRow(alicePage, 1, 222, 3);
    await alicePage.getByRole('button', { name: 'Save Route' }).click();
    await alicePage.waitForURL(new RegExp(`/admin/routes/${routeID}$`), { waitUntil: 'networkidle' });
    const updatedBody = await alicePage.locator('body').textContent();
    assert(updatedBody.includes('Alpha Route Updated'), 'expected updated route name');
    assert(updatedBody.includes('alpha-route-updated'), 'expected updated route slug');
    assert(updatedBody.includes('w3') || updatedBody.includes('3'), 'expected weighted target to be visible');
    await screenshot(alicePage, '06-alice-route-detail-updated.png');

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
    await publicPage.goto(`${baseURL}/r/alpha-route-updated?token=abc123&src=mail`, { waitUntil: 'networkidle' });
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
    const detailURL = await alicePage.locator('#route-detail-url').inputValue();
    assert(detailURL === `${baseURL}/r/alpha-route-updated`, `expected full route URL on detail page, got ${detailURL}`);
    await screenshot(alicePage, '08-alice-route-detail-after-redirect.png');

    const adminAgainContext = await browser.newContext();
    const adminAgainPage = await login(adminAgainContext, 'admin', 'AdminPass123!');
    await adminAgainPage.goto(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });

    const bobRow = adminAgainPage.locator('tr').filter({ hasText: 'bob' }).first();
    await bobRow.getByRole('button', { name: 'Disable' }).click();
    await adminAgainPage.waitForURL(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
    await bobPage.goto(`${baseURL}/admin`, { waitUntil: 'networkidle' });
    assert(bobPage.url().endsWith('/admin/login'), `expected disabled bob session to land on login, got ${bobPage.url()}`);
    await expectLoginFailure(bobContext, 'bob', 'UserPassword123!');

    const aliceRow = adminAgainPage.locator('tr').filter({ hasText: 'alice' }).first();
    await aliceRow.getByPlaceholder('New password').fill('NewPassword456!');
    await aliceRow.getByRole('button', { name: 'Reset Password' }).click();
    await adminAgainPage.waitForURL(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });

    await alicePage.goto(`${baseURL}/admin`, { waitUntil: 'networkidle' });
    assert(alicePage.url().endsWith('/admin/login'), `expected alice old session to be invalidated after password reset, got ${alicePage.url()}`);
    await expectLoginFailure(aliceContext, 'alice', 'UserPassword123!');
    const aliceFreshContext = await browser.newContext();
    const aliceFreshPage = await login(aliceFreshContext, 'alice', 'NewPassword456!', '09-alice-new-password-dashboard.png');
    await aliceFreshPage.goto(`${baseURL}/admin/routes/${routeID}`, { waitUntil: 'networkidle' });
    await aliceFreshPage.getByRole('button', { name: 'Delete Route' }).click();
    await aliceFreshPage.waitForURL(`${baseURL}/admin/routes`, { waitUntil: 'networkidle' });
    const routesBody = await aliceFreshPage.locator('body').textContent();
    assert(!routesBody.includes('Alpha Route Updated'), 'expected route to be deleted');
    await screenshot(aliceFreshPage, '10-alice-routes-after-delete.png');

    await adminAgainPage.goto(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
    await bobRow.getByRole('button', { name: 'Delete' }).click();
    await adminAgainPage.waitForURL(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
    await aliceRow.getByRole('button', { name: 'Delete' }).click();
    await adminAgainPage.waitForURL(`${baseURL}/admin/users`, { waitUntil: 'networkidle' });
    const usersBody = await adminAgainPage.locator('body').textContent();
    assert(!usersBody.includes('alice'), 'expected alice to be deleted');
    assert(!usersBody.includes('bob'), 'expected bob to be deleted');
    await screenshot(adminAgainPage, '11-admin-users-after-delete.png');

    console.log('E2E OK');
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error(err.stack || String(err));
  process.exit(1);
});
