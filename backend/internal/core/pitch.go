package core

import (
	"strings"

	"sobachka/internal/store"
)

// DefaultPitchTemplate используется, если у профиля шаблон не задан.
const DefaultPitchTemplate = "Здравствуйте! Ваш заказ «{title}» — по моему профилю: {matched}.\n\n" +
	"{experience}\n\n" +
	"Чтобы сразу назвать точные сроки и стоимость, подскажите: есть ли чертёж, модель или ТЗ и какой нужен объём?\n\n" +
	"Готов обсудить детали в личных сообщениях."

// pitchVariants разбивает шаблон на варианты по строке «---».
func pitchVariants(tpl string) []string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultPitchTemplate
	}
	var out []string
	for _, v := range strings.Split(strings.ReplaceAll(tpl, "\r\n", "\n"), "\n---") {
		v = strings.TrimSpace(strings.TrimPrefix(v, "-"))
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		out = []string{DefaultPitchTemplate}
	}
	return out
}

// BuildPitch собирает текст отклика из шаблона профиля.
// variant — номер варианта (берётся по модулю числа вариантов).
func BuildPitch(o store.Order, p store.Profile, variant int) string {
	vs := pitchVariants(p.PitchTemplate)
	if variant < 0 {
		variant = -variant
	}
	tpl := vs[variant%len(vs)]

	title := strings.TrimSpace(o.Title)
	// Некоторые площадки дописывают к заголовку «(для всех)», «(Бюджет: …)» — в отклике это лишнее.
	if i := strings.LastIndex(title, " ("); i > 10 && strings.HasSuffix(title, ")") {
		title = strings.TrimSpace(title[:i])
	}
	title = strings.TrimRight(title, ".!?")

	matched := strings.Join(firstN(o.Matched, 4), ", ")
	if matched == "" {
		matched = strings.ToLower(p.Name)
	}
	budget := o.Budget
	if budget == "" {
		budget = "по договорённости"
	}
	r := strings.NewReplacer(
		"{title}", title,
		"{matched}", matched,
		"{direction}", p.Name,
		"{experience}", strings.TrimSpace(p.PitchNotes),
		"{budget}", budget,
	)
	text := r.Replace(tpl)
	// Если какой-то блок пустой (например, нет «опыта»), не оставляем лишних пустых строк.
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// PitchVariantCount — сколько вариантов текста у профиля.
func PitchVariantCount(p store.Profile) int { return len(pitchVariants(p.PitchTemplate)) }
