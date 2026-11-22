package http

import (
	"net/http"

	"gopkg.in/yaml.v3"
)

// docs.go: Scalar Swagger UI и OpenAPI-спека (флаг Deps.DocsEnabled, по
// умолчанию выключен — документация публикуется только на central-ноде).
//
// Подход: спека описана декларативно в Go (структуры ниже) и маршалится
// yaml.v3 — без кодогенерации и без новых зависимостей. Обоснование выбора
// в PR_DESCRIPTION-scalar.md в корне репозитория. Согласованность спеки с
// фактическими маршрутами проверяется тестом (docs_test.go).

// Scalar-оболочка — браузерный UI c CDN; саму спеку UI забирает с локального
// эндпоинта /api/scalar/openapi.yaml, сеть наружу спека не ходит.
const scalarPage = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>yc-hack-horses API — Scalar</title>
  <style>body { margin: 0; }</style>
</head>
<body>
  <!-- Спека всегда локальная: UI — только браузерная оболочка с CDN -->
  <script id="api-reference" data-url="/api/scalar/openapi.yaml"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>
`

// --- описательные структуры OpenAPI 3.0 (yaml.v3 сохраняет порядок полей) ---

type openAPISpec struct {
	OpenAPI string              `yaml:"openapi"`
	Info    openAPIInfo         `yaml:"info"`
	Servers []map[string]string `yaml:"servers"`
	Paths   map[string]pathItem `yaml:"paths"`
	// Schemas — компоненты; в spec собирается в components.schemas.
	Schemas map[string]schemaNode `yaml:"-"`
}

type openAPIInfo struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
}

type pathItem struct {
	Ref       string     `yaml:"$ref,omitempty"`
	Summary   string     `yaml:"summary,omitempty"`
	Get       *operation `yaml:"get,omitempty"`
	Post      *operation `yaml:"post,omitempty"`
	Put       *operation `yaml:"put,omitempty"`
	Patch     *operation `yaml:"patch,omitempty"`
	Delete    *operation `yaml:"delete,omitempty"`
	WebSocket string     `yaml:"x-websocket,omitempty"`
}

type operation struct {
	Summary     string          `yaml:"summary"`
	Description string          `yaml:"description,omitempty"`
	Tags        []string        `yaml:"tags,omitempty"`
	OperationID string          `yaml:"operationId"`
	Parameters  []parameter     `yaml:"parameters,omitempty"`
	RequestBody *requestBody    `yaml:"requestBody,omitempty"`
	Responses   map[string]resp `yaml:"responses"`
}

type parameter struct {
	Name        string     `yaml:"name"`
	In          string     `yaml:"in"`
	Required    bool       `yaml:"required,omitempty"`
	Description string     `yaml:"description,omitempty"`
	Schema      schemaNode `yaml:"schema"`
}

type requestBody struct {
	Description string                    `yaml:"description,omitempty"`
	Required    bool                      `yaml:"required"`
	Content     map[string]mediaTypeValue `yaml:"content"`
}

type mediaTypeValue struct {
	Schema  schemaNode `yaml:"schema,omitempty"`
	Example any        `yaml:"example,omitempty"`
}

type resp struct {
	Description string                    `yaml:"description"`
	Headers     map[string]headerNode     `yaml:"headers,omitempty"`
	Content     map[string]mediaTypeValue `yaml:"content,omitempty"`
}

type headerNode struct {
	Description string     `yaml:"description,omitempty"`
	Schema      schemaNode `yaml:"schema"`
}

// schemaNode — произвольный узел JSON-Schema (типы собираются вручную, чтобы
// не зависеть от библиотеки схем и точно описать фактические ответы хендлеров).
type schemaNode map[string]any

func (s schemaNode) prop(name string, def any) schemaNode {
	props, ok := s["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
		s["properties"] = props
	}
	props[name] = def
	return s
}

func (s schemaNode) required(names ...string) schemaNode {
	req := []any{}
	if existing, ok := s["required"].([]any); ok {
		req = existing
	}
	for _, n := range names {
		req = append(req, n)
	}
	s["required"] = req
	return s
}

func strSchema(description string) schemaNode {
	return schemaNode{"type": "string", "description": description}
}

// --- построение спеки ---

// buildOpenAPISpec собирает документ OpenAPI 3.0 по фактическим маршрутам
// NewRouter. При изменении маршрутов правится и здесь, и в docs_test.go.
func buildOpenAPISpec() []byte {
	spec := openAPISpec{
		OpenAPI: "3.0.3",
		Info: openAPIInfo{
			Title: "yc-hack-horses API",
			Description: "REST/WS API узла реестра лошадей Пржевальского. " +
				"Запись любой модификации — событие в журнал + проекция в одной транзакции. " +
				"Признаки (trait_snapshots) — human-in-the-loop: предсказание движка требует подтверждения человеком.",
			Version: "1.0.0",
		},
		Servers: []map[string]string{{"url": "/", "description": "текущая нода"}},
		Paths:   map[string]pathItem{},
		Schemas: map[string]schemaNode{},
	}
	schemas := spec.Schemas

	jsonContent := func(schema schemaNode) map[string]mediaTypeValue {
		return map[string]mediaTypeValue{"application/json": {Schema: schema}}
	}
	errorRef := jsonContent(schemaNode{"$ref": "#/components/schemas/Error"})

	// Добавить операцию для пути; пути пишем в абсолютном виде, включая префикс /api/v1.
	add := func(path, method, summary, opID, tag, desc string) *operation {
		item := spec.Paths[path]
		op := &operation{
			Summary:     summary,
			Description: desc,
			OperationID: opID,
			Tags:        []string{tag},
			Responses:   map[string]resp{},
		}
		switch method {
		case "get":
			item.Get = op
		case "post":
			item.Post = op
		case "put":
			item.Put = op
		case "patch":
			item.Patch = op
		case "delete":
			item.Delete = op
		}
		spec.Paths[path] = item
		return op
	}
	okJSON := func(schema schemaNode) resp {
		return resp{Description: "OK", Content: jsonContent(schema)}
	}
	createdJSON := func(schema schemaNode) resp {
		return resp{Description: "Created", Content: jsonContent(schema)}
	}
	stdErrors := func(need404 bool) map[string]resp {
		out := map[string]resp{
			"400": {Description: "невалидный JSON или параметры", Content: errorRef},
			"500": {Description: "внутренняя ошибка", Content: errorRef},
		}
		if need404 {
			out["404"] = resp{Description: "не найдено", Content: errorRef}
		}
		return out
	}
	idParam := func(name, desc string) parameter {
		return parameter{
			Name: name, In: "path", Required: true, Description: desc,
			Schema: strSchema("UUID или sha256 — идентификатор ресурса"),
		}
	}
	// sha256Param — path-параметр именно hex-sha256 содержимого блоба
	// (не общий idParam: у /api/v1/blobs/{sha256} ключ строго sha256).
	sha256Param := func(name, desc string) parameter {
		return parameter{
			Name: name, In: "path", Required: true, Description: desc,
			Schema: strSchema("hex sha256 содержимого"),
		}
	}

	// --- health/readyz ---
	add("/health", "get", "Проверка живости", "health", "service", "").
		Responses = map[string]resp{"200": okJSON(schemaNode{"type": "object"}.prop("status", strSchema("always \"ok\"")))}

	add("/readyz", "get", "Готовность (БД)", "readyz", "service",
		"200 — хранилище отвечает; 503 — БД недоступна.").
		Responses = map[string]resp{
		"200": okJSON(schemaNode{"type": "object"}.prop("status", strSchema("\"ready\""))),
		"503": {Description: "БД недоступна", Content: errorRef},
	}

	// --- horses ---
	listHorses := add("/api/v1/horses", "get", "Список лошадей", "listHorses", "horses",
		"Без limit/cursor — весь список. С пагинацией — усечённый список; продолжение помечается заголовком X-Next-Cursor (непрозрачный курсор).")
	listHorses.Parameters = []parameter{
		{Name: "limit", In: "query", Schema: strSchema("Размер страницы, по умолчанию 1000, максимум 5000; задаёт курсорную пагинацию.")},
		{Name: "cursor", In: "query", Schema: strSchema("Курсор продолжения из X-Next-Cursor предыдущей страницы.")},
	}
	listHorses.Responses = map[string]resp{"200": okJSON(schemaNode{"type": "array", "items": schemaNode{"$ref": "#/components/schemas/Horse"}})}

	createHorse := add("/api/v1/horses", "post", "Создать лошадь", "createHorse", "horses",
		"Возвращает 201 и созданную лошадь, Location — путь нового ресурса.")
	createHorse.RequestBody = &requestBody{
		Description: "metadata опционален (карта ключ/значение).",
		Required:    false,
		Content: jsonContent(schemaNode{"type": "object"}.
			prop("metadata", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "string"}})),
	}
	createHorse.Responses = merge(stdErrors(false), map[string]resp{
		"201": {Description: "создано", Content: jsonContent(schemaNode{"$ref": "#/components/schemas/Horse"})},
	})

	horseItem := add("/api/v1/horses/{id}", "get", "Получить лошадь", "getHorse", "horses", "")
	horseItem.Parameters = []parameter{idParam("id", "UUID лошади")}
	horseItem.Responses = merge(stdErrors(true), map[string]resp{
		"200": okJSON(schemaNode{"$ref": "#/components/schemas/Horse"}),
	})

	patchHorse := add("/api/v1/horses/{id}", "patch", "Правка лошади", "patchHorse", "horses",
		"identity_status: active | merged | unresolved; metadata передаётся целиком (карта заменяется).")
	patchHorse.Parameters = []parameter{idParam("id", "UUID лошади")}
	patchHorse.RequestBody = &requestBody{
		Description: "Поле identity_status и/или metadata.",
		Required:    true,
		Content: jsonContent(schemaNode{"type": "object"}.
			prop("identity_status", strSchema("active | merged | unresolved")).
			prop("metadata", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "string"}})),
	}
	patchHorse.Responses = merge(stdErrors(true), map[string]resp{
		"200": okJSON(schemaNode{"$ref": "#/components/schemas/Horse"}),
	})

	putPassport := add("/api/v1/horses/{id}/passport", "put", "Загрузить PDF-паспорт лошади", "putHorsePassport", "horses",
		"multipart/form-data: file — PDF до 50 MiB (проверяется сигнатура %PDF-), studbook_number — номер "+
			"по племкниге (опционально). Файл сохраняется как блоб, пишется событие horse.passport_attached, "+
			"в metadata лошади появляются passport_sha256, passport_filename, passport_size, passport_attached_at "+
			"и studbook_number. Повторная загрузка заменяет паспорт; паспорт синхронизируется на другие ноды.")
	putPassport.Parameters = []parameter{idParam("id", "UUID лошади")}
	putPassport.RequestBody = &requestBody{
		Description: "Ровно один PDF в поле file. При ошибке прежний паспорт сохраняется.",
		Required:    true,
		Content: map[string]mediaTypeValue{
			"multipart/form-data": {Schema: schemaNode{"type": "object", "required": []string{"file"}}.
				prop("file", schemaNode{"type": "string", "format": "binary", "description": "PDF до 50 MiB"}).
				prop("studbook_number", strSchema("номер по племенной книге, например 7706"))},
		},
	}
	putPassport.Responses = merge(stdErrors(true), map[string]resp{
		"200": okJSON(schemaNode{"$ref": "#/components/schemas/Horse"}),
		"400": {Description: "требуется multipart/form-data с одним файлом в поле file", Content: errorRef},
		"413": {Description: "файл больше 50 MiB", Content: errorRef},
		"415": {Description: "файл не PDF", Content: errorRef},
	})

	getPassport := add("/api/v1/horses/{id}/passport", "get", "Получить PDF-паспорт лошади", "getHorsePassport", "horses",
		"Отдаёт PDF (application/pdf, Content-Disposition: inline) для просмотра в браузере; ETag = sha256. "+
			"404 — лошади нет, паспорт не загружен или файл ещё не доехал по синку.")
	getPassport.Parameters = []parameter{idParam("id", "UUID лошади")}
	getPassport.Responses = merge(stdErrors(true), map[string]resp{
		"200": {Description: "PDF-паспорт", Content: map[string]mediaTypeValue{
			"application/pdf": {Schema: schemaNode{"type": "string", "format": "binary"}},
		}},
		"304": {Description: "не изменился (If-None-Match)"},
	})

	create := add("/api/v1/comparisons", "post", "Сравнить лошадей на снимке", "createComparison", "comparisons", "Каждая детекция имеет свою разметку и список особей по косинусному сходству.")
	create.RequestBody = &requestBody{Required: true, Content: jsonContent(schemaRef("ComparisonRequest"))}
	create.Responses = merge(stdErrors(true), map[string]resp{
		"201": createdJSON(schemaRef("ComparisonResult")),
		"422": {Description: "Некорректный хеш, top_k или нечитаемое изображение", Content: errorRef},
		"502": {Description: "Некорректный ответ модели", Content: errorRef},
		"503": {Description: "Сегментация недоступна", Content: errorRef},
	})
	decide := add("/api/v1/comparisons/{id}/detections/{detection}/decision", "put", "Сохранить решение", "decideComparison", "comparisons", "Повтор того же решения возвращает сохранённое наблюдение.")
	decide.Parameters = []parameter{idParam("id", "UUID сравнения"), idParam("detection", "UUID детекции")}
	decide.RequestBody = &requestBody{Required: true, Content: jsonContent(schemaRef("ComparisonDecision"))}
	decide.Responses = merge(stdErrors(true), map[string]resp{
		"200": okJSON(schemaRef("DecisionResult")),
		"409": {Description: "Детекция пропущена или уже сохранена с другим решением", Content: errorRef},
		"422": {Description: "Некорректное решение или особь отсутствует среди кандидатов", Content: errorRef},
	})
	cancel := add("/api/v1/comparisons/{id}/cancel", "post", "Отменить сравнение", "cancelComparison", "comparisons", "Сохраняет подтверждённые наблюдения и пропускает остальные детекции.")
	cancel.Parameters = []parameter{idParam("id", "UUID сравнения")}
	cancel.Responses = merge(stdErrors(true), map[string]resp{"200": okJSON(schemaRef("CancelResult"))})

	// --- observations ---
	listObs := add("/api/v1/observations", "get", "Список наблюдений", "listObservations", "observations",
		"Опциональный фильтр horse_id.")
	listObs.Parameters = []parameter{
		{Name: "horse_id", In: "query", Schema: strSchema("UUID лошади, пусто — все")},
		{Name: "include", In: "query", Schema: strSchema("photos,annotations — добавить фотографии и их разметку")},
		{Name: "limit", In: "query", Schema: strSchema("Размер страницы, по умолчанию 1000, максимум 5000; задаёт курсорную пагинацию (сортировка observed_at, id). Без limit/cursor — историческое поведение: LIMIT 1000.")},
		{Name: "cursor", In: "query", Schema: strSchema("Курсор продолжения из X-Next-Cursor предыдущей страницы.")},
	}
	listObs.Responses = map[string]resp{
		"200": okJSON(schemaNode{"type": "array", "items": schemaNode{"$ref": "#/components/schemas/Observation"}}),
	}

	createObs := add("/api/v1/observations", "post", "Создать наблюдение", "createObservation", "observations",
		"observer_id обязателен. horse_id опционален — пусто, если животное ещё не идентифицировано. "+
			"observed_at пусто — берётся текущее время.")
	createObs.RequestBody = &requestBody{
		Description: "Тело CreateObservationRequest.",
		Required:    true,
		Content:     jsonContent(schemaNode{"$ref": "#/components/schemas/CreateObservationRequest"}),
	}
	createObs.Responses = merge(stdErrors(true), map[string]resp{
		"201": createdJSON(schemaNode{"$ref": "#/components/schemas/Observation"}),
		"422": {Description: "observer_id отсутствует", Content: errorRef},
	})

	deleteObs := add("/api/v1/observations/{id}", "delete", "Удалить наблюдение (планируется)", "deleteObservation", "observations",
		"Проект контракта. Маршрут ещё не реализован. Запрос принимает ID наблюдения в пути без тела.")
	deleteObs.Parameters = []parameter{idParam("id", "UUID наблюдения")}
	deleteObs.Responses = map[string]resp{
		"204": {Description: "Наблюдение удалено, тело ответа пустое"},
		"404": {Description: "Наблюдение отсутствует", Content: errorRef},
	}

	// --- blobs ---
	listBlobs := add("/api/v1/blobs", "get", "Список блобов", "listBlobs", "blobs",
		"Без limit/cursor — весь список. С пагинацией (сортировка created_at, sha256) продолжение помечается заголовком X-Next-Cursor.")
	listBlobs.Parameters = []parameter{
		{Name: "limit", In: "query", Schema: strSchema("Размер страницы, по умолчанию 1000, максимум 5000; задаёт курсорную пагинацию.")},
		{Name: "cursor", In: "query", Schema: strSchema("Курсор продолжения из X-Next-Cursor предыдущей страницы.")},
	}
	listBlobs.Responses = map[string]resp{
		"200": okJSON(schemaNode{"type": "array", "items": schemaNode{"$ref": "#/components/schemas/Blob"}}),
	}

	uploadBlob := add("/api/v1/blobs", "post", "Загрузить файл (multipart)", "uploadBlob", "blobs",
		"Поле формы file — бинарный файл до 50 MiB. Содержимое адресуется sha256: повторная загрузка "+
			"того же файла возвращает 200 (дедупликация), первый раз — 201. Событие blob.registered "+
			"пишется только при первой регистрации.")
	uploadBlob.RequestBody = &requestBody{
		Description: "multipart/form-data с полем file.",
		Required:    true,
		Content: map[string]mediaTypeValue{
			"multipart/form-data": {Schema: schemaNode{"type": "object"}.
				prop("file", schemaNode{"type": "string", "format": "binary", "description": "до 50 MiB"})},
		},
	}
	uploadBlob.Responses = merge(stdErrors(false), map[string]resp{
		"200": createdJSON(schemaNode{"$ref": "#/components/schemas/Blob"}),
		"201": createdJSON(schemaNode{"$ref": "#/components/schemas/Blob"}),
		"413": {Description: "файл больше 50 MiB", Content: errorRef},
	})

	getBlob := add("/api/v1/blobs/{sha256}", "get", "Получить блоб", "getBlob", "blobs",
		"Отдаёт исходное содержимое с Content-Type из метаданных и ETag = sha256.")
	getBlob.Parameters = []parameter{sha256Param("sha256", "hex sha256 содержимого")}
	getBlob.Responses = merge(stdErrors(true), map[string]resp{
		"200": {Description: "содержимое блоба (произвольный тип)"},
	})

	deleteBlobOp := add("/api/v1/blobs/{sha256}", "delete", "Удалить блоб", "deleteBlob", "blobs",
		"Мягкое удаление событием blob.deleted: блоб перестаёт отдаваться (GET → 404), наблюдения с "+
			"этим фото помечаются invalidated и выпадают из сравнения. Файл удаляется, только если на "+
			"блоб не ссылается ни одно наблюдение. Повторная доставка события — no-op. Ре-аплоад того же "+
			"файла (blob.registered) снова делает блоб доступным, но флаг invalidated у наблюдений НЕ сбрасывается. "+
			"Инвалидация наблюдений и удержание файла срабатывают для обеих форм ссылок "+
			"(blobs.id и sha256): ссылка по blobs.id (UUIDv7, канонический контракт) резолвится в sha256 через таблицу blobs.")
	deleteBlobOp.Parameters = []parameter{
		sha256Param("sha256", "hex sha256 содержимого"),
		{Name: "reason", In: "query", Schema: strSchema("причина удаления — попадает в payload события")},
	}
	deleteBlobOp.Responses = merge(stdErrors(true), map[string]resp{
		"204": {Description: "блоб удалён, событие blob.deleted записано"},
		"404": {Description: "блоб не найден или уже удалён", Content: errorRef},
	})

	// --- node ---
	nodeOp := add("/api/v1/node", "get", "Информация о ноде", "getNodeInfo", "node",
		"Счётчики событий, векторное время, драйвер хранилища; при запущенном sync — блок sync.")
	nodeOp.Responses = map[string]resp{"200": okJSON(schemaNode{"$ref": "#/components/schemas/NodeInfo"})}

	// --- events (WebSocket) ---
	eventsOp := add("/api/v1/events", "get", "Поток событий (WebSocket)", "streamEvents", "events",
		"Эндпоинт апгрейдится до WebSocket (не SSE): кадры JSON с полями type/payload; "+
			"type = domain.* | sync_stats. Клиенты без WS-апгрейда получают 426 Upgrade Required.")
	eventsOp.Responses = map[string]resp{
		"101": {Description: "переключение протоколов на WebSocket"},
		"426": {Description: "Upgrade Required — клиент не запросил WebSocket", Content: errorRef},
	}

	// --- inference (ADR-0012) ---
	createTraits := add("/api/v1/inference/traits", "post", "Предсказать признаки", "createTraitSnapshot", "inference",
		"Движок предсказывает признаки по содержимому блоба и сохраняет снимок (confirmed=false). "+
			"Ответ содержит disclaimer: предзаполнение — НЕ идентификация.")
	createTraits.RequestBody = &requestBody{
		Required: true,
		Content:  jsonContent(schemaNode{"$ref": "#/components/schemas/CreateTraitsRequest"}),
	}
	createTraits.Responses = merge(stdErrors(true), map[string]resp{
		"201": createdJSON(schemaNode{"$ref": "#/components/schemas/TraitSnapshotResponse"}),
		"400": {Description: "нет blob_id | invalid image | животное не найдено на фото", Content: errorRef},
		"503": {Description: "модель недоступна (движок unavailable) — признаки вводятся вручную", Content: errorRef},
	})

	patchTraits := add("/api/v1/trait_snapshots/{id}", "patch", "Подтвердить/править признаки", "patchTraitSnapshot", "inference",
		"Human-in-the-loop: подтверждённые признаки сохраняются в traits с confidence=1.0, "+
			"исходное предсказание остаётся в raw_predicted_traits, edited=true.")
	patchTraits.Parameters = []parameter{idParam("id", "UUID снимка признаков")}
	patchTraits.RequestBody = &requestBody{
		Required: true,
		Content:  jsonContent(schemaNode{"$ref": "#/components/schemas/ConfirmTraitsRequest"}),
	}
	patchTraits.Responses = merge(stdErrors(true), map[string]resp{
		"200": okJSON(schemaNode{"$ref": "#/components/schemas/TraitSnapshot"}),
	})

	similar := add("/api/v1/inference/similar", "get", "Похожие по признакам", "listSimilarTraits", "inference",
		"Поиск по fingerprint среди подтверждённых снимков. Ответ содержит дисклеймер: "+
			"fingerprint — поиск похожих, НЕ биологическая идентичность.")
	similar.Parameters = []parameter{
		{Name: "blob_id", In: "query", Required: true, Schema: strSchema("blobs.id или sha256, по которому ищем снимок")},
		{Name: "limit", In: "query", Schema: schemaNode{"type": "integer", "default": 10, "minimum": 1}},
	}
	similar.Responses = merge(stdErrors(true), map[string]resp{
		"200": okJSON(schemaNode{"$ref": "#/components/schemas/SimilarResponse"}),
	})

	// --- components.schemas ---
	schemas["Error"] = schemaNode{"type": "object"}.
		prop("code", strSchema("машиночитаемый код, если известна причина")).
		prop("message", strSchema("человекочитаемое описание ошибки")).
		required("message")
	schemas["Horse"] = schemaNode{"type": "object"}.
		prop("id", strSchema("UUIDv7")).
		prop("created_at", schemaNode{"type": "string", "format": "date-time"}).
		prop("identity_status", strSchema("active | merged | unresolved")).
		prop("metadata", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "string"}}).
		required("id", "created_at", "identity_status", "metadata")
	addComparisonSchemas(schemas)
	schemas["Observation"] = schemaNode{"type": "object"}.
		prop("id", strSchema("UUIDv7")).
		prop("horse_id", strSchema("пусто, если животное ещё не идентифицировано")).
		prop("node_id", strSchema("UUID ноды-источника")).
		prop("observed_at", schemaNode{"type": "string", "format": "date-time"}).
		prop("location", strSchema("текст или latlon")).
		prop("comment", strSchema("Комментарий к наблюдению, опционально")).
		prop("observer_id", strSchema("идентификатор наблюдателя")).
		prop("blob_ids", schemaNode{
			"type":        "array",
			"description": "ссылки на блобы наблюдения: blobs.id (UUIDv7) — канонический контракт (OpenAPI); поддержана и форма sha256 (legacy/серверные пути). Ссылка по blobs.id резолвится в sha256 через таблицу blobs.",
			"items":       strSchema("blobs.id (UUIDv7, канонический контракт) либо hex sha256 (поддерживаемая альтернатива)"),
		}).
		prop("name", strSchema("Имя наблюдения")).
		prop("photos", schemaArray(schemaRef("ObservationPhoto"))).
		prop("trait_snapshot_id", strSchema("привязанный снимок признаков, опционально")).
		prop("invalidated", schemaNode{"type": "boolean", "description": "фото наблюдения удалено (blob.deleted): запись устарела, вектора не участвуют в сравнении. Ре-аплоад блоба флаг не сбрасывает. Инвалидация срабатывает для обеих форм ссылок: blobs.id (UUIDv7, канонический контракт) и sha256; UUID резолвится в sha256 через таблицу blobs."}).
		prop("invalidated_at", schemaNode{"type": "string", "format": "date-time", "description": "момент инвалидации (из deleted_at события blob.deleted)"}).
		prop("invalidation_reason", strSchema("причина инвалидации, фактический формат: \"blob deleted: <sha256>\" либо \"blob deleted: <sha256> (<reason>)\" при указанном reason")).
		required("id", "node_id", "observed_at", "location", "observer_id", "blob_ids")
	schemas["Blob"] = schemaNode{"type": "object"}.
		prop("id", strSchema("UUIDv7")).
		prop("sha256", strSchema("hex sha256 содержимого")).
		prop("mime_type", strSchema("Content-Type, как загружен")).
		prop("size", schemaNode{"type": "integer", "description": "размер в байтах"}).
		required("id", "sha256", "mime_type", "size")
	schemas["TraitSnapshot"] = schemaNode{"type": "object"}.
		prop("id", strSchema("UUIDv7")).
		prop("observation_id", strSchema("опционально: связанное наблюдение")).
		prop("model_version", strSchema("версия модели движка")).
		prop("traits", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "string"}, "description": "канонические признаки"}).
		prop("raw_predicted_traits", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "string"}, "description": "исходное предсказание до правки человеком"}).
		prop("confidence", schemaNode{"type": "number"}).
		prop("fingerprint", strSchema("sha256 канонизированных признаков")).
		prop("confirmed", schemaNode{"type": "boolean", "description": "подтверждено человеком"}).
		prop("edited", schemaNode{"type": "boolean", "description": "правился ли исходный предикт"}).
		required("id", "model_version", "traits", "confidence", "fingerprint", "confirmed", "edited")
	schemas["TraitSnapshotResponse"] = schemaNode{"allOf": []any{
		schemaNode{"$ref": "#/components/schemas/TraitSnapshot"},
		schemaNode{"type": "object"}.
			prop("disclaimer", strSchema("напоминание, что предзаполнение не идентифицирует")),
	}}
	schemas["NodeInfo"] = schemaNode{"type": "object"}.
		prop("node_id", strSchema("UUID ноды")).
		prop("app_version", strSchema("версия приложения")).
		prop("role_hint", strSchema("подсказка роли (local/central)")).
		prop("event_count", schemaNode{"type": "integer"}).
		prop("last_seq", schemaNode{"type": "integer"}).
		prop("vector_clock", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "integer"}, "description": "векторное время ноды"}).
		prop("driver", strSchema("sqlite | postgres")).
		prop("sync", schemaNode{"type": "object", "description": "счётчики sync-движка, если запущен"}).
		prop("blobs", schemaNode{"type": "object", "description": "состояние блобов: referenced/present/missing (неполный синк)"}).
		required("node_id", "app_version", "role_hint", "event_count", "last_seq", "driver")
	schemas["CreateObservationRequest"] = schemaNode{"type": "object"}.
		prop("horse_id", strSchema("опционально")).
		prop("observed_at", schemaNode{"type": "string", "format": "date-time", "description": "пусто — текущее время"}).
		prop("location", strSchema("текст/latlon")).
		prop("comment", strSchema("Комментарий к наблюдению, опционально")).
		prop("observer_id", strSchema("обязателен")).
		prop("blob_ids", schemaNode{
			"type":        "array",
			"description": "ссылки на ранее загруженные блобы: blobs.id (UUIDv7) — канонический контракт (OpenAPI); поддержана и форма sha256 (legacy/серверные пути).",
			"items":       strSchema("blobs.id (UUIDv7, канонический контракт) либо hex sha256 (поддерживаемая альтернатива)"),
		}).
		required("observer_id")
	schemas["CreateTraitsRequest"] = schemaNode{"type": "object"}.
		prop("blob_id", strSchema("blobs.id или sha256")).
		prop("observation_id", strSchema("опционально: привязка к наблюдению")).
		required("blob_id")
	schemas["ConfirmTraitsRequest"] = schemaNode{"type": "object"}.
		prop("traits", schemaNode{"type": "object", "additionalProperties": schemaNode{"type": "string"}, "description": "подтверждённые признаки"}).
		required("traits")
	schemas["SimilarResponse"] = schemaNode{"type": "object"}.
		prop("query", strSchema("fingerprint исходного снимка")).
		prop("disclaimer", strSchema("поиск похожих, не идентичность")).
		prop("results", schemaNode{"type": "array", "items": schemaNode{"$ref": "#/components/schemas/TraitSnapshot"}}).
		required("query", "disclaimer", "results")

	out, err := yaml.Marshal(map[string]any{
		"openapi":    spec.OpenAPI,
		"info":       spec.Info,
		"servers":    spec.Servers,
		"paths":      spec.Paths,
		"components": map[string]any{"schemas": schemas},
	})
	if err != nil {
		// структуры константны; маршал невозможен только при программной ошибке
		return []byte("# openapi spec marshal error\n")
	}
	return out
}

func merge(a, b map[string]resp) map[string]resp {
	out := map[string]resp{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// --- хендлеры ---

// handleScalarPage отдаёт HTML-оболочку Scalar UI.
func handleScalarPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(scalarPage))
}

// handleScalarOpenAPI отдаёт YAML спеки.
func handleScalarOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buildOpenAPISpec())
}

// mountDocs монтирует /api/scalar только при включённом флаге (central).
func mountDocs(r interface {
	Get(pattern string, h http.HandlerFunc)
},
) {
	r.Get("/api/scalar", handleScalarPage)
	r.Get("/api/scalar/openapi.yaml", handleScalarOpenAPI)
}
