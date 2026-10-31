package analysis

// SameCueTiming reports whether every spec has the same cue count, preset order,
// cue time and intensity-independent cue structure. Intensity may differ by variant.
func SameCueTiming(specs []ShowSpec) bool {
	if len(specs) < 2 {
		return true
	}
	base := specs[0]
	for _, candidate := range specs[1:] {
		if len(candidate.Cues) != len(base.Cues) {
			return false
		}
		for i := range base.Cues {
			if candidate.Cues[i].TimeMS != base.Cues[i].TimeMS || candidate.Cues[i].Preset != base.Cues[i].Preset {
				return false
			}
		}
	}
	return true
}
