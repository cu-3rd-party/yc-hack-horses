"""Calendar-season retrieval and fixed-gallery evaluation on unit crop embeddings."""
from collections import Counter, defaultdict
from datetime import date

import numpy as np

SEASONS = ('winter', 'spring', 'summer', 'autumn')
MODES = ('recent5', 'season', 'whole', 'nearest')


def season(day):
    return SEASONS[date.fromisoformat(day).month % 12 // 3] if day else None


def recent_key(row):
    # EXIF clock time is local. ISO day is sufficient when time is unknown.
    return (row['day'], row.get('capture') or '', (row.get('subsec') or '').ljust(12, '0'))


def unit(v):
    norm = np.linalg.norm(v)
    if norm <= 1e-12 or not np.isfinite(norm):
        raise ValueError('Degenerate descriptor centroid')
    return v / norm


def reference_groups(gallery):
    grouped = defaultdict(list)
    for r in gallery:
        grouped[r['horse']].append(r)
    groups = {}
    for name, rows in sorted(grouped.items()):
        # Stable SHA ascending breaks ties in capture timestamps.
        ordered = sorted(sorted(rows, key=lambda r: r['sha256']), key=recent_key, reverse=True)
        by_season = {s: [r for r in ordered if season(r['day']) == s] for s in SEASONS}
        groups[name] = (ordered, by_season)
    return groups


def select_refs(group, query_season, mode):
    all_refs, by_season = group
    same = by_season.get(query_season, [])
    if mode == 'recent5':
        return (same[:5], False) if same else ([r for s in SEASONS for r in by_season[s][:5]], True)
    if mode == 'season':
        return (same, False) if same else (all_refs, True)
    return all_refs, False


def metrics(records):
    if not records:
        return {'queries': 0}
    n = len(records)
    count = Counter(name for name, _, _ in records)
    return dict(queries=n, horses=len(count), top1=sum(rank == 1 for _, rank, _ in records)/n,
                top3=sum(rank <= 3 for _, rank, _ in records)/n, correct=sum(rank == 1 for _, rank, _ in records),
                macro_top1=sum((rank == 1)/count[name] for name, rank, _ in records)/len(count),
                median_rank=float(np.median([rank for _, rank, _ in records])),
                true_season_missing=sum(fallback for _, _, fallback in records))


def evaluate(vectors, gallery, queries, include_ranks=True):
    assert {r['sha256'] for r in gallery}.isdisjoint(r['sha256'] for r in queries)
    assert max(r['day'] for r in gallery) < min(r['day'] for r in queries)
    groups = reference_groups(gallery)
    names = list(groups)
    recent = {name: {s: [r['sha256'] for r in seasons[s][:5]] for s in SEASONS}
              for name, (_, seasons) in groups.items()}
    rankings = {q['sha256']: {} for q in queries}
    report = {}
    cross = defaultdict(lambda: defaultdict(list))
    for mode in MODES:
        records = defaultdict(list)
        for s in SEASONS:
            qrows = [r for r in queries if season(r['day']) == s]
            if not qrows:
                continue
            qmat = vectors[[r['target_index'] for r in qrows]]
            columns = []
            examples = []
            counts = []
            fallback = []
            for name, group in groups.items():
                refs, missing = select_refs(group, s, mode)
                mat = vectors[[r['target_index'] for r in refs]]
                centroid = unit(mat.mean(axis=0))
                if mode == 'nearest':
                    sim = qmat @ mat.T
                    best = sim.argmax(axis=1)
                    columns.append(sim.max(axis=1))
                    examples.append([refs[i]['sha256'] for i in best])
                else:
                    columns.append(qmat @ centroid)
                    example = refs[int(np.argmax(mat @ centroid))]['sha256']
                    examples.append([example] * len(qrows))
                counts.append(len(refs))
                fallback.append(missing)
                if mode == 'recent5':
                    own = [i for i, r in enumerate(qrows) if r['horse'] == name]
                    for gseason, keys in group[1].items():
                        if keys and own:
                            vec = unit(vectors[[r['target_index'] for r in keys[:5]]].mean(axis=0))
                            cross[s, gseason][name].extend((qmat[own] @ vec).tolist())
            values = np.stack(columns, axis=1)
            order = np.argsort(-values, axis=1, kind='stable')
            for i, q in enumerate(qrows):
                actual = names.index(q['horse'])
                position = int(np.flatnonzero(order[i] == actual)[0]) + 1
                records[s].append((q['horse'], position, fallback[actual]))
                if include_ranks:
                    rankings[q['sha256']][mode] = [[names[j], round(float(values[i,j]), 6),
                        examples[j][i], counts[j], fallback[j]] for j in order[i,:5]]
        report[mode] = {'all': metrics([r for items in records.values() for r in items]),
                        'seasons': {s: metrics(records[s]) for s in SEASONS}}
    matrix = {}
    for s in SEASONS:
        matrix[s] = {}
        for gseason in SEASONS:
            group = cross[s, gseason]
            matrix[s][gseason] = {'horses': len(group), 'queries': sum(map(len, group.values())),
                'cosine': float(np.mean([np.mean(v) for v in group.values()])) if group else None}
    counts = {n: {s: len(v) for s,v in g[1].items()} for n,g in groups.items()}
    return report, rankings, recent, counts, matrix
