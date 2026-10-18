import pg from 'pg';

/**
 * The E2E run gets its own database next to the dev one, so it never wipes local
 * data. CI points E2E_DATABASE_URL at the service container instead.
 */
export const databaseUrl =
  process.env.E2E_DATABASE_URL ??
  `postgres://modura:modura@localhost:${process.env.MODURA_PG_PORT ?? '55433'}/modura_e2e?sslmode=disable`;

export async function query<T extends pg.QueryResultRow>(sql: string, params: unknown[] = []): Promise<T[]> {
  const client = new pg.Client({ connectionString: databaseUrl });
  await client.connect();
  try {
    return (await client.query<T>(sql, params)).rows;
  } finally {
    await client.end();
  }
}
