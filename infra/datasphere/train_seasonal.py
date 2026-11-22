"""Fit two chronological folds of the same residual descriptor on DataSphere."""
import json
from collections import Counter
from pathlib import Path

import numpy as np
import torch
from torch import nn
from torch.nn import functional as F

from seasonal_retrieval import SEASONS, evaluate, season

OUT = Path('seasonal')
OUT.mkdir(exist_ok=True)


def read(path):
    return json.loads(Path(path).read_text())


def save(name, obj):
    (OUT/name).write_text(json.dumps(obj, ensure_ascii=False, separators=(',', ':'), allow_nan=False))


class Adapter(nn.Module):
    def __init__(self, dim):
        super().__init__()
        self.down = nn.Linear(dim, 64, bias=False)
        self.up = nn.Linear(64, dim, bias=False)
        nn.init.zeros_(self.up.weight)

    def forward(self, v):
        return F.normalize(v + self.up(self.down(v)), dim=-1)


def fit(rows, vectors, epochs):
    torch.manual_seed(17)
    names = sorted({r['horse'] for r in rows})
    strong = [r for r in rows if r['target'] is not None]
    proto = F.normalize(torch.stack([vectors[[r['target_index'] for r in strong if r['horse'] == n]].mean(0)
                                    for n in names]))
    y = torch.tensor([names.index(r['horse']) for r in rows], device='cuda')
    indices = [([r['target_index']] if r['target'] is not None else r['indices']) for r in rows]
    width = max(map(len, indices))
    slots = torch.zeros((len(rows), width), dtype=torch.long, device='cuda')
    valid = torch.zeros_like(slots, dtype=torch.bool)
    for i, ind in enumerate(indices):
        slots[i,:len(ind)] = torch.tensor(ind, device='cuda')
        valid[i,:len(ind)] = True
    own = y[:,None,None].expand(-1, width, 1)
    scores = vectors[slots] @ proto.T
    margin = scores.gather(2, own).squeeze(2) - scores.scatter(2, own, -2.).amax(dim=2)
    attn = (16 * margin).masked_fill(~valid, -torch.inf).softmax(dim=1)
    keys = [(r['horse'], r['day'], (r.get('capture') or r['sha256'])[:16]) for r in rows]
    bursts = Counter(keys)
    days = Counter(key[:2] for key in bursts)
    individuals = Counter(key[0] for key in days)
    weights = []
    for r, key in zip(rows, keys):
        strength = 1. if r['target'] is not None else .35 if r['tier'] in ('blind','reviewed') else .15
        weights.append(strength / (bursts[key] * days[key[:2]] * individuals[key[0]])**.5)
    weights = torch.tensor(weights, device='cuda')
    head = Adapter(vectors.shape[1]).cuda()
    proxies = nn.Parameter(proto.clone())
    optimizer = torch.optim.AdamW([*head.parameters(), proxies], lr=.001)
    for epoch in range(1, epochs + 1):
        losses = []
        for ids in torch.randperm(len(rows), device='cuda').split(48):
            embeddings = head(vectors[slots[ids]])
            pooled = F.normalize((embeddings * attn[ids,:,None]).sum(1))
            logits = 18 * pooled @ F.normalize(proxies).T
            ce = F.cross_entropy(logits, y[ids], reduction='none')
            loss = (ce * weights[ids]).sum() / weights[ids].sum()
            optimizer.zero_grad(set_to_none=True)
            loss.backward()
            optimizer.step()
            losses.append(loss.detach())
        yield epoch, head, float(torch.stack(losses).mean())


def projected(head, vectors):
    with torch.no_grad():
        return head(vectors).cpu().numpy()


