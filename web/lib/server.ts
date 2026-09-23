// Server-side proxy web→Go (см. 04-architecture/02: web никогда не ходит в PG/Redis напрямую).
// API_INTERNAL_URL: http://api-public:8080 в compose, http://localhost:8080 локально.
export const GO = process.env.API_INTERNAL_URL || "http://localhost:8080";

export async function proxy(path: string, init: RequestInit) {
  const res = await fetch(`${GO}${path}`, { ...init, headers: { ...(init.headers || {}) } });
  return res;
}
