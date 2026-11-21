import { useEffect, useRef, useState } from 'react';
import { Stage, Layer, Line, Circle, Rect, Text } from 'react-konva';
import { ArrowUpRight, Search } from 'lucide-react';
import type { Annotation, Photo } from '../../journal/types';
import { usePhotoURL } from '../../journal/media';

export function Thumbnail({
  photo,
  alt = '',
  className = '',
}: {
  photo: Photo;
  alt?: string;
  className?: string;
}) {
  const src = usePhotoURL(photo.source);
  return <img src={src} alt={alt} className={className} draggable={false} />;
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
  const src = usePhotoURL(photo.source);
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
  const picture = (
    <>
      <img
        src={src}
        alt={photo.name}
        onLoad={(e) =>
          setDimensions({
            url: src ?? '',
            w: e.currentTarget.naturalWidth,
            h: e.currentTarget.naturalHeight,
          })
        }
      />
      {(traits || highlight) && width > 0 && dimensions.url === src && (
        <div className="marks" aria-hidden="true">
          <Stage width={width} height={height} listening={false}>
            <Layer>
              {line.length > 0 && (
                <Line
                  points={line}
                  closed
                  stroke="#e4ed92"
                  strokeWidth={2}
                  fill="rgba(145,160,73,0.13)"
                  lineJoin="round"
                />
              )}
              {traits &&
                annotation.traits.map(({ label, at: [x, y] }, i) => {
                  const px = dx + x * w,
                    py = dy + y * h,
                    labelWidth = label.length * 7 + 14;
                  const tx = Math.min(
                    width - labelWidth - 3,
                    Math.max(3, px + (i % 2 ? 9 : -labelWidth - 9)),
                  );
                  const ty = Math.min(height - 20, Math.max(2, py - (i % 2 ? 23 : 13)));
                  return <LayerMark key={label} {...{ px, py, tx, ty, labelWidth, label }} />;
                })}
            </Layer>
          </Stage>
        </div>
      )}
      {onClick && (
        <span className="open-photo">
          <ArrowUpRight size={16} />
        </span>
      )}
    </>
  );
  return (
    <>
      <div className="marked" ref={box}>
        {onClick ? (
          <button aria-label="Выбрать фотографию кандидата" onClick={onClick}>
            {picture}
          </button>
        ) : (
          picture
        )}
      </div>
      {traits && annotation.traits.length > 0 && (
        <div className="trait-key">
          {annotation.traits.map((t) => (
            <span key={t.label}>
              <b>{t.label}</b> {t.value}
            </span>
          ))}
        </div>
      )}
    </>
  );
}
function LayerMark({
  px,
  py,
  tx,
  ty,
  labelWidth,
  label,
}: {
  px: number;
  py: number;
  tx: number;
  ty: number;
  labelWidth: number;
  label: string;
}) {
  return (
    <>
      <Line points={[px, py, tx + labelWidth / 2, ty + 10]} stroke="#e4ed92" strokeWidth={1} />
      <Circle x={px} y={py} radius={3.5} fill="#e4ed92" stroke="#374429" />
      <Rect x={tx} y={ty} width={labelWidth} height={19} cornerRadius={4} fill="#34432e" />
      <Text x={tx + 6} y={ty + 4} text={label} fontSize={11} fill="#fffef1" />
    </>
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
    [framing, setFraming] = useState('');
  const visible = photos.filter(
    (p) =>
      p.name.toLocaleLowerCase('ru').includes(search.toLocaleLowerCase('ru')) &&
      (!angle || p.angle === angle) &&
      (!framing || p.framing === framing),
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
        <select aria-label="Ракурс" value={angle} onChange={(e) => setAngle(e.target.value)}>
          <option value="">Все ракурсы</option>
          {[...new Set(photos.map((p) => p.angle))].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </select>
        <select aria-label="План" value={framing} onChange={(e) => setFraming(e.target.value)}>
          <option value="">Все планы</option>
          {[...new Set(photos.map((p) => p.framing))].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </select>
      </div>
      {visible.length ? (
        <div className="photo-grid">
          {visible.map((p) => (
            <button key={p.id} className="photo-card" onClick={() => onSelect(p)}>
              <Thumbnail photo={p} />
              <div>
                <strong>{p.name}</strong>
                <span className="tags">
                  <small>{p.angle}</small>
                  <small>{p.framing}</small>
                </span>
              </div>
            </button>
          ))}
        </div>
      ) : (
        <p className="empty">Фотографий по этим условиям нет.</p>
      )}
    </>
  );
}
