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

func TestCapabilityIDLimitAndAuthoringRulesMatchADR0119(t *testing.T) {
	if CapabilityID.Max != 48 {
		t.Fatalf("CapabilityID.Max = %d, want ADR 0119 limit 48", CapabilityID.Max)
	}
	if err := Check(CapabilityID, strings.Repeat("a", CapabilityID.Max)); err != nil {
		t.Fatalf("ID at the ADR 0119 boundary should pass: %v", err)
	}
	if err := Check(CapabilityID, strings.Repeat("a", CapabilityID.Max+1)); err == nil {
		t.Fatal("ID beyond the ADR 0119 boundary should fail")
	}

	rules := strings.Join(CapabilityIDRules(), " ")
	for _, required := range []string{
		"2–5", "48", "^[a-z][a-z0-9]*(-[a-z0-9]+)*$", "`-`", "下划线", "驼峰", "版本号",
		"capability_namespace", "父段已经表达的语义",
	} {
		if !strings.Contains(rules, required) {
			t.Errorf("CapabilityIDRules must tell authors %q", required)
		}
	}
	for _, domain := range CapabilityIDDomains() {
		if !strings.Contains(rules, domain) {
			t.Errorf("CapabilityIDRules omits ADR 0119 domain %q", domain)
		}
	}
}

