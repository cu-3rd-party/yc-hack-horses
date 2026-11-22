package sync

import (
	"context"
	"sort"
)

// NeededBlobSource — источник потребностей blob-синка: sha256, на которые
// ссылаются активные наблюдения этой ноды. Реализуется sqlstore.Store.
type NeededBlobSource interface {
	// ListActiveReferencedBlobHashes отдаёт sha256 активных (не инвалидированных)
	// наблюдений; инвалидированные в список не входят, а ссылки blobs.id уже
	// приведены к sha256.
	ListActiveReferencedBlobHashes(ctx context.Context) ([]string, error)
}

// ReferencedBlobHashes — список потребностей неполного blob-синка: уникальные
// sha256 активных наблюдений в стабильном порядке.
//
// Инвалидированные наблюдения (invalidated_at != 0, фото удалено событием
// blob.deleted) сюда не попадают: их записи остаются в истории как аудит, но
// файлы выпали из сравнения, поиска и инференса. Если бы их blob_ids
// оставались в потребностях, каждый пир тянул бы с сети мёртвые файлы, а
// лимит MaxBlobsPerSync за цикл вытеснял бы живые. Хэш, на который ссылается
// хоть одно активное наблюдение, остаётся: множество строится по всем живым
// строкам, поэтому общие с инвалидированными хэши не теряются.
//
// Файлы инвалидированных наблюдений на нодах, где они уже лежали, не прунятся
// (их держит BlobReferenced), но по сети не доезжают — и из истории фото уже не
// достать: метаданные блоба погашены, GET /api/v1/blobs/<sha256> отвечает 404.
func ReferencedBlobHashes(ctx context.Context, src NeededBlobSource) ([]string, error) {
	hashes, err := src.ListActiveReferencedBlobHashes(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(hashes))
	out := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}
