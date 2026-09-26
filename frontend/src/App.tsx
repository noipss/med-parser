import { useEffect, useMemo, useRef, useState } from "react";
import { api, type Preset, type Region, type Status } from "./api";

const STATE_LABEL: Record<Status["state"], string> = {
  idle: "Ожидание",
  running: "Идёт сбор",
  captcha: "Нужна капча",
  stopping: "Останавливаю…",
  done: "Готово",
  stopped: "Остановлено",
  error: "Ошибка",
};

const PREFS_KEY = "uzi-parser-prefs";

type Prefs = {
  target: string; // текущий пункт списка
  targets: string[]; // набранные цели («чипсы»); если пусто — берётся target
  mode: "auto" | "browser";
  showBrowser: boolean;
  withFD: boolean; // + врачи функциональной диагностики
};

const DEFAULT_TARGET = "preset:Ростов и соседние регионы";

function loadPrefs(): Prefs {
  const def: Prefs = { target: DEFAULT_TARGET, targets: [], mode: "auto", showBrowser: true, withFD: false };
  try {
    return { ...def, ...JSON.parse(localStorage.getItem(PREFS_KEY) || "{}") };
  } catch {
    return def;
  }
}

function formatDuration(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const hh = String(Math.floor(s / 3600)).padStart(2, "0");
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return `${hh}:${mm}:${ss}`;
}

