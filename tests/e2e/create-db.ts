// Recreates the E2E database from scratch, so every run starts empty.
import pg from 'pg';
import { databaseUrl } from './db';

const url = new URL(databaseUrl);
const name = url.pathname.slice(1);
if (!/^[a-z_][a-z0-9_]*e2e[a-z0-9_]*$/.test(name)) {
  throw new Error(`Refusing to drop ${name}: E2E database names must contain "e2e"`);
}

url.pathname = '/postgres';
const admin = new pg.Client({ connectionString: url.toString() });
await admin.connect();
try {
  await admin.query(`DROP DATABASE IF EXISTS ${name} WITH (FORCE)`);
  await admin.query(`CREATE DATABASE ${name}`);
} finally {
  await admin.end();
}