def main():
    torch.backends.cuda.matmul.allow_tf32 = False
    torch.backends.cudnn.allow_tf32 = False
    excluded = {r['sha256'] for r in read('exclusions.json')}
    duplicates = {r['sha256'] for r in read('duplicates.json')}
    evidence = {r['sha256']: r for r in read('identity-evidence.json')}
    rows = [r for r in read('features/index.json') if r['sha256'] not in excluded | duplicates]
    assert not (excluded|duplicates).intersection(evidence)
    assert len(rows) == len({r['sha256'] for r in rows})
    for r in rows:
        key = r['sha256']
        if key in evidence:
            label = evidence[key]
            assert r['split'] == 'train' and r['horse'] == label['horse'] and not r['target']
            r['target'] = label['target']
        if r['target']:
            r['target_index'] = r['indices'][r['regions'].index(r['target'])]
    assert evidence.keys() <= {r['sha256'] for r in rows}
    v = torch.from_numpy(np.load('features/features.npy')).cuda()
    dev = [r for r in rows if r['split'] == 'train']
    final = [r for r in rows if r['split'] in ('train','val')]
    val = [r for r in rows if r['split'] == 'val' and r['target']]
    test = [r for r in rows if r['split'] == 'test']
    assert all(r['target'] for r in test)
    assert max(r['day'] for r in dev) < min(r['day'] for r in val)
    assert max(r['day'] for r in final) < min(r['day'] for r in test)
    assert len(val) == 203 and len(test) == 358
    ranks, stats, banks, counts, matrices = {}, {}, {}, {}, {}
    for phase, train, queries in [('val',dev,val), ('test',final,test)]:
        gal = [r for r in train if r['target']]
        history = []
        best_score = -1
        for epoch, head, loss in fit(train, v, 16 if phase == 'val' else selected_epoch):
            record = {'epoch': epoch, 'loss': loss}
            if phase == 'val':
                report, *_ = evaluate(projected(head,v), gal, queries, include_ranks=False)
                score = report['recent5']['all']['macro_top1']
                record['val_macro_top1_recent5'] = score
                if score > best_score:
                    best_score = score
                    selected_epoch = epoch
                    best_weights = {k: p.detach().cpu().clone() for k,p in head.state_dict().items()}
            print(phase, record, flush=True)
            history.append(record)
        if phase == 'val':
            head.load_state_dict(best_weights)
        else:
            torch.save(dict(state_dict={k:p.detach().cpu() for k,p in head.state_dict().items()},
                            epoch=selected_epoch, revision=Path('features/revision.txt').read_text()), OUT/'adapter.pt')
        tuned = projected(head,v)
        report, predictions, banks[phase], counts[phase], matrices[phase] = evaluate(tuned,gal,queries)
        rankings_base, base_predictions, *_ = evaluate(v.cpu().numpy(),gal,queries)
        for q in queries:
            key = q['sha256']
            ranks[key] = dict(tuned=predictions[key], base=base_predictions[key])
        stats[phase] = dict(train=len(train), exact=len(gal), weak=len(train)-len(gal), queries=len(queries),
                            days=len({r['day'] for r in train}), selected_epoch=selected_epoch,
                            seasons={s: sum(season(r['day'])==s for r in train) for s in SEASONS},
                            tuned=report, base=rankings_base, history=history)
    save('ranks.json', ranks)
    save('reference-groups.json', banks)
    save('metrics.json', dict(phases=stats, counts=counts, cross=matrices, exclusions=len(excluded),
                              duplicates=len(duplicates), evidence=len(evidence), gpu=torch.cuda.get_device_name()))
    save('training-manifest.json', [dict(sha256=r['sha256'],horse=r['horse'],day=r['day'],capture=r.get('capture'),
        season=season(r['day']),target=r['target'],split=r['split'],image=r['image'],boxes=r['boxes'],
        identity_source=('passport_series' if r['sha256'] in evidence else 'v2_target' if r['target'] else 'photo_label'))
        for r in final])
    print('Complete', stats['test']['tuned']['recent5']['all'], flush=True)


if __name__ == '__main__':
    main()