export default function App() {
  const [regions, setRegions] = useState<Region[]>([]);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [status, setStatus] = useState<Status | null>(null);
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs);
  const [filter, setFilter] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(Date.now());
  const [online, setOnline] = useState(true);
  const logRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
    } catch {
      /* хранилище недоступно — не критично */
    }
  }, [prefs]);

  useEffect(() => {
    api.regions().then(setRegions).catch((e) => setError(String(e.message || e)));
    api.presets().then(setPresets).catch(() => setPresets([]));
  }, []);

  // Опрос статуса: чаще во время сессии.
  const running = status?.running ?? false;
  useEffect(() => {
    let alive = true;
    const tick = () =>
      api
        .status()
        .then((s) => {
          if (!alive) return;
          setStatus(s);
          setOnline(true);
        })
        .catch(() => alive && setOnline(false));
    tick();
    const id = setInterval(tick, running ? 500 : 2000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [running]);

  // Секундомер сессии тикает локально, без ожидания сервера.
  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(id);
  }, [running]);

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight });
  }, [status?.logs?.length]);

  const elapsed = useMemo(() => {
    if (!status || !status.startedAt || status.startedAt.startsWith("0001")) return 0;
    if (status.running) return (now - new Date(status.startedAt).getTime()) / 1000;
    return status.elapsedSec;
  }, [status, now]);

  const filteredPresets = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return q ? presets.filter((p) => p.name.toLowerCase().includes(q)) : presets;
  }, [presets, filter]);

  // Подпись цели для «чипсов»: имя набора, области или города.
  const labelOf = useMemo(() => {
    const cities = new Map(regions.flatMap((r) => r.cities.map((c) => [c.id, c.name] as const)));
    return (t: string) => {
      const [kind, val] = [t.slice(0, t.indexOf(":")), t.slice(t.indexOf(":") + 1)];
      if (kind === "city") return cities.get(val) ?? val;
      if (kind === "region") return `${val} (вся)`;
      return val;
    };
  }, [regions]);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return regions;
    return regions
      .map((r) =>
        r.name.toLowerCase().includes(q) ? r : { ...r, cities: r.cities.filter((c) => c.name.toLowerCase().includes(q)) },
      )
      .filter((r) => r.cities.length > 0);
  }, [regions, filter]);

  // Если поиск скрыл выбранный пункт, выбираем первый подходящий.
  useEffect(() => {
    if (!filter || filtered.length + filteredPresets.length === 0) return;
    const values = [
      ...filteredPresets.map((p) => `preset:${p.name}`),
      ...filtered.flatMap((r) => [`region:${r.name}`, ...r.cities.map((c) => `city:${c.id}`)]),
    ];
    if (!values.includes(prefs.target)) {
      if (filteredPresets.length > 0) {
        setPrefs((p) => ({ ...p, target: `preset:${filteredPresets[0].name}` }));
        return;
      }
      const firstCity = filtered[0].cities[0];
      setPrefs((p) => ({ ...p, target: filtered[0].cities.length === 1 ? `city:${firstCity.id}` : `region:${filtered[0].name}` }));
    }
  }, [filtered, filteredPresets, filter, prefs.target]);

  const addTarget = () =>
    setPrefs((p) => (p.targets.includes(p.target) ? p : { ...p, targets: [...p.targets, p.target] }));
  const removeTarget = (t: string) => setPrefs((p) => ({ ...p, targets: p.targets.filter((x) => x !== t) }));

  async function toggle() {
    setError("");
    setBusy(true);
    try {
      const target = prefs.targets.length > 0 ? prefs.targets.join(";") : prefs.target;
      setStatus(
        running
          ? await api.stop()
          : await api.start({ target, mode: prefs.mode, showBrowser: prefs.showBrowser, withFD: prefs.withFD }),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function reExport() {
    if (!status?.sessionId) return;
    try {
      const r = await api.export(status.sessionId);
      setStatus({ ...status, exportPath: r.path });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  const state = status?.state ?? "idle";
  const sources = Object.entries(status?.sources ?? {});
  const progress = status && status.pagesTotal > 0 ? Math.min(100, (status.pages / status.pagesTotal) * 100) : 0;

  return (
    <div className="app">
      <header className="top">
        <div>
          <h1>УЗИ-парсер</h1>
          <p className="sub">Живые данные о врачах ультразвуковой диагностики · НаПоправку, ПроДокторов, Zoon</p>
        </div>
        <span className={`badge badge-${state}`}>{online ? STATE_LABEL[state] : "Нет связи с сервером"}</span>
      </header>

      <section className="card controls">
        <div className="field grow">
          <label htmlFor="target">Город или область</label>
          <div className="target-row">
            <input
              className="search"
              placeholder="Поиск…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              disabled={running}
            />
            <select
              id="target"
              value={prefs.target}
              disabled={running}
              onChange={(e) => setPrefs({ ...prefs, target: e.target.value })}
            >
              {filteredPresets.length > 0 && (
                <optgroup label="Готовые наборы">
                  {filteredPresets.map((p) => (
                    <option key={p.name} value={`preset:${p.name}`}>
                      ★ {p.name} ({p.summary})
                    </option>
                  ))}
                </optgroup>
              )}
              {filtered.map((r) => (
                <optgroup key={r.name} label={r.name}>
                  {r.cities.length > 1 && (
                    <option value={`region:${r.name}`}>
                      ▸ Вся область: {r.name} ({r.cities.length} гор.)
                    </option>
                  )}
                  {r.cities.map((c) => (
                    <option key={c.id} value={`city:${c.id}`}>
                      {c.name} — {c.sources.join(", ")}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
            <button
              className="add-btn"
              title="Добавить в список целей"
              onClick={addTarget}
              disabled={running || prefs.targets.includes(prefs.target)}
            >
              + В список
            </button>
          </div>
          {prefs.targets.length > 0 && (
            <div className="chips">
              <span className="muted">Собрать по:</span>
              {prefs.targets.map((t) => (
                <span key={t} className="target-chip">
                  {labelOf(t)}
                  {!running && (
                    <button aria-label={`Убрать ${labelOf(t)}`} onClick={() => removeTarget(t)}>
                      ×
                    </button>
                  )}
                </span>
              ))}
              {!running && (
                <button className="link" onClick={() => setPrefs({ ...prefs, targets: [] })}>
                  очистить
                </button>
              )}
            </div>
          )}
        </div>
        <div className="field">
          <label htmlFor="mode">Режим</label>
          <select
            id="mode"
            value={prefs.mode}
            disabled={running}
            onChange={(e) => setPrefs({ ...prefs, mode: e.target.value as Prefs["mode"] })}
          >
            <option value="auto">Авто: HTTP, браузер при блокировке</option>
            <option value="browser">Только браузер</option>
          </select>
          <label className="check">
            <input
              type="checkbox"
              checked={prefs.showBrowser}
              disabled={running}
              onChange={(e) => setPrefs({ ...prefs, showBrowser: e.target.checked })}
            />
            Показывать окно браузера (для капчи)
          </label>
          <label className="check" title="Врачи функциональной диагностики делают УЗДГ сосудов и ЭхоКГ, но часть из них — только ЭКГ/ЭЭГ">
            <input
              type="checkbox"
              checked={prefs.withFD}
              disabled={running}
              onChange={(e) => setPrefs({ ...prefs, withFD: e.target.checked })}
            />
            + функциональная диагностика (УЗДГ, ЭхоКГ)
          </label>
        </div>
        <button
          className={`main-btn ${running ? "stop" : "start"}`}
          onClick={toggle}
          disabled={busy || !online || state === "stopping" || regions.length === 0}
        >
          {running ? "Закончить" : "Начать"}
        </button>
      </section>

      {error && <div className="banner banner-error">{error}</div>}
      {status?.message && (
        <div className={`banner ${state === "captcha" ? "banner-warn" : state === "done" ? "banner-ok" : ""}`}>
          {status.message}
        </div>
      )}

      <section className="stats">
        <Stat label="Время сессии" value={formatDuration(elapsed)} mono />
        <Stat label="Найдено врачей" value={(status?.found ?? 0).toLocaleString("ru-RU")} accent />
        <Stat label="Отсеяно неактуальных" value={(status?.skipped ?? 0).toLocaleString("ru-RU")} />
        <Stat label="Страниц" value={`${status?.pages ?? 0} / ${status?.pagesTotal ?? 0}`} mono />
        <Stat label="Скорость, врачей/мин" value={Math.round(status?.perMinute ?? 0).toLocaleString("ru-RU")} />
      </section>
      <div className="progress" aria-hidden>
        <div style={{ width: `${progress}%` }} />
      </div>

      {sources.length > 0 && (
        <section className="sources">
          {sources.map(([key, s]) => (
            <div key={key} className="card source">
              <div className="source-head">
                <strong>{s.title}</strong>
                <span className="chip">{s.mode === "browser" ? "браузер" : "HTTP"}</span>
              </div>
              <div className="source-state">{s.state}</div>
              <div className="source-nums">
                <span>найдено {s.found}</span>
                <span>стр. {s.pages}</span>
                {s.errors > 0 && <span className="err">ошибок {s.errors}</span>}
              </div>
            </div>
          ))}
        </section>
      )}

      {status?.exportPath && !running && (
        <section className="card export">
          <div>
            <div className="label">Документ для заказчика</div>
            <div className="path">{status.exportPath}</div>
            <div className="path muted">БД: {status.dbPath}</div>
          </div>
          <div className="export-actions">
            <button onClick={() => api.reveal(status.exportPath).catch((e) => setError(e.message))}>Показать в папке</button>
            <a className="btn" href={api.downloadUrl(status.sessionId)}>
              Скачать Excel
            </a>
            <button onClick={reExport}>Выгрузить заново</button>
          </div>
        </section>
      )}

      <section className="panels">
        <div className="card panel">
          <h2>Последние найденные</h2>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>ФИО</th>
                  <th>Должность</th>
                  <th>Сфера работы</th>
                  <th>Место работы</th>
                  <th>Город</th>
                  <th>Источник</th>
                </tr>
              </thead>
              <tbody>
                {(status?.recent ?? []).map((d, i) => (
                  <tr key={`${d.fio}-${d.city}-${i}`}>
                    <td className="fio">{d.fio}</td>
                    <td>{d.position}</td>
                    <td>{d.sphere || "—"}</td>
                    <td className="wp">{d.workplace}</td>
                    <td>{d.city}</td>
                    <td>{d.source}</td>
                  </tr>
                ))}
                {(status?.recent ?? []).length === 0 && (
                  <tr>
                    <td colSpan={6} className="empty">
                      Выберите город или область и нажмите «Начать»
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
        <div className="card panel">
          <h2>Журнал</h2>
          <div className="log" ref={logRef}>
            {(status?.logs ?? []).map((l, i) => (
              <div key={i}>
                <span className="muted">{l.time}</span> {l.text}
              </div>
            ))}
          </div>
        </div>
      </section>
    </div>
  );
}

function Stat({ label, value, accent, mono }: { label: string; value: string; accent?: boolean; mono?: boolean }) {
  return (
    <div className={`card stat ${accent ? "accent" : ""}`}>
      <div className="label">{label}</div>
      <div className={`value ${mono ? "mono" : ""}`}>{value}</div>
    </div>
  );
}
