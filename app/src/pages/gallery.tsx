import { useEffect, useState } from 'react';
import { ArrowUpRight, ChevronLeft, ChevronRight, Search } from 'lucide-react';
import { useHorseHistory } from '../journal/use-journal';
import { describeTraits, horsePhotos, type Notebook, type Photo } from '../journal/types';
import { Heading, Back, Modal, Toggle } from '../shared/ui/journal-ui';
import { MarkedPhoto, PhotoGrid, Thumbnail } from '../shared/ui/photos';
export function Gallery({ book }: { book: Notebook }) {
  const [selected, setSelected] = useState<string | null>(null),
    [search, setSearch] = useState('');
  const [active, setActive] = useState<number | null>(null);
  const horse = book.horses.find((h) => h.id === selected);
  const history = useHorseHistory(book, horse?.id);
  const observations = history.data ?? book.observations.filter((o) => o.horseId === horse?.id);
  const photos = observations.flatMap((o) => o.photos);
  if (horse)
    return (
      <>
        <Back onClick={() => setSelected(null)}>Назад в галерею</Back>
        <Heading
          title={horse.name}
          description={
            'Наблюдения особи · ' + book.observations.filter((o) => o.horseId === horse.id).length
          }
        />
        {history.isError && (
          <p role="alert">
            Не удалось загрузить историю.{' '}
            <button className="text-link" onClick={() => void history.refetch()}>
              Повторить
            </button>
          </p>
        )}
        <PhotoGrid photos={photos} onSelect={(photo) => setActive(photos.indexOf(photo))} />
        {observations
          .filter((o) => !o.photos.length)
          .map((o) => (
            <div className="list-row" key={o.id}>
              <strong>{o.name}</strong>
              <span>{describeTraits(o.traits)}</span>
            </div>
          ))}
        {active !== null && (
          <Carousel
            photos={photos}
            index={active}
            setIndex={setActive}
            close={() => setActive(null)}
          />
        )}
      </>
    );
  const visible = book.horses.filter((h) => h.name.toLowerCase().includes(search.toLowerCase()));
  return (
    <>
      <Heading
        title="Галерея особей"
        description="Фотографии, признаки и история полевых наблюдений каждой лошади."
      />
      <div className="filters">
        <label className="search">
          <Search size={16} />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Поиск особи"
            placeholder="Найти особь по имени"
          />
        </label>
        <small>{book.horses.length} в каталоге</small>
      </div>
      <div className="herd-grid">
        {visible.map((h) => {
          const images = horsePhotos(book, h.id);
          return (
            <button key={h.id} className="herd-card" onClick={() => setSelected(h.id)}>
              {images[0] && <Thumbnail photo={images[0]} />}
              <div className="sample-info">
                <div>
                  <strong>{h.name}</strong>
                  <small>Снимков · {images.length}</small>
                </div>
                <span className="round-arrow">
                  <ArrowUpRight size={18} />
                </span>
              </div>
            </button>
          );
        })}
      </div>
      {!visible.length && <p className="empty">Особей с таким именем нет.</p>}
    </>
  );
}
export function Carousel({
  photos,
  index,
  setIndex,
  close,
}: {
  photos: Photo[];
  index: number;
  setIndex: (index: number) => void;
  close: () => void;
}) {
  const [traits, setTraits] = useState(false);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight') {
        e.preventDefault();
        setIndex(Math.min(photos.length - 1, index + 1));
      }
      if (e.key === 'ArrowLeft') {
        e.preventDefault();
        setIndex(Math.max(0, index - 1));
      }
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [index, photos.length, setIndex]);
  const photo = photos[index]!;
  return (
    <Modal fullscreen title={photo.name} onClose={close}>
      <div className="carousel">
        <MarkedPhoto photo={photo} traits={traits} />
        <button
          className="car-prev"
          aria-label="Предыдущее фото"
          disabled={!index}
          onClick={() => setIndex(index - 1)}
        >
          <ChevronLeft />
        </button>
        <button
          className="car-next"
          aria-label="Следующее фото"
          disabled={index === photos.length - 1}
          onClick={() => setIndex(index + 1)}
        >
          <ChevronRight />
        </button>
      </div>
      <div className="carousel-info">
        <Toggle label="Показать черты" on={traits} onChange={() => setTraits((v) => !v)} />
        <span className="tags">
          <small>{photo.angle}</small>
          <small>{photo.framing}</small>
        </span>
        <span>
          Фото {index + 1}/{photos.length}
        </span>
      </div>
    </Modal>
  );
}
