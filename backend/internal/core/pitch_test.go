package core

import (
	"strings"
	"testing"

	"sobachka/internal/store"
)

func TestBuildPitch(t *testing.T) {
	p := store.Profile{
		Name:          "Сайты",
		PitchNotes:    "Опыт 10 лет.",
		PitchTemplate: "Вариант 1: {title} / {matched}\n\n{experience}\n---\nВариант 2: {title}",
	}
	o := store.Order{Title: "Вёрстка лендинга для студии (Бюджет: 5 000 ₽)", Matched: []string{"верстк", "лендинг"}}

	got := BuildPitch(o, p, 0)
	want := "Вариант 1: Вёрстка лендинга для студии / верстк, лендинг\n\nОпыт 10 лет."
	if got != want {
		t.Errorf("variant 0:\n%q\nwant\n%q", got, want)
	}
	if got := BuildPitch(o, p, 1); got != "Вариант 2: Вёрстка лендинга для студии" {
		t.Errorf("variant 1: %q", got)
	}
	if got := BuildPitch(o, p, 2); !strings.HasPrefix(got, "Вариант 1") {
		t.Errorf("variant должен браться по модулю: %q", got)
	}

	// Пустой шаблон и пустой опыт — стандартный текст без лишних пустых строк.
	def := BuildPitch(store.Order{Title: "Логотип для кафе"}, store.Profile{Name: "Дизайн"}, 0)
	if strings.Contains(def, "\n\n\n") || !strings.Contains(def, "«Логотип для кафе»") || !strings.Contains(def, "дизайн") {
		t.Errorf("default pitch: %q", def)
	}
	t.Logf("\n%s", def)
}

func TestSeedPitches(t *testing.T) {
	o := store.Order{Title: "Сделать лендинг для студии (для всех)", Matched: []string{"лендинг"}}
	for _, p := range store.SeedProfiles() {
		for i := 0; i < PitchVariantCount(p); i++ {
			if text := BuildPitch(o, p, i); strings.Contains(text, "{") {
				t.Errorf("%s, вариант %d: неподставленная переменная: %q", p.Name, i, text)
			}
		}
	}
}
