package http

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// passport.go: PDF-паспорт лошади. Файл — обычный блоб (ADR-0009), ссылка на
// него — событие horse.passport_attached, проекция — ключи passport_* в metadata.

var pdfMagic = []byte("%PDF-")

// putHorsePassport — PUT /api/v1/horses/{id}/passport, multipart:
// file (PDF, обязательно), studbook_number (номер по племкниге, опционально).
func putHorsePassport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		current, err := d.Store.GetHorse(r.Context(), id)
		if err != nil {
			notFoundOrServerError(w, err, "horse")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBlobSize+(1<<20))
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "passport too large")
				return
			}
			writeError(w, http.StatusBadRequest, "multipart form required")
			return
		}
		if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
			writeError(w, http.StatusBadRequest, `exactly one PDF in field "file" required`)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, `field "file" required`)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxBlobSize+1))
		if err != nil {
			serverError(w, err)
			return
		}
		if len(data) > maxBlobSize {
			writeError(w, http.StatusRequestEntityTooLarge, "passport too large")
			return
		}
		// Проверяем содержимое, а не заявленный Content-Type: браузеры шлют что угодно.
		if !bytes.HasPrefix(data, pdfMagic) {
			writeError(w, http.StatusUnsupportedMediaType, "passport must be a PDF file")
			return
		}

		blob, _, err := registerBlob(r.Context(), d, data, domain.PassportMimeType)
		if err != nil {
			serverError(w, err)
			return
		}
		passport := domain.PassportAttached{
			HorseID:        id,
			SHA256:         blob.SHA256,
			MimeType:       domain.PassportMimeType,
			Size:           blob.Size,
			Filename:       cleanFilename(header.Filename),
			StudbookNumber: strings.TrimSpace(r.FormValue("studbook_number")),
			AttachedAt:     time.Now().UTC(),
		}
		var horse domain.Horse
		err = appendWithProjection(d, r.Context(), domain.EventHorsePassportAttached, id, mustJSON(passport),
			func(tx *sqlstore.Store) error {
				// Лошадь уже прочитана в начале запроса — второй SELECT не нужен.
				h, err := tx.UpdateHorseFrom(r.Context(), current, nil, passport.Metadata())
				horse = h
				return err
			})
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "horse not found")
				return
			}
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, horse)
	}
}

// getHorsePassport — GET /api/v1/horses/{id}/passport: PDF для просмотра в браузере.
func getHorsePassport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-cache")
		h, err := d.Store.GetHorse(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			notFoundOrServerError(w, err, "horse")
			return
		}
		sha := h.Metadata[domain.MetaPassportSHA256]
		if sha == "" {
			writeError(w, http.StatusNotFound, "passport not found")
			return
		}
		data, err := d.Blobs.Get(r.Context(), sha)
		if err != nil {
			// на пире событие могло приехать раньше файла (неполный blob-синк)
			notFoundOrServerError(w, err, "passport")
			return
		}
		// Без CSP sandbox, как у /blobs: в песочнице Chromium не открывает
		// встроенный просмотрщик PDF. Содержимое проверено при загрузке и
		// отдаётся строго как application/pdf с nosniff.
		w.Header().Set("Content-Type", domain.PassportMimeType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", contentDisposition(h.Metadata[domain.MetaPassportFilename]))
		w.Header().Set("ETag", `"`+sha+`"`)
		if match := r.Header.Get("If-None-Match"); match == `"`+sha+`"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// cleanFilename оставляет только имя файла без пути (Windows/Unix) и ограничивает длину.
func cleanFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" {
		return ""
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}

// contentDisposition: inline + ASCII-запасное имя и UTF-8 имя по RFC 6266/5987
// (имена паспортов бывают кириллицей).
func contentDisposition(name string) string {
	if name == "" {
		name = "passport.pdf"
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return `inline; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(name)
}
