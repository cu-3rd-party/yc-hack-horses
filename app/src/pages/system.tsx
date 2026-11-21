import { ModelPanel, useModels } from './models';
import { setDemo, useDemo } from '../journal/mode';
import { version } from '../../package.json';
import { useState } from 'react';
import { Activity, Database, Images, ListChecks, RefreshCw, RotateCcw } from 'lucide-react';
import type { Notebook } from '../journal/types';
import { useCommand } from '../journal/use-journal';
import { Heading, Modal, Toggle, useNotice } from '../shared/ui/journal-ui';
import { Button } from '../shared/ui/button';
import { formatBytes } from '../shared/lib/utils';

import { initialScheme, schemes, selectScheme } from '../app/appearance';

const stamp = (iso: string | null) =>
  iso ? new Date(iso).toLocaleString('ru-RU') : 'Ещё не выполнялась';
function Facts({ rows }: { rows: [string, string | number][] }) {
  return (
    <dl className="facts">
      {rows.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}
export function Dashboard({ book }: { book: Notebook }) {
  if (book.node)
    return (
      <>
        <Heading title="Сводка" description="Локальный полевой журнал" />
        <section className="panel">
          <Facts
            rows={[
              ['Особи', book.horses.length],
              ['Наблюдения', book.observations.length],
              ['Локальный узел', book.node.node_id],
              ['События', book.node.event_count],
            ]}
          />
        </section>
      </>
    );
  return (
    <>
      <Heading title="Сводка" description="Состояние локального журнала и обмена наблюдениями." />
      <div className="stat-grid">
        {[
          { title: 'Особи', value: book.horses.length, Icon: Images },
          { title: 'Наблюдения', value: book.observations.length, Icon: ListChecks },
          { title: 'В очереди', value: book.pending.length, Icon: Database },
        ].map(({ title, value, Icon }) => (
          <article className="panel stat" key={title}>
            <Icon size={20} />
            <span>{title}</span>
            <strong>{value}</strong>
          </article>
        ))}
      </div>
      <section className="panel">
        <h2>
          <Activity size={18} /> Демо-узел
        </h2>
        <Facts
          rows={[
            ['Идентификатор', 'TAKHI-FIELD-01'],
            ['Роль', 'Полевой наблюдатель'],
            ['Связь с центром', book.online ? 'Доступна' : 'Офлайн'],
            ['Поток событий', book.online ? 'Подключён' : 'Ожидание'],
            ['Последняя синхронизация', stamp(book.lastSync)],
          ]}
        />
      </section>
    </>
  );
}
export function Sync({ book }: { book: Notebook }) {
  const sync = useCommand('/sync'),
    pref = useCommand('/preferences'),
    notice = useNotice();
  if (book.node)
    return (
      <>
        <Heading title="Синхронизация" description="Состояние обмена по данным локальной ноды." />
        <section className="panel">
          <h2>Узлы обмена</h2>
          {book.node.sync?.length ? (
            book.node.sync.map((peer) => (
              <div key={peer.addr}>
                <h3>{peer.addr}</h3>
                <Facts
                  rows={[
                    ['Отправлено событий', peer.events_sent],
                    ['Получено событий', peer.events_received],
                    [
                      'Последний обмен',
                      stamp(
                        peer.last_sync_at?.startsWith('0001') ? null : (peer.last_sync_at ?? null),
                      ),
                    ],
                    ...(peer.last_error ? [['Ошибка', peer.last_error] as [string, string]] : []),
                  ]}
                />
              </div>
            ))
          ) : (
            <p className="empty">Нода не сообщила об узлах обмена.</p>
          )}
        </section>
      </>
    );
  return (
    <>
      <Heading
        title="Синхронизация"
        description="Демонстрация обмена с центральным узлом и очереди полевых записей."
      />
      <section className="panel">
        <div className="section-heading">
          <h2>Центральный узел</h2>
          <span className="pill">{book.online ? 'На связи' : 'Офлайн'}</span>
        </div>
        <Facts
          rows={[
            ['Узел', 'TAKHI-CENTRAL'],
            ['Последний обмен', stamp(book.lastSync)],
            ['Отправлено записей', book.sent],
            ['Ожидают отправки', book.pending.length],
          ]}
        />
        {book.syncError && <p className="field-error">{book.syncError}</p>}
        <div className="row-actions">
          <Toggle
            on={book.online}
            disabled={pref.isPending || sync.isPending}
            label="Связь с демо-узлом"
            onChange={() => pref.mutate({ online: !book.online })}
          />
          <Button
            disabled={sync.isPending || pref.isPending}
            onClick={() =>
              sync.mutate(undefined, {
                onSuccess: () => notice('Очередь синхронизирована.'),
                onError: (e) => notice(e.message, true),
              })
            }
          >
            <RefreshCw size={15} className={sync.isPending ? 'spin' : ''} />
            {book.syncError ? 'Повторить отправку' : 'Синхронизировать'}
          </Button>
        </div>
      </section>
      <section className="panel">
        <h2>Очередь отправки</h2>
        {book.pending.length ? (
          book.pending.map((id) => (
            <div key={id} className="list-row">
              <span>{book.observations.find((o) => o.id === id)?.name}</span>
              <small>Ожидает</small>
            </div>
          ))
        ) : (
          <p className="empty">Все локальные записи учтены.</p>
        )}
      </section>
    </>
  );
}
export function Settings({ book }: { book?: Notebook }) {
  const demo = useDemo();
  const setup = useModels(!demo && Boolean(book?.node));
  const [theme, setTheme] = useState(initialScheme);
  const [reset, setReset] = useState(false),
    pref = useCommand('/preferences'),
    clear = useCommand('/reset'),
    notice = useNotice();
  return (
    <>
      <Heading title="Настройки" description="Источник данных и оформление журнала." />
      <fieldset className="scheme-panel">
        <legend>Цветовая схема</legend>
        <div className="schemes">
          {schemes.map(({ id, label }) => (
            <label key={id}>
              <input
                type="radio"
                name="scheme"
                value={id}
                checked={theme === id}
                onChange={() => {
                  selectScheme(id);
                  setTheme(id);
                }}
              />
              <span className={'scheme-swatch ' + id} aria-hidden="true" />
              {label}
            </label>
          ))}
        </div>
      </fieldset>
      <section className="panel">
        <h2>Источник данных</h2>
        <div className="setting">
          <p>{demo ? 'Примеры и записи на этом устройстве' : 'Локальная нода · 127.0.0.1:8080'}</p>
          <Toggle label="Демосценарии" on={demo} onChange={() => setDemo(!demo)} />
        </div>
      </section>
      {!demo && setup.data?.mode === 'local' && <ModelPanel setup={setup.data} />}
      {demo && book ? (
        <>
          <section className="panel">
            <h2>Рабочее пространство</h2>
            <Facts
              rows={[
                ['Профиль', 'Полевой наблюдатель'],
                ['Локальный узел', 'TAKHI-FIELD-01'],
                ['Хранилище', 'IndexedDB · на этом устройстве'],
                ['Объём импортированных снимков', formatBytes(book.mediaBytes ?? 0)],
                ['Сертификат', 'Демонстрационный · доверенный'],
                ['Субъект сертификата', 'TAKHI-FIELD-01 · Demo CA'],
                ['Срок сертификата', '31 декабря 2028 г.'],
                ['Версия приложения', version],
              ]}
            />
          </section>
          <section className="panel">
            <h2>Демонстрационные сценарии</h2>
            <div className="setting">
              <div>
                <strong>Ошибка сохранения</strong>
                <p>Одна следующая попытка завершится ошибкой. Повторная попытка сохранит запись.</p>
              </div>
              <Toggle
                on={book.failNextSave}
                disabled={pref.isPending}
                label="Сбой один раз"
                onChange={() => pref.mutate({ failNextSave: !book.failNextSave })}
              />
            </div>
            <div className="setting">
              <div>
                <strong>Исходные данные</strong>
                <p>Восстановить примеры, удалить добавленные снимки и очистить очередь.</p>
              </div>
              <Button variant="outline" onClick={() => setReset(true)}>
                <RotateCcw size={15} />
                Сбросить демо
              </Button>
            </div>
          </section>
        </>
      ) : (
        !demo && (
          <section className="panel">
            <h2>Рабочее пространство</h2>
            <Facts
              rows={[
                ['Локальный узел', book?.node?.node_id ?? 'Нет соединения'],
                ['Хранилище', book?.node?.driver ?? '—'],
                ['Версия приложения', version],
              ]}
            />
          </section>
        )
      )}
      {reset && demo && (
        <Modal
          title="Восстановить исходные данные?"
          description="Ваши локальные снимки и решения будут удалены. В журнале останутся только исходные примеры."
          busy={clear.isPending}
          onClose={() => setReset(false)}
        >
          <div className="dialog-actions">
            <Button
              size="lg"
              variant="outline"
              disabled={clear.isPending}
              onClick={() => setReset(false)}
            >
              Отменить
            </Button>
            <Button
              size="lg"
              disabled={clear.isPending}
              onClick={() =>
                clear.mutate(undefined, {
                  onSuccess: () => {
                    setReset(false);
                    notice('Демонстрационные данные восстановлены.');
                  },
                  onError: (e) => notice(e.message, true),
                })
              }
            >
              Восстановить
            </Button>
          </div>
        </Modal>
      )}
    </>
  );
}
