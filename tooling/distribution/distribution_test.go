package distribution

import "testing"

func TestParseKeepsFixedOrder(t *testing.T) {
	targets, err := Parse([]string{"github", "workbench"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(targets) != 2 || targets[0] != Workbench || targets[1] != Github {
		t.Fatalf("Parse = %v", targets)
	}
}

func TestParseRejectsEmptyUnknownAndDuplicate(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("empty target list should fail")
	}
	if _, err := Parse([]string{}); err == nil {
		t.Fatal("empty target list should fail")
	}
	if _, err := Parse([]string{"gitlab"}); err == nil {
		t.Fatal("unknown target should fail")
	}
	if _, err := Parse([]string{"github", "github"}); err == nil {
		t.Fatal("duplicate target should fail")
	}
}

func TestAllowsAndDescribe(t *testing.T) {
	targets, err := Parse([]string{"workbench", "github"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !Allows(targets, Github) || !Allows(targets, Workbench) {
		t.Fatal("both targets should be allowed")
	}
	if Allows(Default(), Github) {
		t.Fatal("default must not allow GitHub")
	}
	if got := Describe(targets); got != "工作台 + GitHub" {
		t.Fatalf("Describe = %q", got)
	}
}

func TestFromManifestReportsDeclaration(t *testing.T) {
	targets, declared, err := FromManifest([]byte(`{"id":"com.himind.skill.demo"}`))
	if err != nil || declared {
		t.Fatalf("missing field: targets=%v declared=%v err=%v", targets, declared, err)
	}
	targets, declared, err = FromManifest([]byte(`{"distribution_targets":["github"]}`))
	if err != nil || !declared {
		t.Fatalf("declared field: targets=%v declared=%v err=%v", targets, declared, err)
	}
	if len(targets) != 1 || targets[0] != Github {
		t.Fatalf("targets = %v", targets)
	}
	if _, declared, err = FromManifest([]byte(`{"distribution_targets":[]}`)); err == nil {
		t.Fatal("explicit empty list should fail")
	} else if !declared {
		t.Fatal("invalid explicit list should still report declared")
	}
}
