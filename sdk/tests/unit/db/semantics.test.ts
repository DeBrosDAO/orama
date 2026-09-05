import { describe, expect, it, vi } from 'vitest';
import { DBClient } from '../../../src/db/client';
import { HttpClient } from '../../../src/core/http';
import { Repository } from '../../../src/db/repository';
import { SDKError } from '../../../src/errors';

function gateway(handler: (path: string, body: any) => Response) {
  const sent: Array<{ path: string; body: any }> = [];
  const fetchImpl = vi.fn(async (url: any, init: any) => {
    const path = new URL(String(url)).pathname;
    const body = init?.body ? JSON.parse(init.body) : undefined;
    sent.push({ path, body });
    return handler(path, body);
  });
  const http = new HttpClient({ baseURL: 'https://gw.example', maxRetries: 0, fetch: fetchImpl as any });
  return { http, sent };
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('a row that is not there', () => {
  /**
   * `DBClient.findOne` has always been typed `Promise<T | null>` while a 404
   * from the gateway propagated as an error, and `Repository.findOne` returned
   * null for the same condition. Two ways of reading one row, disagreeing about
   * what "absent" means.
   */
  it('is null from DBClient.findOne', async () => {
    const { http } = gateway(() => json(404, { error: 'not found' }));
    const db = new DBClient(http);

    await expect(db.findOne('users', { id: 1 })).resolves.toBeNull();
  });

  it('is null from Repository.findOne', async () => {
    const { http } = gateway(() => json(404, { error: 'not found' }));
    const repo = new Repository(http, 'users');

    await expect(repo.findOne({ id: 1 })).resolves.toBeNull();
  });

  it('is the row when there is one', async () => {
    const { http } = gateway(() => json(200, { id: 1, name: 'Ada' }));
    const db = new DBClient(http);

    await expect(db.findOne('users', { id: 1 })).resolves.toEqual({ id: 1, name: 'Ada' });
  });

  it('does not swallow a real failure', async () => {
    const { http } = gateway(() => json(500, { error: 'database unavailable' }));
    const db = new DBClient(http);

    await expect(db.findOne('users', { id: 1 })).rejects.toMatchObject({ httpStatus: 500 });
  });
});

describe('Repository identifiers', () => {
  /**
   * Values are parameterised; the table name, the primary key and the column
   * names are interpolated into the SQL text. Column names are the keys of the
   * entity handed to `save`, so an application that does `save({ ...req.body })`
   * lets a request body write SQL. There is no placeholder for an identifier,
   * so they are validated instead.
   */
  it('refuses a table name that is not an identifier', () => {
    const { http } = gateway(() => json(200, {}));
    expect(() => new Repository(http, 'users; DROP TABLE users')).toThrow(SDKError);
    expect(() => new Repository(http, 'users; DROP TABLE users')).toThrow(/invalid table name/);
  });

  it('refuses a primary key that is not an identifier', () => {
    const { http } = gateway(() => json(200, {}));
    expect(() => new Repository(http, 'users', 'id) --')).toThrow(/invalid primary key name/);
  });

  it('refuses a column name that is not an identifier, on insert', async () => {
    const { http } = gateway(() => json(200, { rows_affected: 1, last_insert_id: 1 }));
    const repo = new Repository<Record<string, any>>(http, 'users');

    await expect(repo.save({ 'name) VALUES (1); --': 'x' })).rejects.toThrow(/invalid column name/);
  });

  it('refuses a column name that is not an identifier, on update', async () => {
    const { http } = gateway(() => json(200, { rows_affected: 1 }));
    const repo = new Repository<Record<string, any>>(http, 'users');

    await expect(repo.save({ id: 7, 'name = 1, admin': 'x' })).rejects.toThrow(/invalid column name/);
  });

  it('accepts the identifiers a schema actually uses', async () => {
    const { http, sent } = gateway(() => json(200, { rows_affected: 1, last_insert_id: 9 }));
    const repo = new Repository<Record<string, any>>(http, 'user_profiles', 'user_id');

    await repo.save({ display_name: 'Ada', createdAt: 1, _internal: true });

    expect(sent[0].body.sql).toContain('INSERT INTO user_profiles');
    expect(sent[0].body.sql).toContain('display_name');
    expect(sent[0].body.sql).toContain('createdAt');
    expect(sent[0].body.sql).toContain('_internal');
  });

  it('still parameterises the values', async () => {
    const { http, sent } = gateway(() => json(200, { rows_affected: 1, last_insert_id: 9 }));
    const repo = new Repository<Record<string, any>>(http, 'users');

    await repo.save({ name: "Ada'); DROP TABLE users; --" });

    expect(sent[0].body.sql).not.toContain('DROP TABLE');
    expect(sent[0].body.args).toEqual(["Ada'); DROP TABLE users; --"]);
  });

  it('reports the offending name, so the mistake is findable', () => {
    const { http } = gateway(() => json(200, {}));
    try {
      new Repository(http, '1nvalid');
      throw new Error('expected a rejection');
    } catch (error) {
      expect(error).toBeInstanceOf(SDKError);
      expect((error as SDKError).code).toBe('INVALID_IDENTIFIER');
      expect((error as SDKError).details.name).toBe('1nvalid');
    }
  });
});
