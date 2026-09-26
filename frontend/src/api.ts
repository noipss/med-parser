// Клиент API Go-бэкенда.

export type City = { id: string; name: string; sources: string[] };
export type Region = { name: string; cities: City[] };
export type NearbyCity = { id: string; name: string; region: string; km: number; sources: string[] };
export type Preset = { name: string; target: string; summary: string; cities: number };

export type SourceStat = {
  title: string;
  mode: string;
  state: string;
  found: number;
  pages: number;
  errors: number;
};

export type DoctorView = {
  fio: string;
  position: string;
  sphere: string;
  workplace: string;
  city: string;
  source: string;
};

export type Status = {
  running: boolean;
  state: "idle" | "running" | "captcha" | "stopping" | "done" | "stopped" | "error";
  message: string;
  sessionId: number;
  target: string;
  targetName: string;
  mode: string;
  startedAt: string;
  finishedAt: string;
  elapsedSec: number;
  cities: number;
  found: number;
  skipped: number;
  pages: number;
  pagesTotal: number;
  errors: number;
  perMinute: number;
  sources: Record<string, SourceStat>;
  recent: DoctorView[] | null;
  logs: { time: string; text: string }[] | null;
  exportPath: string;
  dbPath: string;
};

export type StartOptions = {
  target: string;
  mode: "auto" | "browser";
  showBrowser: boolean;
  withFD: boolean;
};

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json" },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data as T;
}

export const api = {
  regions: () => call<Region[]>("/api/regions"),
  presets: () => call<Preset[]>("/api/presets"),
  nearby: (city: string, km: number) =>
    call<NearbyCity[]>(`/api/nearby?city=${encodeURIComponent(city)}&km=${km}`),
  neighbors: () => call<Record<string, string[]>>("/api/neighbors"),
  status: () => call<Status>("/api/status"),
  start: (o: StartOptions) => call<Status>("/api/start", { method: "POST", body: JSON.stringify(o) }),
  stop: () => call<Status>("/api/stop", { method: "POST" }),
  export: (sessionId: number) =>
    call<{ path: string; count: number }>("/api/export", { method: "POST", body: JSON.stringify({ sessionId }) }),
  reveal: (path: string) => call<{ path: string }>("/api/reveal", { method: "POST", body: JSON.stringify({ path }) }),
  downloadUrl: (sessionId: number) => `/api/export.xlsx?session=${sessionId}`,
};
