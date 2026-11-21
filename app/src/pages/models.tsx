import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Download, Link2 } from 'lucide-react';
import { getModels, updateModel, type ModelFile, type ModelSetup } from '../journal/node';
import { Button } from '../shared/ui/button';

const key = ['recognition-models'];
export function useModels(enabled: boolean) {
  return useQuery({
    queryKey: key,
    enabled,
    queryFn: ({ signal }) => getModels(signal),
    retry: 1,
    refetchInterval: (q) =>
      q.state.data?.models.some((m) => ['checking', 'downloading'].includes(m.state)) ? 500 : false,
  });
}
const names: Record<string, [string, string]> = {
  yolo: ['YOLO', 'Поиск лошадей на снимке'],
  megadescriptor: ['MegaDescriptor', 'Поиск похожих особей'],
  dinov2: ['DINOv2', 'Признаки для сегментации'],
  'part-heads': ['Маски частей тела', 'Контуры лошади и её черт'],
};
const decimal = new Intl.NumberFormat('ru', { maximumFractionDigits: 1 });
function formatSize(bytes: number) {
  if (bytes < 1000) return decimal.format(bytes) + ' Б';
  if (bytes < 1_000_000) return decimal.format(bytes / 1000) + ' КБ';
  if (bytes >= 1_000_000_000) return new Intl.NumberFormat('ru', { maximumFractionDigits: 2 }).format(bytes / 1_000_000_000) + ' ГБ';
  return decimal.format(bytes / 1_000_000) + ' МБ';
}
function ModelRow({ file }: { file: ModelFile }) {
  const cache = useQueryClient();
  const [source, setSource] = useState('');
  const [editing, setEditing] = useState(false);
  const action = useMutation({
    mutationFn: (kind: 'download' | 'cancel') => updateModel(file.id, kind, source),
    onSuccess: (status) => {
      cache.setQueryData(key, status);
      setEditing(false);
    },
  });
  const [name, purpose] = names[file.id] ?? [file.filename, ''];
  const installLabel = file.bundled ? 'Установить' : 'Скачать';
  const active = file.state === 'downloading';
  const percent = file.total > 0 ? Math.min(100, 100 * file.received / file.total) : undefined;
  return (
    <li className="model-row">
      <div className="model-summary">
        <div>
          <strong>{name}</strong>
          <small>
            {purpose}
            {file.size ? ' · ' + formatSize(file.size) : ''}
          </small>
        </div>
        {file.state === 'ready' ? (
          <span className="model-ready">
            <Check size={16} />
            Установлена
          </span>
        ) : file.state === 'checking' ? (
          <span>Проверяем…</span>
        ) : active ? (
          <Button
            variant="outline"
            className="model-action"
            disabled={action.isPending}
            onClick={() => action.mutate('cancel')}
            aria-label={'Отменить загрузку ' + name}
          >
            Отменить
          </Button>
        ) : !file.has_source ? (
          !editing && (
            <Button variant="outline" className="model-action" onClick={() => setEditing(true)}
              aria-label={'Указать ссылку на ' + name}>
              <Link2 size={15} /> Указать ссылку
            </Button>
          )
        ) : (
          <Button
            className="model-action model-download"
            disabled={action.isPending}
            aria-label={installLabel + ' ' + name}
            onClick={() => action.mutate('download')}
          >
            <Download size={15} />
            {file.state === 'error' ? 'Повторить' : installLabel}
          </Button>
        )}
      </div>
      {editing && (
        <form
          className="model-source"
          onSubmit={(e) => {
            e.preventDefault();
            action.mutate('download');
          }}
        >
          <label className="field">
            Ссылка на {file.filename}
            <input
              autoFocus
              type="url"
              required
              placeholder="https://"
              value={source}
              onChange={(e) => setSource(e.target.value)}
            />
          </label>
          <small>Прямая HTTPS-ссылка на файл этой версии.</small>
          <div>
            <Button className="model-action model-download" disabled={action.isPending}>Скачать файл</Button>{' '}
            <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
              Закрыть
            </Button>
          </div>
        </form>
      )}
      {active && (
        <div className="model-progress">
          <div
            className="model-meter"
            role="progressbar"
            aria-label={'Загрузка ' + name}
            aria-valuenow={percent === undefined ? undefined : Math.round(percent)}
          >
            <span style={percent === undefined ? undefined : { width: percent + '%' }} />
          </div>
          <small>
            {formatSize(file.received)}
            {file.total > 0 && ' из ' + formatSize(file.total)}
          </small>
        </div>
      )}
      {(file.message || action.error) && (
        <p role="alert">
          {action.error?.message || file.message}
        </p>
      )}
    </li>
  );
}
export function ModelPanel({ setup }: { setup: ModelSetup }) {
  return (
    <section className="model-panel" aria-label="Модели распознавания">
      <h2>Модели распознавания</h2>
      {setup.mode === 'disabled' ? (
        <p>Распознавание выключено на этой ноде.</p>
      ) : (
        <>
          {!setup.ready && <p>Для сравнения снимка установите модели на этом устройстве.</p>}
          {!setup.runtime_ready && (
            <p role="status">Для запуска моделей на ноде нужен ONNX Runtime.</p>
          )}
          {setup.models.length ? (
            <ul>
              {setup.models.map((m) => (
                <ModelRow key={m.id} file={m} />
              ))}
            </ul>
          ) : (
            <p>Установка доступна через локальную ноду.</p>
          )}
        </>
      )}
    </section>
  );
}
