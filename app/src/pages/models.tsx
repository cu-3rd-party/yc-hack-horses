import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Download } from 'lucide-react';
import { getModels, updateModel, type ModelFile, type ModelSetup } from '../journal/node';
import { formatBytes } from '../shared/lib/utils';
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
function mb(bytes: number) {
  return new Intl.NumberFormat('ru', { maximumFractionDigits: 1 }).format(bytes / 1000000) + ' МБ';
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
  const active = file.state === 'downloading';
  return (
    <li className="model-row">
      <div className="model-summary">
        <div>
          <strong>{name}</strong>
          <small>
            {purpose}
            {file.size ? ' · ' + formatBytes(file.size) : ''}
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
            disabled={action.isPending}
            onClick={() => action.mutate('cancel')}
            aria-label={'Отменить загрузку ' + name}
          >
            Отменить
          </Button>
        ) : (
          <Button
            variant="outline"
            disabled={action.isPending}
            aria-label={'Скачать ' + name}
            onClick={() => {
              if (file.has_source || source) action.mutate('download');
              else setEditing(true);
            }}
          >
            <Download size={15} />
            {file.state === 'error' ? 'Повторить' : 'Скачать'}
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
          <label>
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
            <Button disabled={action.isPending}>Скачать файл</Button>{' '}
            <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
              Закрыть
            </Button>
          </div>
        </form>
      )}
      {active && (
        <div className="model-progress">
          <progress
            aria-label={'Загрузка ' + name}
            max={file.total || 1}
            value={file.total ? file.received : undefined}
          />
          <small>
            {mb(file.received)}
            {file.total > 0 && ' из ' + mb(file.total)}
          </small>
        </div>
      )}
      {(file.message || action.error) && (
        <p role="alert">
          {action.error?.message || file.message}
          {!file.has_source && !editing && (
            <button className="text-link" onClick={() => setEditing(true)}>
              Изменить ссылку
            </button>
          )}
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
