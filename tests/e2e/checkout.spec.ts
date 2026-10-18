import { expect, test, type Page } from '@playwright/test';
import { query } from './db';

// One cashier session, end to end: UI -> generated SDK -> Go -> Postgres -> UI.
// The steps share state (stock, sales), so they run in order.
test.describe.configure({ mode: 'serial' });

let page: Page;

test.beforeAll(async ({ browser }) => {
  page = await browser.newPage();
  await page.goto('/');
});

test.afterAll(async () => {
  await page.close();
});

const product = (name: string) => page.getByRole('button', { name: new RegExp(`^Add ${name}`) });

async function sales() {
  return query<{ id: string; total_minor: string }>('SELECT id, total_minor FROM sales ORDER BY created_at');
}

test('shows the seeded catalog with live stock', async () => {
  await expect(product('Butter Croissant')).toContainText('3 left');
  await expect(product('Espresso')).toContainText('200 in stock');
  await expect(page.getByText('Pumpkin Spice Latte')).toHaveCount(0); // inactive
});

test('sells two croissants and writes exactly one sale', async () => {
  await product('Butter Croissant').click();
  await product('Butter Croissant').click();
  await page.getByRole('button', { name: 'Charge $7.50' }).click();

  const receipt = page.getByRole('status').filter({ hasText: 'Sale complete' });
  await expect(receipt).toContainText('2 × Butter Croissant');
  await expect(receipt).toContainText('$7.50');
  await expect(product('Butter Croissant')).toContainText('1 left');

  const rows = await sales();
  expect(rows).toHaveLength(1);
  expect(rows[0]?.total_minor).toBe('750');
  const [line] = await query<{ product_id: string; quantity: number; unit_price_minor: string }>(
    'SELECT product_id, quantity, unit_price_minor FROM sale_lines',
  );
  expect(line).toEqual({ product_id: 'prd_croissant', quantity: 2, unit_price_minor: '375' });
  const [stock] = await query<{ available: number }>(
    "SELECT available FROM inventory WHERE product_id = 'prd_croissant'",
  );
  expect(stock?.available).toBe(1);
});

test('a simulated retry replays the sale without selling again', async () => {
  await page.getByLabel('Show internals').check();
  await page.getByRole('button', { name: 'Simulate retry' }).click();

  await expect(page.getByText(/^Replayed sale_[0-9a-z]+\. Stock unchanged: Butter Croissant 1\.$/)).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'Sale complete' })).toContainText('Replayed');
  await expect(page.locator('.exchange')).toContainText('Idempotent-Replayed: true');
  expect(await sales()).toHaveLength(1);
});

test('the Today panel counts the sale once, straight from the reports service', async () => {
  const reports = `http://localhost:${process.env.E2E_REPORTS_PORT ?? '18091'}`;
  const today = page.getByRole('region', { name: 'Today' });

  // The browser calls the Python service itself (CORS), not through the Go origin.
  const response = page.waitForResponse((r) => r.url() === `${reports}/v1/reports.dailySummary`);
  await today.getByRole('button', { name: 'Refresh' }).click();
  expect((await response).status()).toBe(200);

  // The sale reaches the reports service through the outbox; the replay adds nothing.
  await expect(today.getByTestId('today-sales')).toHaveText('1');
  await expect(today.getByTestId('today-revenue')).toHaveText('$7.50');
  await expect(today).toContainText('Butter Croissant');
  await expect(today).toContainText('Includes sales up to');
});

test('asking for more than the stock is rejected and writes nothing', async () => {
  await product('Butter Croissant').click();
  await product('Butter Croissant').click();
  await expect(page.getByText('Only 1 in stock')).toBeVisible();
  await page.getByRole('button', { name: 'Charge $7.50' }).click();

  const alert = page.getByRole('alert');
  await expect(alert).toContainText('Not enough stock for this order');
  await expect(alert).toContainText('sales.INSUFFICIENT_STOCK');
  expect(await sales()).toHaveLength(1);
  await page.getByRole('button', { name: 'Clear' }).click();
});

test('a viewer can browse but the server refuses to let them sell', async () => {
  await page.getByRole('radio', { name: 'Viewer' }).click();
  await product('Espresso').click();
  await page.getByRole('button', { name: 'Charge $3.00' }).click();

  await expect(page.getByRole('alert')).toContainText('This role cannot sell');
  await expect(page.getByRole('alert')).toContainText('FORBIDDEN');
  expect(await sales()).toHaveLength(1);
});
