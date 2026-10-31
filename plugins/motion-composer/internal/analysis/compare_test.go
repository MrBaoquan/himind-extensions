package analysis

import "testing"

func cueSpec(cues []Cue) ShowSpec { return ShowSpec{Cues: cues} }

func TestSameCueTimingAllowsIntensityChanges(t *testing.T) {
	base := cueSpec([]Cue{{TimeMS: 1000, Preset: "a", Intensity: 0.6}, {TimeMS: 4000, Preset: "b", Intensity: 0.8}})
	other := cueSpec([]Cue{{TimeMS: 1000, Preset: "a", Intensity: 1.0}, {TimeMS: 4000, Preset: "b", Intensity: 1.1}})
	if !SameCueTiming([]ShowSpec{base, other}) {
		t.Fatal("intensity-only changes must keep timing")
	}
}

func TestSameCueTimingRejectsDrift(t *testing.T) {
	base := cueSpec([]Cue{{TimeMS: 1000, Preset: "a"}})
	if SameCueTiming([]ShowSpec{base, cueSpec([]Cue{{TimeMS: 1100, Preset: "a"}})}) {
		t.Fatal("expected cue time drift")
	}
	if SameCueTiming([]ShowSpec{base, cueSpec([]Cue{{TimeMS: 1000, Preset: "b"}})}) {
		t.Fatal("expected preset drift")
	}
	if SameCueTiming([]ShowSpec{base, cueSpec(nil)}) {
		t.Fatal("expected cue count drift")
	}
}
