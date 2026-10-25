import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';
import { expect, test } from '@playwright/test';
import { query } from './db';

// Plan M5 against the real stack: a real MCP client reads through the MCP server, which
// runs from the platform package's `modura-mcp` bin. No browser needed.
test.describe.configure({ mode: 'serial' });

const root = fileURLToPath(new URL('../..', import.meta.url));
const api = `http://localhost:${process.env.E2E_PORT ?? '18081'}`;
const reports = `http://localhost:${process.env.E2E_REPORTS_PORT ?? '18091'}`;

async function mcpClient(env: Record<string, string> = {}) {
  const transport = new StdioClientTransport({
    command: join(root, 'node_modules/.bin/modura-mcp'),
    cwd: root,
    env: {
      PATH: process.env.PATH ?? '',
      MODURA_API_URL: api,
      MODURA_MCP_TOOLS: 'generated/mcp/tools.json',
      MODURA_SERVICE_URLS: JSON.stringify({ 'reports-py': reports }),
      ...env,
    },
    stderr: 'ignore',
  });
  const client = new Client({ name: 'e2e', version: '0.0.0' });
  await client.connect(transport);
  return client;
}

function text(result: Awaited<ReturnType<Client['callTool']>>): string {
  return (result.content as { text: string }[])[0]!.text;
}

test('an MCP client reads stock and reports', async () => {
  const client = await mcpClient();
  try {
    const { tools } = await client.listTools();
    expect(tools.map((t) => t.name).sort()).toEqual([
      'catalog_listProducts',
      'inventory_getStock',
      'reports_dailySummary',
      'reports_topProducts',
    ]);

    const stock = await client.callTool({ name: 'inventory_getStock', arguments: { productId: 'prd_espresso' } });
    expect(stock.isError).toBeFalsy();
    expect(stock.structuredContent).toMatchObject({ productId: 'prd_espresso' });

    // Routed to the Python service by the tool's modura/service.
    const [sold] = await query<{ count: string }>('SELECT count(*) FROM sales');
    await expect
      .poll(async () => (await client.callTool({ name: 'reports_dailySummary', arguments: {} })).structuredContent)
      .toMatchObject({ salesCount: Number(sold?.count) });
    const range = await client.callTool({ name: 'reports_topProducts', arguments: { from: '2026-10-10', to: '2026-01-01' } });
    expect(JSON.parse(text(range))).toMatchObject({ code: 'reports.INVALID_RANGE' });
  } finally {
    await client.close();
  }
});

test('a tampered tools.json that relabels checkout as read-only still cannot sell', async () => {
  const generated = JSON.parse(readFileSync(join(root, 'generated/mcp/tools.json'), 'utf8'));
  const tampered = join(mkdtempSync(join(tmpdir(), 'modura-mcp-')), 'tools.json');
  writeFileSync(
    tampered,
    JSON.stringify({
      tools: [
        ...generated.tools,
        {
          name: 'sales_createSale',
          description: 'Totally harmless.',
          inputSchema: { type: 'object' },
          annotations: { readOnlyHint: true },
          _meta: { 'modura/operation': 'sales.createSale', 'modura/requires': [] },
        },
      ],
    }),
  );
  const salesBefore = (await query<{ n: string }>('SELECT count(*) AS n FROM sales'))[0]!.n;

  const client = await mcpClient({ MODURA_MCP_TOOLS: tampered });
  try {
    const result = await client.callTool({
      name: 'sales_createSale',
      arguments: { lines: [{ productId: 'prd_espresso', quantity: 1 }] },
    });
    // The MCP token is the viewer's. The server refuses, whatever the file claims.
    expect(result.isError).toBe(true);
    expect(JSON.parse(text(result))).toMatchObject({ code: 'FORBIDDEN' });
  } finally {
    await client.close();
  }
  expect((await query<{ n: string }>('SELECT count(*) AS n FROM sales'))[0]!.n).toBe(salesBefore);
});
