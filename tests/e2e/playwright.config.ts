import { defineConfig, devices } from '@playwright/test';

const port = process.env.E2E_PORT ?? '18081';
const reportsPort = process.env.E2E_REPORTS_PORT ?? '18091';

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: `http://localhost:${port}`,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'bash start-server.sh',
      url: `http://localhost:${port}/healthz`,
      timeout: 180_000,
      reuseExistingServer: false,
      stdout: 'ignore',
      stderr: 'pipe',
    },
    {
      // The reports service, a separate process the UI calls directly (ADR-0011).
      command: 'bash start-reports.sh',
      url: `http://localhost:${reportsPort}/healthz`,
      timeout: 240_000,
      reuseExistingServer: false,
      stdout: 'ignore',
      stderr: 'pipe',
    },
  ],
});
