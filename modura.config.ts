import type { ModuraConfig } from '@modura/cli';
import { catalog } from '@modura/module-catalog';
import { inventory } from '@modura/module-inventory';
import { reports } from '@modura/module-reports';
import { sales } from '@modura/module-sales';

const config: ModuraConfig = {
  modules: [catalog, inventory, sales, reports],
  api: { title: 'Modura POS API', version: '0.4.0' },
  services: {
    'sales-go': {
      modules: ['catalog', 'inventory', 'sales'],
      openapi: 'generated/openapi/sales-go.json',
    },
    'reports-py': {
      modules: ['reports'],
      openapi: 'generated/openapi/reports-py.json',
    },
  },
  // This repository keeps its release history in git (plan M8). The platform's registry
  // server is a separate service; pass --registry http://... to use it instead.
  registry: 'file:contracts/releases',
  outputs: {
    ir: 'generated/ir/modura.ir.json',
    mcp: 'generated/mcp/tools.json',
    sdk: 'packages/pos-sdk/src/generated.ts',
  },
};

export default config;
