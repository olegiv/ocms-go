// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import "slices"

// Severity defines whether an issue blocks persistence or asks for review.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// Issue is stable machine-readable feedback with localized text and a next step.
type Issue struct {
	Code     string   `json:"code"`
	Field    string   `json:"field"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Hint     string   `json:"hint"`
}

// IssueText is one language's message and actionable hint.
type IssueText struct{ Message, Hint string }

// Definition is a closed catalog entry. The same entries drive locale drift
// checks and the later issue-catalog MCP resource/tool.
type Definition struct {
	Code     string
	Severity Severity
	English  IssueText
	Russian  IssueText
}

// issueCatalog is the single catalog of codes, severities and localized text.
var issueCatalog = []Definition{
	{"invalid_input", Error, IssueText{"Input does not match the draft contract.", "Use the create or update schema and check field types, required values and nullability."}, IssueText{"Входные данные не соответствуют контракту черновика.", "Проверьте типы полей, обязательные значения и допустимость null по схеме создания или обновления."}},
	{"title_length", Error, IssueText{"The title must contain 1–255 characters.", "Trim the title and keep it within the character limit."}, IssueText{"Заголовок должен содержать от 1 до 255 символов.", "Уберите пробелы по краям и соблюдайте ограничение длины."}},
	{"summary_length", Error, IssueText{"The summary exceeds 500 characters.", "Shorten the summary to at most 500 characters."}, IssueText{"Краткое описание превышает 500 символов.", "Сократите краткое описание до 500 символов."}},
	{"meta_title_length", Warning, IssueText{"The SEO title exceeds 60 characters.", "Consider a shorter SEO title; the submitted value is retained."}, IssueText{"SEO-заголовок превышает 60 символов.", "Рекомендуется сократить SEO-заголовок; переданное значение сохранено."}},
	{"meta_description_trimmed", Warning, IssueText{"The SEO description was shortened to 160 characters or fewer.", "Review the description shortened at a word boundary."}, IssueText{"SEO-описание сокращено до 160 символов или меньше.", "Проверьте описание, сокращённое по границе слова."}},
	{"slug_invalid", Error, IssueText{"The slug has an invalid format.", "Use lowercase Latin letters, digits and single hyphens, up to 255 characters."}, IssueText{"Недопустимый формат URL-идентификатора.", "Используйте строчные латинские буквы, цифры и одиночные дефисы, не более 255 символов."}},
	{"slug_taken", Error, IssueText{"The slug conflicts with an existing page, alias or route.", "Choose another slug or omit it when creating a draft to derive an available one."}, IssueText{"URL-идентификатор занят страницей, псевдонимом или маршрутом.", "Выберите другой идентификатор или не передавайте его при создании для автоматического подбора."}},
	{"tags_limit", Error, IssueText{"More than ten distinct tags were supplied.", "Keep at most ten existing tag slugs after deduplication."}, IssueText{"Передано более десяти разных тегов.", "Оставьте не более десяти существующих тегов после удаления повторов."}},
	{"unknown_tag", Error, IssueText{"A tag is unavailable in the selected language.", "Choose an existing tag offered for this language."}, IssueText{"Тег недоступен для выбранного языка.", "Выберите существующий тег, предложенный для этого языка."}},
	{"tag_creation_not_allowed", Error, IssueText{"Creating tags through draft authoring is not allowed.", "Use existing tag slugs from the authoring context."}, IssueText{"Создание тегов через инструменты черновиков запрещено.", "Используйте существующие теги из контекста сайта."}},
	{"category_not_found", Error, IssueText{"The category is unavailable in the selected language.", "Choose an existing category offered for this language, or clear it."}, IssueText{"Категория недоступна для выбранного языка.", "Выберите существующую категорию для этого языка или очистите поле."}},
	{"image_not_found", Error, IssueText{"The featured image is unavailable or is not a supported image.", "Choose an existing JPEG, PNG, GIF or WebP image from the media library."}, IssueText{"Изображение записи недоступно или имеет неподдерживаемый формат.", "Выберите существующее изображение JPEG, PNG, GIF или WebP из медиатеки."}},
	{"image_too_small", Error, IssueText{"The featured image is smaller than 1200×800 pixels.", "Choose an existing image meeting the featured-image minimum dimensions."}, IssueText{"Изображение записи меньше 1200×800 пикселей.", "Выберите существующее изображение с допустимыми минимальными размерами."}},
	{"media_ref_not_found", Error, IssueText{"An inline image does not resolve to supported existing media.", "Use a canonical media:ID reference to an existing image."}, IssueText{"Изображение в тексте не найдено в медиатеке или имеет неподдерживаемый формат.", "Используйте ссылку media:ID на существующее изображение."}},
	{"language_inactive", Error, IssueText{"The language is unavailable, inactive or cannot be routed.", "Choose an active language offered by this site."}, IssueText{"Язык недоступен, неактивен или не поддерживает маршрутизацию.", "Выберите активный язык, предложенный сайтом."}},
	{"reference_unresolved", Error, IssueText{"A site reference could not be resolved.", "Reread the authoring context and choose an existing site reference."}, IssueText{"Не удалось разрешить ссылку на материал сайта.", "Обновите контекст сайта и выберите существующий материал."}},
	{"reference_not_visible", Error, IssueText{"A site reference is unavailable to this account.", "Use only references visible under the current connection's permissions."}, IssueText{"Материал сайта недоступен этому аккаунту.", "Используйте только материалы, доступные текущему подключению."}},
	{"external_link", Info, IssueText{"The draft includes an external link for editorial review.", "Review the external-links list and the applied keep, nofollow or strip policy."}, IssueText{"Черновик содержит внешнюю ссылку для редакторской проверки.", "Проверьте список внешних ссылок и применённую политику keep, nofollow или strip."}},
	{"internal_link_broken", Warning, IssueText{"An internal link does not resolve to visible site content.", "Choose a valid internal link from the authoring context."}, IssueText{"Внутренняя ссылка не ведёт на доступный материал сайта.", "Выберите допустимую внутреннюю ссылку из контекста сайта."}},
	{"link_not_allowed", Error, IssueText{"A link uses an unsupported or unsafe URL.", "Use an internal link, HTTP, HTTPS or mailto URL without embedded credentials."}, IssueText{"Ссылка содержит неподдерживаемый или небезопасный URL.", "Используйте внутреннюю ссылку, HTTP, HTTPS или mailto без встроенных учётных данных."}},
	{"raw_html_not_allowed", Error, IssueText{"Raw HTML is not accepted in Markdown drafts.", "Use Markdown syntax; put HTML examples inside fenced code blocks."}, IssueText{"HTML-разметка не принимается в Markdown-черновиках.", "Используйте Markdown; примеры HTML помещайте в блоки кода."}},
	{"overlap_with_example", Warning, IssueText{"The draft overlaps substantially with an example page.", "Review and rewrite passages that copy an example rather than use it for style."}, IssueText{"Черновик существенно совпадает с материалом-примером.", "Проверьте и перепишите фрагменты, скопированные из примера вместо использования его стиля."}},
	{"forbidden_field", Error, IssueText{"The request contains a field this operation cannot change.", "Remove lifecycle, ownership, language, page-type or verified fields from updates."}, IssueText{"Запрос содержит поле, которое эта операция не может изменять.", "Уберите из обновления поля публикации, владельца, языка, типа страницы и проверенных данных."}},
	{"revision_required", Error, IssueText{"A base revision is required.", "Read the current draft and submit its revision with the update."}, IssueText{"Необходимо указать базовую ревизию.", "Прочитайте текущий черновик и передайте его ревизию при обновлении."}},
	{"revision_conflict", Error, IssueText{"The page changed after it was read.", "Reread the draft and merge the changes before submitting a new update."}, IssueText{"Страница изменена после чтения.", "Перечитайте черновик и объедините изменения перед повторным обновлением."}},
	{"idempotency_key_reused", Error, IssueText{"This request ID was already used for different input.", "Reuse an ID only for an unchanged retry; use a new UUID for a new operation."}, IssueText{"Этот идентификатор запроса уже использован с другими данными.", "Повторяйте идентификатор только с теми же данными; для новой операции создайте новый UUID."}},
	{"request_expired", Error, IssueText{"The original result is expired or its page was deleted.", "Inspect current drafts before starting a new operation with a new request ID."}, IssueText{"Срок хранения результата истёк или страница удалена.", "Проверьте текущие черновики перед новой операцией с новым идентификатором запроса."}},
	{"not_ai_editable", Error, IssueText{"The draft is not available for AI edits.", "The owner must enable AI edits on a plain draft accessible to this account."}, IssueText{"Черновик недоступен для редактирования ИИ.", "Владелец должен разрешить правки ИИ для доступного этому аккаунту неопубликованного черновика."}},
	{"source_revision_conflict", Error, IssueText{"The translation source changed after it was read.", "Reread the source and regenerate the translation from its current revision."}, IssueText{"Источник перевода изменён после чтения.", "Перечитайте источник и подготовьте перевод по его текущей ревизии."}},
	{"translation_exists", Error, IssueText{"This translation group already contains the target language.", "Read the translation status before creating another translation."}, IssueText{"Группа переводов уже содержит материал на целевом языке.", "Проверьте состояние переводов перед созданием нового."}},
	{"body_too_large", Error, IssueText{"The Markdown or resulting HTML exceeds 200,000 bytes.", "Shorten the body and retry the same logical operation only with a new request ID."}, IssueText{"Markdown или итоговый HTML превышает 200 000 байт.", "Сократите текст; для изменённых данных используйте новый идентификатор запроса."}},
	{"html_conversion_lossy", Warning, IssueText{"The stored rich HTML cannot be represented completely as Markdown.", "Review the stored HTML before explicitly replacing its body; metadata-only updates preserve it."}, IssueText{"Сохранённый HTML нельзя полностью представить в Markdown.", "Проверьте HTML перед заменой текста; обновления метаданных сохраняют его без изменений."}},
	{"media_conversion_lossy", Warning, IssueText{"An existing image could not be converted to a media:ID reference.", "Choose matching media from the library before replacing this body."}, IssueText{"Не удалось преобразовать существующее изображение в ссылку media:ID.", "Перед заменой текста выберите соответствующий файл из медиатеки."}},
}

// Catalog returns a copy so callers cannot change server validation behavior.
func Catalog() []Definition { return slices.Clone(issueCatalog) }

// NewIssue resolves only known catalog entries. An unknown code becomes a
// blocking invalid_input issue rather than emitting an undocumented code.
func NewIssue(code, field, language string) Issue {
	definition := issueCatalog[0]
	for _, candidate := range issueCatalog {
		if candidate.Code == code {
			definition = candidate
			break
		}
	}
	text := definition.English
	if language == "ru" || len(language) > 3 && language[:3] == "ru-" {
		text = definition.Russian
	}
	return Issue{Code: definition.Code, Field: field, Severity: definition.Severity, Message: text.Message, Hint: text.Hint}
}

// HasErrors reports whether feedback blocks a write.
func HasErrors(issues []Issue) bool {
	return slices.ContainsFunc(issues, func(issue Issue) bool { return issue.Severity == Error })
}
