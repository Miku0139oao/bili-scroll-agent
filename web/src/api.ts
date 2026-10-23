const CSRF_STORAGE_KEY = "bili-scroll-csrf";

function readStoredCsrf(): string {
  try {
    return window.localStorage.getItem(CSRF_STORAGE_KEY) || "";
  } catch {
    return "";
  }
}

let csrf = readStoredCsrf();

export function setCsrf(value: string) {
  csrf = value;
  try {
    if (value) {
      window.localStorage.setItem(CSRF_STORAGE_KEY, value);
    } else {
      window.localStorage.removeItem(CSRF_STORAGE_KEY);
    }
  } catch {
    // The in-memory token still works when storage is unavailable.
  }
}

if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.key === CSRF_STORAGE_KEY) {
      csrf = event.newValue || "";
    }
  });
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
  const { stepUp, ...requestOptions } = options;
  let csrfRetried = false;

  for (;;) {
    const headers = new Headers(requestOptions.headers);
    if (requestOptions.body && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
    if (
      csrf &&
      requestOptions.method &&
      !["GET", "HEAD"].includes(requestOptions.method.toUpperCase())
    ) {
      headers.set("X-CSRF-Token", csrf);
    }
    if (stepUp) {
      headers.set("X-Step-Up-Token", stepUp);
    }

    const response = await fetch(path, {
      ...requestOptions,
      headers,
      credentials: "same-origin",
    });
    const type = response.headers.get("content-type") || "";
    const payload = type.includes("application/json")
      ? await response.json()
      : await response.text();

    if (response.ok) {
      return payload as T;
    }

    const detail =
      typeof payload === "object" && payload && "detail" in payload
        ? String(payload.detail)
        : String(payload || `HTTP ${response.status}`);

    if (
      response.status === 403 &&
      detail.toLowerCase().includes("invalid csrf token") &&
      path !== "/api/auth/csrf" &&
      !csrfRetried
    ) {
      csrfRetried = true;
      await restoreCsrf(true);
      continue;
    }

    throw new ApiError(response.status, detail);
  }
}

export async function restoreCsrf(force = false): Promise<void> {
  if (csrf && !force) {
    return;
  }
  const result = await api<{ csrf: string }>("/api/auth/csrf", { method: "POST" });
  setCsrf(result.csrf);
}
