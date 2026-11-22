package domain

import (
	"fmt"
	"time"
)

// EventHorsePassportAttached — к лошади прикреплён PDF-паспорт. Сам файл лежит
// в content-addressed хранилище (ADR-0009), событие несёт только ссылку.
// Отдельный тип (а не horse.updated), чтобы паспорт проектировался и на пирах.
const EventHorsePassportAttached = "horse.passport_attached"

// PassportMimeType — паспорт хранится только как PDF.
const PassportMimeType = "application/pdf"

// Ключи Horse.Metadata, в которые проектируется паспорт.
const (
	MetaPassportSHA256     = "passport_sha256"
	MetaPassportFilename   = "passport_filename"
	MetaPassportSize       = "passport_size"
	MetaPassportAttachedAt = "passport_attached_at"
	MetaStudbookNumber     = "studbook_number"
)

// PassportAttached — payload события horse.passport_attached.
type PassportAttached struct {
	HorseID        string    `json:"horse_id"`
	SHA256         string    `json:"sha256"`
	MimeType       string    `json:"mime_type"`
	Size           int64     `json:"size"`
	Filename       string    `json:"filename,omitempty"`
	StudbookNumber string    `json:"studbook_number,omitempty"`
	AttachedAt     time.Time `json:"attached_at"`
}

// Metadata — ключи, которые паспорт записывает в Horse.Metadata.
// Номер по племкниге пишется только если указан, чтобы не затирать имеющийся.
func (p PassportAttached) Metadata() map[string]string {
	m := map[string]string{
		MetaPassportSHA256:     p.SHA256,
		MetaPassportFilename:   p.Filename,
		MetaPassportSize:       fmt.Sprint(p.Size),
		MetaPassportAttachedAt: p.AttachedAt.UTC().Format(time.RFC3339Nano),
	}
	if p.StudbookNumber != "" {
		m[MetaStudbookNumber] = p.StudbookNumber
	}
	return m
}

// SupersededBy сообщает, что в метаданных уже записан паспорт не старее этого:
// события с пиров приходят в произвольном порядке, побеждает последний по времени
// (при равенстве — больший sha256, чтобы все ноды сошлись к одному результату).
func (p PassportAttached) SupersededBy(meta map[string]string) bool {
	cur, err := time.Parse(time.RFC3339Nano, meta[MetaPassportAttachedAt])
	if err != nil {
		return false
	}
	at := p.AttachedAt.UTC()
	if cur.After(at) {
		return true
	}
	return cur.Equal(at) && meta[MetaPassportSHA256] >= p.SHA256
}