func TestCheckCapabilityIDShapeUsesADR0119Structure(t *testing.T) {
	for _, id := range []string{
		"media.toolkit.frames.contact-sheet",
		"extension.plugin.scaffold",
	} {
		if err := CheckCapabilityIDShape(id); err != nil {
			t.Errorf("valid ID %q rejected: %v", id, err)
		}
	}
	for _, id := range []string{
		"media_toolkit.run",
		"media.toolkit.frames.contact_sheet",
		"media.toolkit.frames.contact-sheet.v1",
		"media.toolkit.toolkit.run",
		"media.toolkit.frames.contact-sheet.extra.segment",
		"3d.tool.run",
	} {
		if err := CheckCapabilityIDShape(id); err == nil {
			t.Errorf("invalid ID %q should be rejected", id)
		}
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

// 「智能」这类承诺词只有 2 个字，长度校验必然放行 —— 所以必须单独有一条词表检查，
// 否则「媒体智能」这种必然违规的名字会一路走到提审。这是实测踩出来的。
func TestBannedInCatchesCommitmentWords(t *testing.T) {
	for _, name := range []string{"媒体智能", "智能处理工具", "一键发布工具", "全能工具箱"} {
		if hits := BannedIn(name); len(hits) == 0 {
			t.Errorf("%q 应命中禁用词", name)
		}
	}
	for _, name := range []string{"媒体处理工具", "媒体理解工具", "科技雷达", "软件分发工具"} {
		if hits := BannedIn(name); len(hits) != 0 {
			t.Errorf("%q 不应命中禁用词，实际命中 %v", name, hits)
		}
	}
}

// Rules 是推给创作方的唯一文案规则来源；它必须把禁用词列全，
// 否则创作方看到的规则比规范窄 —— 这正是当初「智能」漏网的原因。
func TestRulesMentionsEveryBannedWord(t *testing.T) {
	for _, word := range BannedWords {
		if !strings.Contains(Rules, word) {
			t.Errorf("Rules 未提及禁用词 %q，创作方与校验器会不一致", word)
		}
	}
}

func TestCommonPrefix(t *testing.T) {
	cases := []struct {
		ids  []string
		want string
	}{
		{[]string{"media.toolkit.probe", "media.toolkit.job.status"}, "media.toolkit"},
		{[]string{"extension.plugin.scaffold", "extension.skill.scaffold"}, "extension"},
		{[]string{"iostudio.motion.build"}, "iostudio.motion"},
		{[]string{"a.b", "c.d"}, ""},
		{nil, ""},
	}
	for _, item := range cases {
		if got := CommonPrefix(item.ids); got != item.want {
			t.Errorf("CommonPrefix(%v) = %q, want %q", item.ids, got, item.want)
		}
	}
}

// 多段前缀必须声明；首段是插件 slug 或共享域则免声明（规范 §二8）。
func TestSuggestedNamespace(t *testing.T) {
	cases := []struct {
		pluginID string
		ids      []string
		want     string
	}{
		{"com.himind.media-toolkit", []string{"media.toolkit.probe"}, "media.toolkit"},
		{"com.himind.media-ai", []string{"media.ai.transcribe"}, "media.ai"},
		{"com.himind.iostudio", []string{"iostudio.devices"}, ""},
		{"com.himind.media-library", []string{"media-library.search"}, ""},
		// extension.plugin.* 的共同前缀只有一段，且 extension 是共享域 → 免声明。
		// 若这里判成需要声明，工具链自己的清单就会被自己的校验器拦下。
		{"com.himind.extension-development-tools",
			[]string{"extension.plugin.scaffold", "extension.skill.validate"}, ""},
	}
	for _, item := range cases {
		if got := SuggestedNamespace(item.pluginID, item.ids); got != item.want {
			t.Errorf("SuggestedNamespace(%s, %v) = %q, want %q", item.pluginID, item.ids, got, item.want)
		}
	}
}

func TestCheckNamespace(t *testing.T) {
	ids := []string{"media.toolkit.probe"}
	if err := CheckNamespace("com.himind.media-toolkit", "media.toolkit", ids); err != nil {
		t.Errorf("已声明且匹配应通过: %v", err)
	}
	if err := CheckNamespace("com.himind.media-toolkit", "", ids); err == nil {
		t.Error("多段前缀未声明应被拒绝")
	}
	// 声明了却不是所有能力的前缀 —— 那些能力会落到别人的命名空间里。
	mixed := []string{"media.toolkit.probe", "media.ai.transcribe"}
	if err := CheckNamespace("com.himind.media-toolkit", "media.toolkit", mixed); err == nil {
		t.Error("声明未覆盖全部能力应被拒绝")
	}
	if err := CheckNamespace("com.himind.iostudio", "", []string{"iostudio.devices"}); err != nil {
		t.Errorf("首段即 slug 应免声明: %v", err)
	}
}

func repeat(value string, count int) string {
	out := make([]rune, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, []rune(value)...)
	}
	return string(out)
}

// 能力引用扫描：判据要够紧 —— 普通词、命名空间本身、文件名都不该被当成能力。
func TestUndeclaredCapabilityReferencesCatchesMissingDeclaration(t *testing.T) {
	declared := []string{"iostudio.motion.plan", "iostudio.motion.build"}
	slugs := []string{"iostudio"}
	text := "先跑 iostudio.motion.plan，再跑 iostudio.motion.build；" +
		"如果预检有问题，用 iostudio.motion.validate 看清单。" // 这条没声明
	missing := UndeclaredCapabilityReferences(text, declared, slugs)
	if len(missing) != 1 || missing[0] != "iostudio.motion.validate" {
		t.Fatalf("应只报未声明的 validate，实际 %v", missing)
	}
}

func TestUndeclaredCapabilityReferencesIgnoresNoise(t *testing.T) {
	declared := []string{"media.toolkit.probe"}
	slugs := []string{"media-toolkit"}
	text := strings.Join([]string{
		"配置写在 distribution.sample.json 里", // 文件名
		"命名空间 media.toolkit 下的能力",         // 公共前缀，不是能力
		"media-toolkit 这个插件",              // 插件 slug
		"git.commit.message 是无关词",         // 首段不在命名空间内
		"media.toolkit.probe 已声明",         // 已声明
	}, "；")
	if missing := UndeclaredCapabilityReferences(text, declared, slugs); len(missing) != 0 {
		t.Fatalf("不应报任何缺口，实际 %v", missing)
	}
}

// 没有任何声明时跳过而不是全量报错 —— 否则新建工程会被自己的文档淹掉。
func TestUndeclaredCapabilityReferencesSkipsWithoutAnchors(t *testing.T) {
	if missing := UndeclaredCapabilityReferences("a.b.c d.e.f", nil, nil); missing != nil {
		t.Fatalf("无锚点时应跳过，实际 %v", missing)
	}
}
