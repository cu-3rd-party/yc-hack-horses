import { useEffect, useRef, useState } from 'react';
import { Stage, Layer, Group, Line, Circle, Rect, Text } from 'react-konva';
import { Aperture, FileText, Frame, ScanEye, Search, X, type LucideIcon } from 'lucide-react';
import type { Annotation, Photo, Point } from '../../journal/types';
import { photoQuality } from '../../journal/fixtures';
import { usePhotoURL } from '../../journal/media';
import { Select } from './select';

export function Thumbnail({
  photo,
  alt = '',
  className = '',
}: {
  photo: Photo;
  alt?: string;
  className?: string;
}) {
  const { src, onError } = usePhotoURL(photo.source);
  return <img src={src} onError={onError} alt={alt} className={className} draggable={false} />;
}
export function MarkedPhoto({
  photo,
  annotation = photo.annotation,
  traits = true,
  highlight = false,
  onClick,
}: {
  photo: Photo;
  annotation?: Annotation;
  traits?: boolean;
  highlight?: boolean;
  onClick?: () => void;
}) {
  const { src, onError } = usePhotoURL(photo.source);
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState([0, 0]);
  const [dimensions, setDimensions] = useState({ url: '', w: 0, h: 0 });
  useEffect(() => {
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setSize([entry.contentRect.width, entry.contentRect.height]);
    });
    if (box.current) observer.observe(box.current);
    return () => observer.disconnect();
  }, []);
  const [width = 0, height = 0] = size;
  const scale = Math.min(width / (dimensions.w || 1), height / (dimensions.h || 1));
  const w = dimensions.w * scale,
    h = dimensions.h * scale,
    dx = (width - w) / 2,
    dy = (height - h) / 2;
  const line = annotation.outline.flatMap(([x, y]) => [x * w + dx, y * h + dy]);
  const labels = annotation.traits.map(({ label, value }) => {
    if (/^[-−]?\d/.test(value)) return label + ' ' + value;
    const phrase = value.trim() + ' ' + label.toLocaleLowerCase('ru');
    return phrase.charAt(0).toLocaleUpperCase('ru') + phrase.slice(1);
  });
  const marks =
    traits && w > 0 && h > 0
      ? placeTraitLabels(annotation, labels, { x: dx, y: dy, width: w, height: h })
      : [];
  const picture = (
    <>
      <img
        src={src}
        onError={onError}
        alt={traits && labels.length ? photo.name + '. ' + labels.join(', ') : photo.name}
        onLoad={(e) =>
          setDimensions({
            url: src ?? '',
            w: e.currentTarget.naturalWidth,
            h: e.currentTarget.naturalHeight,
          })
        }
      />
      {traits && annotation.traits.some((t) => !t.at) && (
        <span className="unlocated-traits">
          {annotation.traits.flatMap((t, i) => (t.at ? [] : [labels[i]])).join(' · ')}
        </span>
      )}
      {(traits || highlight) && width > 0 && dimensions.url === src && (
        <div className="marks" aria-hidden="true">
          <Stage width={width} height={height} listening={false}>
            <Layer>
              {line.length > 0 && (
                <Line
                  points={line}
                  closed
                  stroke="#ffffff"
                  strokeWidth={2}
                  shadowColor="#101010"
                  shadowBlur={2}
                  shadowOpacity={0.9}
                  fill="rgba(255,255,255,0.09)"
                  lineJoin="round"
                />
              )}
              {traits &&
                annotation.traits.map(({ polygon, label }, i) =>
                  polygon?.length ? (
                    <Line
                      key={label + i}
                      points={polygon.flatMap(([x, y]) => [dx + x * w, dy + y * h])}
                      closed
                      stroke="white"
                      strokeWidth={1.5}
                      fill="rgba(255,255,255,0.14)"
                    />
                  ) : null,
                )}
              {marks.map(({ label, px, py, tx, ty, width }) => (
                <Line
                  key={label}
                  points={[px, py, tx + width / 2, ty + 10]}
                  stroke="#fff"
                  strokeWidth={1}
                />
              ))}
              {marks.map(({ label, px, py, tx, ty, width }) => (
                <Group key={label}>
                  <Circle x={px} y={py} radius={3.5} fill="#fff" stroke="#242424" />
                  <Rect x={tx} y={ty} width={width} height={19} cornerRadius={4} fill="#222" />
                  <Text
                    x={tx + 6}
                    y={ty + 4}
                    width={width - 10}
                    wrap="none"
                    ellipsis
                    text={label}
                    fontFamily="Inter"
                    fontSize={11}
                    fill="#fff"
                  />
                </Group>
              ))}
            </Layer>
          </Stage>
        </div>
      )}
    </>
  );
  return (
    <>
      <div className="marked" ref={box}>
        {onClick ? (
          <button
            aria-label="Выбрать фотографию кандидата"
            aria-description={traits ? labels.join(', ') : undefined}
            onClick={onClick}
          >
            {picture}
          </button>
        ) : (
          picture
        )}
      </div>
    </>
  );
}
interface TraitMark {
  label: string;
  px: number;
  py: number;
  tx: number;
  ty: number;
  width: number;
}
const textMeasure = document.createElement('canvas').getContext('2d')!;
function placeTraitLabels(
  annotation: Annotation,
  labels: string[],
  image: { x: number; y: number; width: number; height: number },
) {
  const region: Point[] = annotation.outline.length
    ? annotation.outline
    : [
        [0, 0],
        [1, 1],
      ];
  const xs = region.map(([x]) => image.x + x * image.width);
  const ys = region.map(([, y]) => image.y + y * image.height);
  const left = Math.min(...xs),
    right = Math.max(...xs);
  const top = Math.min(...ys),
    bottom = Math.max(...ys);
  const placed: TraitMark[] = [];
  textMeasure.font = '11px Inter';
  const points = annotation.traits
    .flatMap((trait, i) => (trait.at ? [{ at: trait.at, label: labels[i]! }] : []))
    .sort((a, b) => a.at[1] - b.at[1]);
  for (const {
    at: [x, y],
    label,
  } of points) {
    const px = image.x + x * image.width,
      py = image.y + y * image.height;
    const width = Math.min(image.width, Math.ceil(textMeasure.measureText(label).width) + 12);
    const tx = Math.min(
      image.x + image.width - width,
      Math.max(left, Math.min(px + 8, right - width)),
    );
    // Prefer the anchor's nearest free row inside this horse's detection.
    const candidates = [py - 21, py + 5, ...placed.flatMap((p) => [p.ty + 21, p.ty - 21])].map(
      (v) => Math.max(top, Math.min(v, bottom - 19)),
    );
    const ty =
      candidates.find((v) =>
        placed.every(
          (p) => Math.abs(p.ty - v) >= 20 || tx >= p.tx + p.width + 2 || p.tx >= tx + width + 2,
        ),
      ) ?? candidates[0]!;
    placed.push({ label, px, py, tx, ty, width });
  }
  return placed;
}
export function PhotoTile({
  photo,
  name,
  tags,
  detail,
  onClick,
}: {
  photo?: Photo;
  name: string;
  tags: string[];
  detail?: string;
  onClick: () => void;
}) {
  return (
    <button className="photo-card" onClick={onClick}>
      {photo ? (
        <Thumbnail photo={photo} />
      ) : (
        <span className="no-photo">
          <FileText size={28} />
          Запись без фото
        </span>
      )}
      <div>
        <strong>{name}</strong>
        {detail && <span className="tile-detail">{detail}</span>}
        <span className="tags">
          {tags.map((tag, i) => (
            <small key={i}>{tag}</small>
          ))}
        </span>
      </div>
    </button>
  );
}
function PhotoFilter({
  label,
  all,
  icon: Icon,
  options,
  value,
  onChange,
}: {
  label: string;
  all: string;
  icon: LucideIcon;
  options: string[];
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <Select
      label={label}
      filterIcon={Icon}
      value={value}
      onChange={onChange}
      options={[
        { value: '', label: all },
        ...Array.from(new Set(options), (option) => ({ value: option, label: option })),
      ]}
    />
  );
}
export function PhotoGrid({
  photos,
  onSelect,
}: {
  photos: Photo[];
  onSelect: (photo: Photo) => void;
}) {
  const [search, setSearch] = useState(''),
    [angle, setAngle] = useState(''),
    [framing, setFraming] = useState(''),
    [quality, setQuality] = useState('');
  const visible = photos.filter(
    (p) =>
      p.name.toLocaleLowerCase('ru').includes(search.toLocaleLowerCase('ru')) &&
      (!angle || p.angle === angle) &&
      (!framing || p.framing === framing) &&
      (!quality || photoQuality(p) === quality),
  );
  return (
    <>
      <div className="filters">
        <label className="search">
          <Search size={16} />
          <input
            aria-label="Поиск по названию"
            placeholder="Поиск по названию"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <PhotoFilter
          label="Ракурс"
          all="Все ракурсы"
          icon={ScanEye}
          options={photos.map((p) => p.angle)}
          value={angle}
          onChange={setAngle}
        />
        <PhotoFilter
          label="План"
          all="Все планы"
          icon={Frame}
          options={photos.map((p) => p.framing)}
          value={framing}
          onChange={setFraming}
        />
        <PhotoFilter
          label="Качество"
          all="Любое качество"
          icon={Aperture}
          options={photos.map(photoQuality)}
          value={quality}
          onChange={setQuality}
        />
        {(angle || framing || quality) && (
          <button
            type="button"
            className="filter-clear"
            aria-label="Сбросить фильтры"
            title="Сбросить фильтры"
            onClick={() => {
              setAngle('');
              setFraming('');
              setQuality('');
            }}
          >
            <X size={16} />
          </button>
        )}
      </div>
      {visible.length ? (
        <div className="photo-grid">
          {visible.map((p) => (
            <PhotoTile
              key={p.id}
              name={p.name}
              photo={p}
              tags={[p.angle, p.framing]}
              onClick={() => onSelect(p)}
            />
          ))}
        </div>
      ) : (
        <p className="empty">Фотографий по этим условиям нет.</p>
      )}
    </>
  );
}
