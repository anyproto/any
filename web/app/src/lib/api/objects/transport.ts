import { ApiError, apiFetch } from '../client';
import type { ObjectRecord } from './model';

export interface QueryBody {
  filter?: Record<string, unknown>;
  sort?: string[];
  limit?: number;
  offset?: number;
}

interface QueryResponse {
  records: ObjectRecord[];
}

export async function queryObjects(
  spaceId: string,
  body: QueryBody,
  signal?: AbortSignal,
): Promise<ObjectRecord[]> {
  const init: { method: 'POST'; json: QueryBody; signal?: AbortSignal } = {
    method: 'POST',
    json: body,
  };
  if (signal) init.signal = signal;
  const res = await apiFetch<QueryResponse>(
    `/spaces/${encodeURIComponent(spaceId)}/objects/query`,
    init,
  );
  return parseQueryResponse(res);
}

export async function createObject(
  spaceId: string,
  body: Record<string, unknown> = {},
): Promise<{ objectId: string }> {
  const res = await apiFetch<{ objectId: string }>(
    `/spaces/${encodeURIComponent(spaceId)}/objects`,
    { method: 'POST', json: body },
  );
  if (!res || typeof res.objectId !== 'string') {
    throw badObjectResponse('server returned an invalid create-object response');
  }
  return res;
}

/**
 * Set base properties on an object — used for renames (typeId = 'any',
 * patch = {name: '...'}) and for tree moves (typeId = 'nav',
 * patch = {parentId, pos}).
 */
export async function setObjectProperty(
  spaceId: string,
  objectId: string,
  typeId: string,
  patch: Record<string, unknown>,
): Promise<unknown> {
  return apiFetch<unknown>(
    `/spaces/${encodeURIComponent(spaceId)}/properties/${encodeURIComponent(objectId)}/base/${encodeURIComponent(typeId)}`,
    { method: 'POST', json: { patch } },
  );
}

export async function deleteObject(spaceId: string, objectId: string): Promise<void> {
  await apiFetch<void>(
    `/spaces/${encodeURIComponent(spaceId)}/objects/${encodeURIComponent(objectId)}`,
    { method: 'DELETE' },
  );
}

function parseQueryResponse(res: QueryResponse): ObjectRecord[] {
  if (!res || !Array.isArray(res.records)) {
    throw badObjectResponse('server returned an invalid objects query response');
  }
  if (!res.records.every(isObjectRecord)) {
    throw badObjectResponse('server returned an invalid object record');
  }
  return res.records;
}

function isObjectRecord(value: unknown): value is ObjectRecord {
  return (
    value !== null &&
    typeof value === 'object' &&
    typeof (value as { id?: unknown }).id === 'string'
  );
}

function badObjectResponse(message: string): ApiError {
  return new ApiError(
    {
      code: 'client.bad_response',
      message,
    },
    200,
  );
}
