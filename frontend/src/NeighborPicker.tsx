import { useEffect, useMemo, useState } from "react";
import { api, type NearbyCity, type Region } from "./api";

type Props = {
  baseRegion: string | null; // регион выбранной цели (для города — его регион)
  baseIsCity: boolean;
  baseCityId: string | null;
  selectedCities: string[]; // отмеченные ближайшие города (id)
  onCitiesChange: (next: string[]) => void;
  regions: Region[];
  neighbors: Record<string, string[]>;
  selected: string[]; // отмеченные регионы
  onChange: (next: string[]) => void;
  disabled: boolean;
};

const RADII = [25, 50, 100, 150];

export default function NeighborPicker({
  baseRegion,
  baseIsCity,
  baseCityId,
  selectedCities,
  onCitiesChange,
  regions,
  neighbors,
  selected,
  onChange,
  disabled,
}: Props) {
  const [showSecond, setShowSecond] = useState(false);
  const [radius, setRadius] = useState(50);
  const [nearby, setNearby] = useState<NearbyCity[]>([]);

  useEffect(() => {
    if (!baseCityId) {
      setNearby([]);
      return;
    }
    let alive = true;
    api
      .nearby(baseCityId, radius)
      .then((list) => alive && setNearby(list))
      .catch(() => alive && setNearby([]));
    return () => {
      alive = false;
    };
  }, [baseCityId, radius]);

  const cityCount = useMemo(() => new Map(regions.map((r) => [r.name, r.cities.length])), [regions]);

  const [first, second] = useMemo(() => {
    if (!baseRegion) return [[], []] as string[][];
    const byName = (a: string, b: string) => a.localeCompare(b, "ru");
    const l1 = [...(neighbors[baseRegion] ?? [])].sort(byName);
    const skip = new Set([baseRegion, ...l1]);
    const l2 = new Set<string>();
    for (const n of l1) for (const m of neighbors[n] ?? []) if (!skip.has(m)) l2.add(m);
    return [l1, [...l2].sort(byName)];
  }, [baseRegion, neighbors]);

  if (!baseRegion) {
    return <div className="neighbors muted">Соседние области можно добавить, если выбран регион или город.</div>;
  }

  const isOn = (r: string) => selected.includes(r);
  const toggle = (r: string) => onChange(isOn(r) ? selected.filter((x) => x !== r) : [...selected, r]);
  const setMany = (list: string[], on: boolean) =>
    onChange(on ? [...new Set([...selected, ...list])] : selected.filter((x) => !list.includes(x)));

  const box = (r: string) => (
    <label key={r} className={`nb-item ${isOn(r) ? "on" : ""}`}>
      <input type="checkbox" checked={isOn(r)} disabled={disabled} onChange={() => toggle(r)} />
      {r}
      <span className="muted"> · {cityCount.get(r) ?? 0} гор.</span>
    </label>
  );

  const cityOn = (id: string) => selectedCities.includes(id);
  const toggleCity = (id: string) =>
    onCitiesChange(cityOn(id) ? selectedCities.filter((x) => x !== id) : [...selectedCities, id]);

  return (
    <div className="neighbors">
      {baseCityId && (
        <>
          <div className="nb-head">
            <span className="label">Ближайшие города</span>
            <span className="nb-actions">
              <span className="radius">
                радиус
                {RADII.map((r) => (
                  <button key={r} className={`link ${radius === r ? "active" : ""}`} onClick={() => setRadius(r)}>
                    {r} км
                  </button>
                ))}
              </span>
              <button
                className="link"
                disabled={disabled || nearby.length === 0}
                onClick={() => onCitiesChange([...new Set([...selectedCities, ...nearby.map((c) => c.id)])])}
              >
                все в радиусе
              </button>
              <button
                className="link"
                disabled={disabled || selectedCities.length === 0}
                onClick={() => onCitiesChange([])}
              >
                снять
              </button>
            </span>
          </div>
          <div className="nb-list">
            {nearby.map((c) => (
              <label key={c.id} className={`nb-item ${cityOn(c.id) ? "on" : ""}`} title={c.sources.join(", ")}>
                <input type="checkbox" checked={cityOn(c.id)} disabled={disabled} onChange={() => toggleCity(c.id)} />
                {c.name}
                <span className="muted">
                  {" "}
                  · {c.km} км{c.region !== baseRegion ? ` · ${c.region}` : ""}
                </span>
              </label>
            ))}
            {nearby.length === 0 && <span className="muted">В радиусе {radius} км городов справочника нет.</span>}
          </div>
        </>
      )}
      <div className="nb-head">
        <span className="label">Ближайшие области{baseIsCity ? "" : ` к «${baseRegion}»`}</span>
        {first.length > 0 && (
          <span className="nb-actions">
            <button className="link" disabled={disabled} onClick={() => setMany(first, true)}>
              все соседние
            </button>
            <button className="link" disabled={disabled || selected.length === 0} onClick={() => onChange([])}>
              снять все
            </button>
          </span>
        )}
      </div>

      <div className="nb-list">
        {baseIsCity && box(baseRegion)}
        {first.map(box)}
        {first.length === 0 && !baseIsCity && <span className="muted">В справочнике нет граничащих регионов.</span>}
      </div>

      {second.length > 0 && (
        <>
          <button className="link" onClick={() => setShowSecond((v) => !v)}>
            {showSecond ? "скрыть" : "показать"} второй круг ({second.length})
          </button>
          {showSecond && (
            <>
              <div className="nb-list second">{second.map(box)}</div>
              <span className="nb-actions">
                <button className="link" disabled={disabled} onClick={() => setMany(second, true)}>
                  отметить второй круг
                </button>
              </span>
            </>
          )}
        </>
      )}
    </div>
  );
}
