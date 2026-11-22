# Паспорта лошадей (seed)

PDF-паспорта лошадей, 15 шт. Имя файла: `<номер в племкниге>-<кличка>-ru.pdf`.
Номер в имени файла совпадает с полем «Номер по племкниге» внутри паспорта —
его же передаём в `studbook_number` при загрузке.

| Файл | Кличка | Номер в племкниге |
|---|---|---|
| `7454-Amaga-ru.pdf`   | Амага   | 7454 |
| `7458-Azarm-ru.pdf`   | Азарм   | 7458 |
| `7633-Vitr-ru.pdf`    | Витр    | 7633 |
| `7669-Buda-ru.pdf`    | Буда    | 7669 |
| `7706-Berkut-ru.pdf`  | Беркут  | 7706 |
| `7838-Basya-ru.pdf`   | Бася    | 7838 |
| `8008-Stanley-ru.pdf` | Стэнли  | 8008 |
| `8288-Inei-ru.pdf`    | Иней    | 8288 |
| `8292-Rudy-ru.pdf`    | Руди    | 8292 |
| `8300-Storm-ru.pdf`   | Шторм   | 8300 |
| `8303-Vesna-ru.pdf`   | Весна   | 8303 |
| `8352-Asphea-ru.pdf`  | Асфея   | 8352 |
| `8481-Tigr-ru.pdf`    | Тигр    | 8481 |
| `8556-Tmin-ru.pdf`    | Тмин    | 8556 |
| `8977-Femida-ru.pdf`  | Фемида  | 8977 |

## Загрузка

`PUT /api/v1/horses/{id}/passport`, multipart: `file` (PDF, обязательно) и
`studbook_number` (номер по племкниге, необязательно). Ответ — JSON лошади,
у которой в `metadata` появляются `passport_sha256`, `passport_filename`,
`passport_size`, `passport_attached_at`.

```sh
API=http://127.0.0.1:8080
HORSE_ID=<uuid лошади>

curl -X PUT "$API/api/v1/horses/$HORSE_ID/passport" \
  -F file=@7454-Amaga-ru.pdf -F studbook_number=7454
```

Проверки на стороне сервера: файл обязан начинаться с `%PDF-` (иначе 415),
размер — не больше 50 MiB (иначе 413), лошадь должна существовать (иначе 404).

Посмотреть загруженный паспорт: `GET /api/v1/horses/{id}/passport`
(отдаётся `application/pdf`, поддерживается `ETag`/`If-None-Match`).

```sh
curl -o passport.pdf "$API/api/v1/horses/$HORSE_ID/passport"
```