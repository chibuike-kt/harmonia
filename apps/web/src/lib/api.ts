// Typed client for the Go backend. Every call goes straight to the
// backend's own origin — this frontend has no API routes of its own to
// proxy through (ADR-003: "one product, two independent build systems").
// The base URL is env-configured per deployment, never hardcoded.

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL;

/** The `{"error": "..."}` shape every handler in the Go API uses. */
interface ApiErrorBody {
  error: string;
}

/**
 * Thrown by apiFetch on a non-2xx response. Carries the backend's own
 * error message (from `{"error": "..."}`) rather than a generic one, so
 * callers can show the user what the API actually said instead of
 * re-deriving their own copy of its validation rules.
 */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export interface ApiFetchOptions extends Omit<RequestInit, "body"> {
  /** JSON-serialized automatically; omit for a bodyless request. */
  body?: unknown;
}

/**
 * Builds a full URL to path on the Go backend. This is the one place
 * that reads NEXT_PUBLIC_API_BASE_URL — apiFetch uses it for JSON calls,
 * and callers that need a real browser navigation instead of a fetch
 * (the OAuth login buttons, which must hit the backend's redirect
 * directly rather than through this client) use it too, so both agree
 * on the backend's origin.
 *
 * Throws if NEXT_PUBLIC_API_BASE_URL isn't set — a misconfigured
 * environment should fail loudly, not silently hit a relative path that
 * happens to 404.
 */
export function apiUrl(path: string): string {
  if (!API_BASE_URL) {
    throw new Error(
      "NEXT_PUBLIC_API_BASE_URL is not set — see apps/web/.env.example",
    );
  }
  return `${API_BASE_URL}${path}`;
}

/**
 * Calls `${NEXT_PUBLIC_API_BASE_URL}${path}` and decodes the JSON
 * response as T. Always sends cookies (`credentials: "include"`) since
 * the backend authenticates humans via an HttpOnly session cookie, not a
 * header this client could attach itself.
 *
 * Throws ApiError on a non-2xx response, with the backend's own
 * `{"error": "..."}` message when the body actually has one.
 */
export async function apiFetch<T>(
  path: string,
  options: ApiFetchOptions = {},
): Promise<T> {
  const { body, headers, ...rest } = options;
  const response = await fetch(apiUrl(path), {
    ...rest,
    credentials: "include",
    headers: {
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...headers,
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (!response.ok) {
    let message = response.statusText;
    try {
      const data = (await response.json()) as ApiErrorBody;
      if (data.error) {
        message = data.error;
      }
    } catch {
      // Not a JSON body (or an empty one) — the status text is the best
      // we've got.
    }
    throw new ApiError(response.status, message);
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}
