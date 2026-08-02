export interface SystemInfo {
  name: string;
  version: string;
}

export async function loadSystemInfo(
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch,
): Promise<SystemInfo> {
  const response = await fetcher("/api/v1/system/info", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw new Error(`system info request failed with HTTP ${response.status}`);
  }

  const value: unknown = await response.json();
  if (!isSystemInfo(value)) {
    throw new Error("system info response is invalid");
  }
  return value;
}

function isSystemInfo(value: unknown): value is SystemInfo {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const record = value as Record<string, unknown>;
  return record.name === "relayward" && typeof record.version === "string" && record.version.length > 0;
}
