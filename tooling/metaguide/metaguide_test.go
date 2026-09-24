package metaguide

import (
	"strings"
	"testing"
)

func TestLengthCountsCharacters(t *testing.T) {
	cases := map[string]int{
		"":              0,
		"  abc  ":       3,
		"技能":            2,
		"开发UI规范":        6,
		"com.himind.x":  12,
		"hello-world-1": 13,
	}
	for value, want := range cases {
		if got := Length(value); got != want {
			t.Fatalf("Length(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestSlugOfUsesLastSegment(t *testing.T) {
	if got := SlugOf("com.himind.skill.develop-ui"); got != "develop-ui" {
		t.Fatalf("SlugOf = %q", got)
	}
	if got := SlugOf("plain"); got != "plain" {
		t.Fatalf("SlugOf = %q", got)
	}
}

func TestCheckAllowsBoundaryAndRejectsOver(t *testing.T) {
	if err := Check(DisplayName, repeat("名", DisplayName.Max)); err != nil {
		t.Fatalf("boundary should pass: %v", err)
	}
	if err := Check(DisplayName, repeat("名", DisplayName.Max+1)); err == nil {
		t.Fatal("over limit should fail")
	}
}

func TestCheckReportsSuggestionOnlyWhenPresent(t *testing.T) {
	withSuggestion := Check(DisplayName, repeat("名", DisplayName.Max+1))
	if withSuggestion == nil || !strings.Contains(withSuggestion.Error(), "(recommended") {
		t.Fatalf("expected suggestion in message, got %v", withSuggestion)
	}
	hardLimitOnly := Check(CapabilityID, repeat("a", CapabilityID.Max+1))
	if hardLimitOnly == nil || strings.Contains(hardLimitOnly.Error(), "recommended") {
		t.Fatalf("expected hard limit only, got %v", hardLimitOnly)
	}
	if HasRecommended(CapabilityID) {
		t.Fatal("CapabilityID should not carry a suggestion")
	}
}

func TestFieldsCoverEveryRule(t *testing.T) {
	for _, field := range Fields() {
		if field.Max <= 0 || field.Recommended < 0 {
			t.Fatalf("field %s has invalid bounds: %+v", field.Key, field)
		}
		if HasRecommended(field) && field.Max <= field.Recommended {
			t.Fatalf("field %s suggestion must stay below the hard limit: %+v", field.Key, field)
		}
	}
}

func repeat(value string, count int) string {
	out := make([]rune, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, []rune(value)...)
	}
	return string(out)
}
