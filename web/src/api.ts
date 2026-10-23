let csrf = "";

export function setCsrf(value: string) {
  csrf = value;
}

export class ApiError extends Error {
  status: number;
  detail: string;
  constructor(status: number, detail: string) {
    super(detail);
    this.status = status;
    this.detail = detail;
  }
}

export async function api<T>(
  path: string,
  options: RequestInit & { stepUp?: string } = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  if (options.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (csrf && options.method && !["GET", "HEAD"].includes(options.method.toUpperCase())) {
    headers.set("X-CSRF-Token", csrf);
  }
  if (options.stepUp) {
    headers.set("X-Step-Up-Token", options.stepUp);
  }
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  const type = response.headers.get("content-type") || "";
  const payload = type.includes("application/json") ? await response.json() : await response.text();
  if (!response.ok) {
    const detail =
      typeof payload === "object" && payload && "detail" in payload
        ? String(payload.detail)
        : String(payload || `HTTP ${response.status}`);
    throw new ApiError(response.status, detail);
  }
  return payload as T;
}

export async function restoreCsrf(): Promise<void> {
  const result = await api<{ csrf: string }>("/api/auth/csrf", { method: "POST" });
  setCsrf(result.csrf);
}

