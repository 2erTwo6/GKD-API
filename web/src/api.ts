const TOKEN_KEY = "gkd_token";

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || "";
}

export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

export async function api<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch("/api/admin" + path, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      Authorization: "Bearer " + getToken(),
      ...(opts.headers || {}),
    },
  });
  if (res.status === 401) {
    clearToken();
    window.location.href = "/login";
    throw new Error("登录已过期，请重新登录");
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({} as any));
    throw new Error((body as any).message || `请求失败 (HTTP ${res.status})`);
  }
  return res.json() as Promise<T>;
}

export function fmtTime(s?: string | null): string {
  if (!s) return "-";
  return new Date(s).toLocaleString("zh-CN", { hour12: false });
}
