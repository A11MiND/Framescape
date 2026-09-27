import { defineConfig, devices } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
export default defineConfig({
  outputDir: process.env.PLAYWRIGHT_OUTPUT_DIR ?? join(tmpdir(), 'framescape-playwright-results'),
  testDir: './tests/e2e', fullyParallel: true, timeout: 45_000,
  use: { baseURL: 'http://127.0.0.1:4173', trace: 'retain-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: { command: 'npm run dev -- --host 127.0.0.1 --port 4173', url: 'http://127.0.0.1:4173', reuseExistingServer: !process.env.CI },
})
