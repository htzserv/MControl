const TOKEN_KEY = 'mc_v2_token';

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || '';
}

export function setToken(tok: string): void {
  if (tok) localStorage.setItem(TOKEN_KEY, tok);
  else localStorage.removeItem(TOKEN_KEY);
}

export async function api<T = Record<string, unknown>>(
  path: string,
  method: string = 'GET',
  body?: unknown
): Promise<T & { ok?: boolean; error?: string }> {
  const headers: Record<string, string> = {};
  const tok = getToken();
  if (tok) headers['Authorization'] = `Bearer ${tok}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({ ok: false, error: `HTTP ${res.status}` }));
  return data;
}
