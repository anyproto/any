/**
 * Tiny typed fetch wrapper for the any HTTP API.
 *
 * Decodes the canonical error envelope (see docs/06-errors.md):
 *   { "error": { "code": "...", "message": "...", "details"?: {...} } }
 *
 * Throws ApiError for any non-2xx response or invalid envelope.
 * Successful responses are JSON.parse'd into T.
 */

export interface ApiErrorEnvelope {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly details?: Record<string, unknown>;

  constructor(envelope: ApiErrorEnvelope, status: number) {
    super(envelope.message);
    this.name = 'ApiError';
    this.code = envelope.code;
    this.status = status;
    if (envelope.details !== undefined) {
      this.details = envelope.details;
    }
  }
}

const BASE_URL = '/v1';

export interface ApiFetchOptions extends Omit<RequestInit, 'body'> {
  /** JSON-serialisable body. Skips serialization if FormData/Blob/etc. */
  json?: unknown;
  /** AbortSignal to cancel the request. */
  signal?: AbortSignal;
}

/** Make a JSON request to /v1/<path>; throws ApiError on non-2xx. */
export async function apiFetch<T>(
  path: string,
  { json, headers, ...init }: ApiFetchOptions = {},
): Promise<T> {
  const url = `${BASE_URL}${path.startsWith('/') ? path : `/${path}`}`;
  const requestHeaders = new Headers(headers);
  const requestInit: RequestInit = { ...init, headers: requestHeaders };
  if (json !== undefined) {
    requestHeaders.set('Content-Type', 'application/json; charset=utf-8');
    requestInit.body = JSON.stringify(json);
  }

  const response = await fetch(url, requestInit);

  // Try to parse a body either way; some endpoints return 204 No Content.
  let parsed: unknown = undefined;
  if (response.status !== 204) {
    const text = await response.text();
    if (text.length > 0) {
      try {
        parsed = JSON.parse(text) as unknown;
      } catch {
        throw new ApiError(
          {
            code: 'client.bad_response',
            message: 'server returned non-JSON body',
            details: { status: response.status, body: text.slice(0, 500) },
          },
          response.status,
        );
      }
    }
  }

  if (!response.ok) {
    const envelope = extractErrorEnvelope(parsed);
    throw new ApiError(envelope, response.status);
  }

  return parsed as T;
}

function extractErrorEnvelope(parsed: unknown): ApiErrorEnvelope {
  if (
    parsed !== null &&
    typeof parsed === 'object' &&
    'error' in parsed &&
    typeof (parsed as { error: unknown }).error === 'object' &&
    (parsed as { error: unknown }).error !== null
  ) {
    const e = (parsed as { error: Record<string, unknown> }).error;
    if (typeof e['code'] === 'string' && typeof e['message'] === 'string') {
      const env: ApiErrorEnvelope = {
        code: e['code'],
        message: e['message'],
      };
      if (typeof e['details'] === 'object' && e['details'] !== null) {
        env.details = e['details'] as Record<string, unknown>;
      }
      return env;
    }
  }
  return {
    code: 'client.bad_response',
    message: 'server returned a non-conforming error envelope',
  };
}
