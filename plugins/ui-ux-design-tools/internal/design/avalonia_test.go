package design

import "testing"

func TestInspectAvaloniaProject(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "IOStudio.csproj", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Avalonia" Version="11.3.10" /></ItemGroup></Project>`)
	writeFixture(t, root, "App.axaml", `<Application xmlns="https://github.com/avaloniaui"><Application.Resources><ResourceDictionary><ResourceDictionary.ThemeDictionaries><ResourceDictionary x:Key="Light"><SolidColorBrush x:Key="TlAccent" Color="#3169bc"/></ResourceDictionary></ResourceDictionary.ThemeDictionaries></ResourceDictionary></Application.Resources></Application>`)
	writeFixture(t, root, "Assets/Themes/TimelineThemeResources.axaml", `<ResourceDictionary xmlns="https://github.com/avaloniaui"><ResourceDictionary.ThemeDictionaries><ResourceDictionary x:Key="Light"><SolidColorBrush x:Key="TlTextPrimary" Color="#20242a"/></ResourceDictionary><ResourceDictionary x:Key="Dark"><SolidColorBrush x:Key="TlTextPrimary" Color="#e6e8eb"/></ResourceDictionary></ResourceDictionary.ThemeDictionaries></ResourceDictionary>`)
	writeFixture(t, root, "Views/MainWindow.axaml", `<Window xmlns="https://github.com/avaloniaui"></Window>`)

	c := newTestCatalog(t)
	result, err := c.InspectProject(ProjectInspectInput{WorkspaceRoot: root, MaxDepth: 5, MaxFiles: 500})
	if err != nil {
		t.Fatal(err)
	}
	detected := result["detected"].([]map[string]any)
	names := map[string]bool{}
	for _, item := range detected {
		names[item["name"].(string)] = true
	}
	for _, name := range []string{".NET / Windows 桌面", "XAML UI", "Avalonia"} {
		if !names[name] {
			t.Fatalf("missing detected stack %s: %#v", name, detected)
		}
	}
	ds := result["design_system"].(map[string]any)
	if !ds["has_design_tokens"].(bool) {
		t.Fatalf("expected Avalonia token files: %#v", ds)
	}
	tokens := ds["token_files"].([]string)
	if len(tokens) < 2 {
		t.Fatalf("expected App.axaml and theme resource file: %#v", tokens)
	}
}

func TestValidateAvaloniaTokens(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Assets/Themes/TimelineThemeResources.axaml", `<ResourceDictionary xmlns="https://github.com/avaloniaui">
<ResourceDictionary.ThemeDictionaries>
<ResourceDictionary x:Key="Light">
<SolidColorBrush x:Key="TlTextPrimary" Color="#20242a"/>
<SolidColorBrush x:Key="TlPanelBg" Color="#f4f5f6"/>
<SolidColorBrush x:Key="TlAccent" Color="#3169bc"/>
<SolidColorBrush x:Key="TlAccentFg" Color="#ffffff"/>
<SolidColorBrush x:Key="TlSuccessSoftBg" Color="#f0fdf4"/>
<SolidColorBrush x:Key="TlSuccessSoftFg" Color="#166534"/>
</ResourceDictionary>
<ResourceDictionary x:Key="Dark">
<SolidColorBrush x:Key="TlTextPrimary" Color="#e6e8eb"/>
<SolidColorBrush x:Key="TlPanelBg" Color="#1b1e22"/>
<SolidColorBrush x:Key="TlAccent" Color="#3169bc"/>
<SolidColorBrush x:Key="TlAccentFg" Color="#ffffff"/>
<SolidColorBrush x:Key="TlSuccessSoftBg" Color="#203c33"/>
<SolidColorBrush x:Key="TlSuccessSoftFg" Color="#7bd1ac"/>
</ResourceDictionary>
</ResourceDictionary.ThemeDictionaries>
<!-- 文档示例颜色 #abcdef 不应被当作硬编码 UI 色 -->
<BoxShadows x:Key="Shadow">0 1 3 0 #22000000</BoxShadows>
</ResourceDictionary>`)
	writeFixture(t, root, "Views/MainWindow.axaml", `<Window xmlns="https://github.com/avaloniaui"><Border Background="#ff00ff"/></Window>`)

	c := newTestCatalog(t)
	result, err := c.ValidateTokens(TokenValidateInput{WorkspaceRoot: root, Paths: []string{"Assets/Themes/TimelineThemeResources.axaml", "Views/MainWindow.axaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["token_count"].(int) < 6 {
		t.Fatalf("expected XAML tokens: %#v", result["tokens"])
	}
	if len(result["contrast_checks"].([]map[string]any)) == 0 {
		t.Fatalf("expected Avalonia contrast checks: %#v", result["contrast_checks"])
	}
	hardcoded := result["hardcoded_colors"].([]map[string]any)
	found := false
	for _, item := range hardcoded {
		if item["value"] == "#ff00ff" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected inline XAML hardcoded color: %#v", hardcoded)
	}
	if len(hardcoded) != 1 {
		t.Fatalf("token declarations and comments should be ignored: %#v", hardcoded)
	}
}
