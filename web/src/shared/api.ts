// Calls to the catalog's API. Errors carry the API's own message ({"error": ...}).

export async function api<T>(path: string, opts?: RequestInit): Promise<T> {
  const r = await fetch(path, opts);
  let data: any = {};
  try {
    data = await r.json();
  } catch {
    /* no JSON body */
  }
  if (!r.ok) throw new Error((data && data.error) || "HTTP " + r.status);
  return data as T;
}

export function json(method: string, body: unknown): RequestInit {
  return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

export function errorText(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}
