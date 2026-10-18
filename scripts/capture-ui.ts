/**
 * Captures docs/media/pos.png and docs/media/pos-internals.png for the README.
 * Start the same server the E2E test uses first:
 *
 *   bash tests/e2e/start-server.sh &
 *   pnpm --filter @modura/scripts capture-ui
 */
import { mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

const media = join(resolve(dirname(fileURLToPath(import.meta.url)), '..'), 'docs/media');
const baseURL = process.env.BASE_URL ?? 'http://localhost:18081';
mkdirSync(media, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 780 }, colorScheme: 'light' });
await page.goto(baseURL);
const add = (name: string) => page.getByRole('button', { name: new RegExp(`^Add ${name}`) }).click();

await add('Flat White');
await add('Butter Croissant');
await add('Butter Croissant');
await page.screenshot({ path: join(media, 'pos.png') });

await page.getByRole('button', { name: /^Charge/ }).click();
await page.getByText('Sale complete').waitFor();
await page.getByLabel('Show internals').check();
await page.getByRole('button', { name: 'Simulate retry' }).click();
await page.getByText(/Stock unchanged/).waitFor();
await page.setViewportSize({ width: 1280, height: 1160 });
await page.screenshot({ path: join(media, 'pos-internals.png') });

await browser.close();
console.log(`Wrote ${join(media, 'pos.png')} and pos-internals.png`);
