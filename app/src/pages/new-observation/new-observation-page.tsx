// New Observation: фото, местоположение, привязка к лошади, признаки,
// подтверждение и сохранение офлайн.
import { useRef, useState } from 'react';
import type { GeoLocation, TraitValue } from '@yc-hack-horses/contracts';
import { Card, CardHeader, CardTitle, CardContent } from '../../shared/ui/card';
import { Badge } from '../../shared/ui/badge';
import { Button } from '../../shared/ui/button';
import { Input, Label, Select } from '../../shared/ui/input';
import { useNodeResource } from '../../shared/api/hooks';
import { getHorses } from '../../shared/api/endpoints';
import { saveObservation } from '../../features/new-observation/model/save';
import { runLocalInference, type InferenceResult } from '../../features/new-observation/model/inference';

interface TraitDraft {
  trait: string;
  value: string;
  confidence: string;
}

export function NewObservationPage() {
  const horses = useNodeResource(() => getHorses({ limit: 100 }));
  const fileInput = useRef<HTMLInputElement>(null);
  const [files, setFiles] = useState<File[]>([]);
  const [horseId, setHorseId] = useState('');
  const [inference, setInference] = useState<InferenceResult | null>(null);
  const [traits, setTraits] = useState<TraitDraft[]>([{ trait: 'coat', value: '', confidence: '' }]);
  const [manualConfidence, setManualConfidence] = useState('0.9');
  const [locationText, setLocationText] = useState('');
  const [geo, setGeo] = useState<GeoLocation | null>(null);
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  const onPickFiles = (list: FileList | null) => {
    const picked = list ? Array.from(list) : [];
    setFiles(picked);
    setInference(null);
  };

  const onInference = async () => {
    const file = files[0];
    if (!file) return;
    const res = await runLocalInference(file);
    setInference(res);
    if (res.available && res.traits.length > 0) {
      setTraits(
        res.traits.map((t: TraitValue) => ({
          trait: t.trait,
          value: t.value,
          confidence: t.confidence != null ? String(t.confidence) : '',
        })),
      );
      setManualConfidence(String(res.confidence));
    }
    // res.available === false: показываем подсказку и оставляем ручной ввод.
  };

  const onGeolocate = () => {
    if (!navigator.geolocation) return;
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        const loc: GeoLocation = {
          lat: pos.coords.latitude,
          lon: pos.coords.longitude,
          accuracy_m: pos.coords.accuracy ?? null,
        };
        setGeo(loc);
        setLocationText(`${loc.lat.toFixed(6)}, ${loc.lon.toFixed(6)}`);
      },
      (err) => setResult({ ok: false, text: `Геолокация: ${err.message}` }),
    );
  };

  const onLocationText = (text: string) => {
    setLocationText(text);
    const m = text.match(/(-?\d+(?:\.\d+)?)\s*[, ]\s*(-?\d+(?:\.\d+)?)/);
    if (m) {
      const lat = Number(m[1]);
      const lon = Number(m[2]);
      if (!Number.isNaN(lat) && !Number.isNaN(lon)) setGeo({ lat, lon });
    } else {
      setGeo(null);
    }
  };

  const onSave = async () => {
    setSaving(true);
    setResult(null);
    const cleanTraits: TraitValue[] = traits
      .filter((t) => t.trait.trim() && t.value.trim())
      .map((t) => ({
        trait: t.trait.trim(),
        value: t.value.trim(),
        confidence: t.confidence ? Number(t.confidence) : null,
      }));
    const res = await saveObservation(
      {
        horse_id: horseId || null,
        observed_at: new Date().toISOString(),
        location: geo,
        blob_ids: [],
        trait_snapshot: {
          model_version: inference?.model_version ?? 'manual-v0',
          traits: cleanTraits,
          confidence: Number(manualConfidence) || 0,
          fingerprint: inference?.fingerprint ?? null,
        },
      },
      files,
    );
    setSaving(false);
    setResult({
      ok: res.saved === 'online',
      text: res.saved === 'online' ? 'Наблюдение сохранено на ноде.' : `Сохранено офлайн: ${res.error}`,
    });
    if (res.saved === 'online') {
      setFiles([]);
      if (fileInput.current) fileInput.current.value = '';
      setInference(null);
      setResult({ ok: true, text: 'Наблюдение сохранено на ноде.' });
    }
  };

  const canSave = files.length > 0 || traits.some((t) => t.value.trim());

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <h1 className="text-lg font-semibold">New Observation</h1>

      <Card>
        <CardHeader><CardTitle>Фото</CardTitle></CardHeader>
        <CardContent className="flex flex-col gap-2">
          <input
            ref={fileInput}
            type="file"
            accept="image/*"
            multiple
            onChange={(e) => onPickFiles(e.target.files)}
            className="text-sm"
          />
          {files.length > 0 && (
            <div className="text-xs text-muted-foreground">
              Выбрано: {files.map((f) => `${f.name} (${Math.round(f.size / 1024)} KB)`).join(', ')}
            </div>
          )}
          <Button variant="outline" size="sm" disabled={files.length === 0 || saving} onClick={() => void onInference()}>
            Локальный inference
          </Button>
          {inference && !inference.available && (
            <Badge variant="muted">Inference недоступен — вводите признаки вручную</Badge>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>Местоположение</CardTitle></CardHeader>
        <CardContent className="flex flex-col gap-2">
          <Button variant="outline" size="sm" onClick={onGeolocate}>
            Определить координаты
          </Button>
          <Label>Координаты (lat, lon) или ручной ввод</Label>
          <Input value={locationText} onChange={(e) => onLocationText(e.target.value)} placeholder="55.7045, 36.6306" />
          {geo && <div className="text-xs text-muted-foreground">Точность: {geo.accuracy_m ?? '—'}</div>}
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>Привязка</CardTitle></CardHeader>
        <CardContent>
          <Label>Лошадь</Label>
          <Select value={horseId} onChange={(e) => setHorseId(e.target.value)}>
            <option value="">Неизвестная особь (unknown)</option>
            {(horses.data?.horses ?? []).map((h) => (
              <option key={h.id} value={h.id}>
                {h.metadata?.name || h.metadata?.nickname || h.id.slice(0, 8)}
              </option>
            ))}
          </Select>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>Признаки</CardTitle></CardHeader>
        <CardContent className="flex flex-col gap-3">
          {traits.map((t, i) => (
            <div key={i} className="grid grid-cols-3 gap-2">
              <Input
                placeholder="признак"
                value={t.trait}
                onChange={(e) =>
                  setTraits((prev) => prev.map((p, j) => (j === i ? { ...p, trait: e.target.value } : p)))
                }
              />
              <Input
                placeholder="значение"
                value={t.value}
                onChange={(e) =>
                  setTraits((prev) => prev.map((p, j) => (j === i ? { ...p, value: e.target.value } : p)))
                }
              />
              <Input
                placeholder="conf"
                value={t.confidence}
                onChange={(e) =>
                  setTraits((prev) => prev.map((p, j) => (j === i ? { ...p, confidence: e.target.value } : p)))
                }
              />
            </div>
          ))}
          <Button
            variant="ghost"
            size="sm"
            className="self-start"
            onClick={() => setTraits((prev) => [...prev, { trait: '', value: '', confidence: '' }])}
          >
            + признак
          </Button>
          <div className="max-w-40">
            <Label>Общая confidence</Label>
            <Input value={manualConfidence} onChange={(e) => setManualConfidence(e.target.value)} />
          </div>
          <div className="text-xs text-muted-foreground">
            Подтверждение человеком: значения признаков проверяются перед сохранением
            (trait_snapshot.confirmed = true при онлайн-сохранении).
          </div>
        </CardContent>
      </Card>

      <div className="flex items-center gap-3">
        <Button disabled={!canSave || saving} onClick={() => void onSave()}>
          {saving ? 'Сохранение...' : 'Сохранить наблюдение'}
        </Button>
        {result && (
          <span className={result.ok ? 'text-sm text-primary' : 'text-sm text-yellow-400'}>
            {result.text}
          </span>
        )}
      </div>
    </div>
  );
}
