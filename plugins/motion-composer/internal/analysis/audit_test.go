package analysis

import "testing"

func strongSpec() ShowSpec {
	return ShowSpec{
		Duration: 20000,
		Tracks: []Track{
			{Role: "heave", Channel: "OAxis_06", ValueType: "float", EngineeringRange: 1000},
			{Role: "wind", Channel: "OAxis_18", ValueType: "float", EngineeringRange: 100},
			{Role: "scent", Channel: "OAxis_24", ValueType: "bool"},
		},
		Presets: []Preset{{
			ID: "pulse", DurationMS: 20000,
			Channels: []PresetChannel{{Role: "heave", Keyframes: []Keyframe{
				{TimeMS: 0, Value: 0.10},
				{TimeMS: 5000, Value: 0.80},
				{TimeMS: 6000, Value: 0.80},
				{TimeMS: 7500, Value: 0.10},
				{TimeMS: 8500, Value: 0.45},
				{TimeMS: 11000, Value: 0.15},
				{TimeMS: 13000, Value: 0.08},
				{TimeMS: 15000, Value: 0.14},
				{TimeMS: 17000, Value: 0.09},
				{TimeMS: 19000, Value: 0.10},
				{TimeMS: 20000, Value: 0.00},
			}}},
		}},
		Cues: []Cue{{TimeMS: 0, Preset: "pulse", Intensity: 1}},
		Gates: []Gate{
			{Role: "wind", StartMS: 2000, EndMS: 10000, Level: 0.65},
			{Role: "scent", StartMS: 12000, EndMS: 15000, Level: 1},
		},
	}
}

func TestAuditAcceptingWeightlessPulse(t *testing.T) {
	timeline := &Timeline{Segments: []TimelineSegment{{ID: "high", StartMS: 0, EndMS: 20000, Energy: 0.8}}}
	profile := &DeviceProfile{Device: "YDL-0", Axes: []DeviceAxis{{Role: "heave", MaxSlewRate: 0.5}}}
	report := Audit(strongSpec(), "spec.json", timeline, profile, DefaultThresholds())
	if !report.OK {
		t.Fatalf("expected audit to pass, issues=%+v", report.Issues)
	}
	if report.Summary.StrongPulseCount != 1 {
		t.Fatalf("strong pulses=%d", report.Summary.StrongPulseCount)
	}
	if report.Summary.PeakHeave != 0.8 || report.Summary.WeakHighEnergySegments != 0 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
}

func TestAuditRejectsHighEnergyFlatSine(t *testing.T) {
	spec := strongSpec()
	spec.Presets[0].Channels[0].Keyframes = []Keyframe{
		{TimeMS: 0, Value: 0.10},
		{TimeMS: 5000, Value: 0.28},
		{TimeMS: 10000, Value: 0.10},
		{TimeMS: 15000, Value: 0.28},
		{TimeMS: 20000, Value: 0.10},
	}
	timeline := &Timeline{Segments: []TimelineSegment{{ID: "high", StartMS: 0, EndMS: 20000, Energy: 0.8}}}
	report := Audit(spec, "spec.json", timeline, nil, DefaultThresholds())
	if report.OK {
		t.Fatal("expected high-energy flat motion to fail")
	}
	if !hasIssue(report.Issues, "motion.high_energy_flat") || !hasIssue(report.Issues, "motion.no_weightless_pulse") {
		t.Fatalf("missing expected issues: %+v", report.Issues)
	}
}

func TestAuditRejectsOpenEndingEffectsAndNonNeutralHeave(t *testing.T) {
	spec := strongSpec()
	spec.Gates[1].EndMS = 19900
	spec.Presets[0].Channels[0].Keyframes[len(spec.Presets[0].Channels[0].Keyframes)-1].Value = 0.10
	report := Audit(spec, "spec.json", nil, nil, DefaultThresholds())
	if !hasIssue(report.Issues, "motion.end_effects_not_closed") || !hasIssue(report.Issues, "motion.end_not_neutral") {
		t.Fatalf("missing ending reset issues: %+v", report.Issues)
	}
}

func TestAuditRejectsLongStaticGap(t *testing.T) {
	spec := strongSpec()
	spec.Presets[0].Channels[0].Keyframes = []Keyframe{
		{TimeMS: 0, Value: 0.10},
		{TimeMS: 1000, Value: 0.30},
		{TimeMS: 2000, Value: 0.10},
		{TimeMS: 18000, Value: 0.10},
		{TimeMS: 20000, Value: 0.00},
	}
	report := Audit(spec, "spec.json", nil, nil, DefaultThresholds())
	if !hasIssue(report.Issues, "motion.long_static_gap") {
		t.Fatalf("missing static gap issue: %+v", report.Issues)
	}
}

func TestAuditRejectsInvalidBoolGate(t *testing.T) {
	spec := strongSpec()
	spec.Gates[1].Level = 0.5
	report := Audit(spec, "spec.json", nil, nil, DefaultThresholds())
	if !hasIssue(report.Issues, "gate.bool_level") {
		t.Fatalf("missing bool gate issue: %+v", report.Issues)
	}
}

func hasIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
