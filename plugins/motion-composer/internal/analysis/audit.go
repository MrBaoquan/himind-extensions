package analysis

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// ShowSpec is the subset of IOStudio's ShowSpec needed for deterministic motion review.
type ShowSpec struct {
	Name     string    `json:"name"`
	Duration int64     `json:"duration_ms"`
	Tracks   []Track   `json:"tracks"`
	Presets  []Preset  `json:"presets"`
	Cues     []Cue     `json:"cues"`
	Gates    []Gate    `json:"gates"`
	Chapters []Chapter `json:"chapters"`
}

type Track struct {
	Role             string  `json:"role"`
	Channel          string  `json:"channel"`
	OutputType       string  `json:"output_type"`
	OAction          string  `json:"oaction"`
	ValueType        string  `json:"value_type"`
	Neutral          float64 `json:"neutral"`
	EngineeringRange float64 `json:"engineering_range"`
}

type Preset struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	DurationMS int64           `json:"duration_ms"`
	Channels   []PresetChannel `json:"channels"`
}

type PresetChannel struct {
	Role      string     `json:"role"`
	Keyframes []Keyframe `json:"keyframes"`
}

type Keyframe struct {
	TimeMS int64   `json:"time_ms"`
	Value  float64 `json:"value"`
}

type Cue struct {
	TimeMS    int64   `json:"time_ms"`
	Preset    string  `json:"preset"`
	Intensity float64 `json:"intensity"`
}

type Gate struct {
	Role    string  `json:"role"`
	StartMS int64   `json:"start_ms"`
	EndMS   int64   `json:"end_ms"`
	FadeMS  int64   `json:"fade_ms"`
	Level   float64 `json:"level"`
}

type Chapter struct {
	TimeMS int64  `json:"time_ms"`
	Name   string `json:"name"`
}

type Timeline struct {
	Segments []TimelineSegment `json:"segments"`
	Events   []TimelineEvent   `json:"events"`
}

type TimelineSegment struct {
	ID       string  `json:"id"`
	StartMS  int64   `json:"start_ms"`
	EndMS    int64   `json:"end_ms"`
	Behavior string  `json:"behavior"`
	Scene    string  `json:"scene"`
	Energy   float64 `json:"energy"`
}

type TimelineEvent struct {
	TimeMS    int64   `json:"time_ms"`
	Type      string  `json:"type"`
	Intensity float64 `json:"intensity"`
}

type Thresholds struct {
	HighEnergy              float64 `json:"high_energy"`
	MinHighEnergyRange      float64 `json:"min_high_energy_range"`
	LongSegmentMS           int64   `json:"long_segment_ms"`
	MinStrongDrop           float64 `json:"min_strong_drop"`
	MaxStrongDropDurationMS int64   `json:"max_strong_drop_duration_ms"`
	MinRebound              float64 `json:"min_rebound"`
	MinDropRatePerSecond    float64 `json:"min_drop_rate_per_second"`
	EndShutdownMarginMS     int64   `json:"end_shutdown_margin_ms"`
	MaxStaticGapMS          int64   `json:"max_static_gap_ms"`
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		HighEnergy:              0.55,
		MinHighEnergyRange:      0.35,
		LongSegmentMS:           12000,
		MinStrongDrop:           0.33,
		MaxStrongDropDurationMS: 2300,
		MinRebound:              0.10,
		MinDropRatePerSecond:    0.28,
		EndShutdownMarginMS:     1500,
		MaxStaticGapMS:          3000,
	}
}

type DeviceProfile struct {
	Device string       `json:"device"`
	Axes   []DeviceAxis `json:"axes"`
}

type DeviceAxis struct {
	Role        string  `json:"role"`
	Channel     string  `json:"channel"`
	ValueType   string  `json:"value_type"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
	Neutral     float64 `json:"neutral"`
	MaxSlewRate float64 `json:"max_slew_rate"`
}

type Issue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	SegmentID string `json:"segment_id,omitempty"`
	TimeMS    int64  `json:"time_ms,omitempty"`
	Message   string `json:"message"`
}

type SegmentMetrics struct {
	ID              string  `json:"id"`
	StartMS         int64   `json:"start_ms"`
	EndMS           int64   `json:"end_ms"`
	Behavior        string  `json:"behavior,omitempty"`
	Energy          float64 `json:"energy"`
	PeakHeave       float64 `json:"peak_heave"`
	TroughHeave     float64 `json:"trough_heave"`
	DynamicRange    float64 `json:"dynamic_range"`
	ActiveRatio     float64 `json:"active_ratio"`
	MaxDownRate     float64 `json:"max_down_rate_per_second"`
	StrongPulses    int     `json:"strong_pulses"`
	ContinuityGap   float64 `json:"continuity_gap"`
	MaxStaticGapMS  int64   `json:"max_static_gap_ms"`
	EvidenceAligned bool    `json:"evidence_aligned"`
}

type Summary struct {
	DurationMS             int64   `json:"duration_ms"`
	CueCount               int     `json:"cue_count"`
	SegmentCount           int     `json:"segment_count"`
	HighEnergySegments     int     `json:"high_energy_segments"`
	StrongPulseCount       int     `json:"strong_pulse_count"`
	PeakHeave              float64 `json:"peak_heave"`
	MeanDynamicRange       float64 `json:"mean_dynamic_range"`
	WeakHighEnergySegments int     `json:"weak_high_energy_segments"`
	MaxDownRatePerSecond   float64 `json:"max_down_rate_per_second"`
}

type Report struct {
	OK          bool             `json:"ok"`
	Spec        string           `json:"spec"`
	Device      string           `json:"device,omitempty"`
	MaxSlewRate float64          `json:"max_slew_rate,omitempty"`
	Thresholds  Thresholds       `json:"thresholds"`
	Summary     Summary          `json:"summary"`
	Segments    []SegmentMetrics `json:"segments"`
	Issues      []Issue          `json:"issues"`
}

// Audit applies deterministic, non-physical quality checks. IOStudio remains the
// authority for schema, channel, range, interlock and first-frame safety checks.
func Audit(spec ShowSpec, specName string, timeline *Timeline, profile *DeviceProfile, thresholds Thresholds) Report {
	report := Report{OK: true, Spec: specName, Thresholds: thresholds, Issues: []Issue{}, Segments: []SegmentMetrics{}}
	var maxSlew float64
	if profile != nil {
		report.Device = profile.Device
		for _, axis := range profile.Axes {
			if axis.Role == "heave" {
				maxSlew = axis.MaxSlewRate
				break
			}
		}
		report.MaxSlewRate = maxSlew
	}

	presets := map[string]Preset{}
	for _, preset := range spec.Presets {
		presets[preset.ID] = preset
	}
	tracks := map[string]Track{}
	for _, track := range spec.Tracks {
		tracks[track.Role] = track
	}

	cues := append([]Cue(nil), spec.Cues...)
	sort.Slice(cues, func(i, j int) bool { return cues[i].TimeMS < cues[j].TimeMS })
	if len(cues) == 0 {
		report.Issues = append(report.Issues, Issue{Code: "spec.no_cues", Severity: "error", Message: "spec has no cues"})
	}

	roleEnd := map[string]int64{}
	roleEndValue := map[string]float64{}
	for _, cue := range cues {
		preset, ok := presets[cue.Preset]
		if !ok {
			report.Issues = append(report.Issues, Issue{Code: "cue.preset_missing", Severity: "error", TimeMS: cue.TimeMS, Message: fmt.Sprintf("cue references missing preset %q", cue.Preset)})
			continue
		}
		end := cue.TimeMS + preset.DurationMS
		if cue.TimeMS < 0 || end > spec.Duration {
			report.Issues = append(report.Issues, Issue{Code: "cue.out_of_bounds", Severity: "error", TimeMS: cue.TimeMS, Message: fmt.Sprintf("cue %q ends at %dms, beyond duration %dms", cue.Preset, end, spec.Duration)})
		}
		role, keys := heaveChannel(preset)
		if role == "" {
			continue
		}
		if prevEnd, exists := roleEnd[role]; exists && cue.TimeMS < prevEnd {
			report.Issues = append(report.Issues, Issue{Code: "cue.role_overlap", Severity: "error", TimeMS: cue.TimeMS, Message: fmt.Sprintf("cue %q overlaps role %s occupied until %dms", cue.Preset, role, prevEnd)})
		}
		roleEnd[role] = end

		metrics := segmentMetrics(preset.ID, cue.TimeMS, end, keys, thresholds)
		if seg := segmentAt(timeline, cue.TimeMS); seg != nil {
			metrics.ID = seg.ID
			metrics.Behavior = seg.Behavior
			metrics.Energy = seg.Energy
		}
		metrics.ContinuityGap = math.Abs(keys[0].Value - roleEndValue[role])
		if _, exists := roleEndValue[role]; !exists {
			metrics.ContinuityGap = 0
		}
		metrics.EvidenceAligned = alignedToEvent(timeline, metrics.StartMS, metrics.EndMS, keys, thresholds)
		roleEndValue[role] = keys[len(keys)-1].Value

		if metrics.Energy >= thresholds.HighEnergy && metrics.DynamicRange < thresholds.MinHighEnergyRange {
			report.Issues = append(report.Issues, Issue{Code: "motion.high_energy_flat", Severity: "error", SegmentID: metrics.ID, TimeMS: metrics.StartMS, Message: fmt.Sprintf("high-energy segment %.2f has only %.3f normalized heave range", metrics.Energy, metrics.DynamicRange)})
		}
		if metrics.Energy >= thresholds.HighEnergy && metrics.EndMS-metrics.StartMS >= thresholds.LongSegmentMS && metrics.StrongPulses == 0 {
			report.Issues = append(report.Issues, Issue{Code: "motion.no_weightless_pulse", Severity: "error", SegmentID: metrics.ID, TimeMS: metrics.StartMS, Message: "long segment has no qualifying lift/hold/drop/rebound pulse"})
		}
		if metrics.MaxStaticGapMS > thresholds.MaxStaticGapMS {
			report.Issues = append(report.Issues, Issue{Code: "motion.long_static_gap", Severity: "error", SegmentID: metrics.ID, TimeMS: metrics.StartMS, Message: fmt.Sprintf("static low-motion gap %dms exceeds %dms", metrics.MaxStaticGapMS, thresholds.MaxStaticGapMS)})
		}
		if metrics.ContinuityGap > 0.15 {
			report.Issues = append(report.Issues, Issue{Code: "motion.continuity_gap", Severity: "warning", SegmentID: metrics.ID, TimeMS: metrics.StartMS, Message: fmt.Sprintf("boundary value gap %.3f", metrics.ContinuityGap)})
		}
		if maxSlew > 0 && metrics.MaxDownRate > maxSlew+1e-9 {
			report.Issues = append(report.Issues, Issue{Code: "motion.drop_rate_exceeds_slew", Severity: "error", SegmentID: metrics.ID, TimeMS: metrics.StartMS, Message: fmt.Sprintf("down rate %.3f/s exceeds declared %.3f/s", metrics.MaxDownRate, maxSlew)})
		}
		report.Segments = append(report.Segments, metrics)
	}

	auditGates(spec, tracks, &report)
	if track, ok := tracks["heave"]; ok {
		if finalValue, exists := roleEndValue["heave"]; exists && math.Abs(finalValue-track.Neutral) > 0.01 {
			report.Issues = append(report.Issues, Issue{Code: "motion.end_not_neutral", Severity: "error", TimeMS: spec.Duration, Message: fmt.Sprintf("heave ends at %.3f instead of neutral %.3f", finalValue, track.Neutral)})
		}
	}
	for _, gate := range spec.Gates {
		if gate.EndMS > spec.Duration-thresholds.EndShutdownMarginMS {
			report.Issues = append(report.Issues, Issue{Code: "motion.end_effects_not_closed", Severity: "error", TimeMS: gate.StartMS, Message: fmt.Sprintf("role %q remains active until %dms; close it at least %dms before the end", gate.Role, gate.EndMS, thresholds.EndShutdownMarginMS)})
		}
	}
	report.Summary = summarize(spec.Duration, len(cues), report.Segments, thresholds)
	for _, issue := range report.Issues {
		if issue.Severity == "error" {
			report.OK = false
		}
	}
	return report
}

func heaveChannel(preset Preset) (string, []Keyframe) {
	for _, channel := range preset.Channels {
		if channel.Role == "heave" && len(channel.Keyframes) > 0 {
			keys := append([]Keyframe(nil), channel.Keyframes...)
			sort.Slice(keys, func(i, j int) bool { return keys[i].TimeMS < keys[j].TimeMS })
			return channel.Role, keys
		}
	}
	return "", nil
}

func segmentMetrics(id string, start, end int64, keys []Keyframe, thresholds Thresholds) SegmentMetrics {
	m := SegmentMetrics{ID: id, StartMS: start, EndMS: end}
	if len(keys) == 0 {
		return m
	}
	m.PeakHeave, m.TroughHeave = keys[0].Value, keys[0].Value
	for _, key := range keys {
		m.PeakHeave = math.Max(m.PeakHeave, key.Value)
		m.TroughHeave = math.Min(m.TroughHeave, key.Value)
	}
	m.DynamicRange = m.PeakHeave - m.TroughHeave
	base := keys[0].Value
	active := 0
	samples := 0
	staticStart := int64(0)
	staticMin, staticMax := sample(keys, 0), sample(keys, 0)
	for t := int64(0); t <= end-start; t += 100 {
		v := sample(keys, t)
		if v >= base+0.08 {
			active++
		}
		if v < staticMin {
			staticMin = v
		}
		if v > staticMax {
			staticMax = v
		}
		if staticMax-staticMin <= 0.02 {
			if gap := t - staticStart; gap > m.MaxStaticGapMS {
				m.MaxStaticGapMS = gap
			}
		} else {
			staticStart = t
			staticMin, staticMax = v, v
		}
		samples++
	}
	if samples > 0 {
		m.ActiveRatio = float64(active) / float64(samples)
	}
	for i := 0; i+1 < len(keys); i++ {
		a, b := keys[i], keys[i+1]
		if b.Value >= a.Value {
			continue
		}
		drop := a.Value - b.Value
		dt := b.TimeMS - a.TimeMS
		if dt <= 0 {
			continue
		}
		rate := drop / (float64(dt) / 1000.0)
		m.MaxDownRate = math.Max(m.MaxDownRate, rate)
		if drop < thresholds.MinStrongDrop || dt > thresholds.MaxStrongDropDurationMS || rate < thresholds.MinDropRatePerSecond {
			continue
		}
		rebound := 0.0
		for j := i + 2; j < len(keys); j++ {
			if keys[j].Value > b.Value {
				rebound = math.Max(rebound, keys[j].Value-b.Value)
			}
			if keys[j].TimeMS-b.TimeMS > 1800 {
				break
			}
		}
		if rebound >= thresholds.MinRebound {
			m.StrongPulses++
		}
	}
	return m
}

func sample(keys []Keyframe, at int64) float64 {
	if at <= keys[0].TimeMS {
		return keys[0].Value
	}
	if at >= keys[len(keys)-1].TimeMS {
		return keys[len(keys)-1].Value
	}
	for i := 0; i+1 < len(keys); i++ {
		a, b := keys[i], keys[i+1]
		if at < a.TimeMS || at > b.TimeMS {
			continue
		}
		span := b.TimeMS - a.TimeMS
		if span <= 0 {
			return b.Value
		}
		ratio := float64(at-a.TimeMS) / float64(span)
		ratio = ratio * ratio * (3 - 2*ratio)
		return a.Value + (b.Value-a.Value)*ratio
	}
	return keys[len(keys)-1].Value
}

func segmentAt(timeline *Timeline, at int64) *TimelineSegment {
	if timeline == nil {
		return nil
	}
	for i := range timeline.Segments {
		if at >= timeline.Segments[i].StartMS && at < timeline.Segments[i].EndMS {
			return &timeline.Segments[i]
		}
	}
	if len(timeline.Segments) > 0 && at == timeline.Segments[len(timeline.Segments)-1].EndMS {
		return &timeline.Segments[len(timeline.Segments)-1]
	}
	return nil
}

func alignedToEvent(timeline *Timeline, start, end int64, keys []Keyframe, thresholds Thresholds) bool {
	if timeline == nil || len(timeline.Events) == 0 {
		return false
	}
	for _, event := range timeline.Events {
		if event.TimeMS < start || event.TimeMS > end {
			continue
		}
		for i := 0; i+1 < len(keys); i++ {
			a, b := keys[i], keys[i+1]
			drop := a.Value - b.Value
			dt := b.TimeMS - a.TimeMS
			if dt <= 0 || drop < thresholds.MinStrongDrop || dt > thresholds.MaxStrongDropDurationMS {
				continue
			}
			trough := start + b.TimeMS
			if math.Abs(float64(trough-event.TimeMS)) <= 1800 {
				return true
			}
		}
	}
	return false
}

func auditGates(spec ShowSpec, tracks map[string]Track, report *Report) {
	byRole := map[string][]Gate{}
	for _, gate := range spec.Gates {
		track, ok := tracks[gate.Role]
		if !ok {
			report.Issues = append(report.Issues, Issue{Code: "gate.track_missing", Severity: "error", TimeMS: gate.StartMS, Message: fmt.Sprintf("gate role %q has no track", gate.Role)})
			continue
		}
		if gate.StartMS < 0 || gate.EndMS <= gate.StartMS || gate.EndMS > spec.Duration {
			report.Issues = append(report.Issues, Issue{Code: "gate.invalid_window", Severity: "error", TimeMS: gate.StartMS, Message: fmt.Sprintf("invalid gate window %d..%d", gate.StartMS, gate.EndMS)})
		}
		if track.ValueType == "bool" && gate.Level != 0 && gate.Level != 1 {
			report.Issues = append(report.Issues, Issue{Code: "gate.bool_level", Severity: "error", TimeMS: gate.StartMS, Message: fmt.Sprintf("bool role %q must use level 0 or 1", gate.Role)})
		}
		if track.ValueType == "float" && track.EngineeringRange <= 0 {
			report.Issues = append(report.Issues, Issue{Code: "gate.float_range_missing", Severity: "error", TimeMS: gate.StartMS, Message: fmt.Sprintf("float role %q needs engineering_range before gate scaling", gate.Role)})
		}
		byRole[gate.Role] = append(byRole[gate.Role], gate)
	}
	for role, gates := range byRole {
		sort.Slice(gates, func(i, j int) bool { return gates[i].StartMS < gates[j].StartMS })
		for i := 0; i+1 < len(gates); i++ {
			if gates[i].EndMS > gates[i+1].StartMS {
				report.Issues = append(report.Issues, Issue{Code: "gate.overlap", Severity: "error", TimeMS: gates[i+1].StartMS, Message: fmt.Sprintf("gate overlap on role %s", role)})
			}
		}
	}
}

func summarize(duration int64, cueCount int, segments []SegmentMetrics, thresholds Thresholds) Summary {
	summary := Summary{DurationMS: duration, CueCount: cueCount, SegmentCount: len(segments)}
	var ranges float64
	for _, segment := range segments {
		summary.StrongPulseCount += segment.StrongPulses
		summary.PeakHeave = math.Max(summary.PeakHeave, segment.PeakHeave)
		summary.MaxDownRatePerSecond = math.Max(summary.MaxDownRatePerSecond, segment.MaxDownRate)
		ranges += segment.DynamicRange
		if segment.Energy >= thresholds.HighEnergy {
			summary.HighEnergySegments++
			if segment.DynamicRange < thresholds.MinHighEnergyRange || segment.StrongPulses == 0 {
				summary.WeakHighEnergySegments++
			}
		}
	}
	if len(segments) > 0 {
		summary.MeanDynamicRange = ranges / float64(len(segments))
	}
	return summary
}

func DecodeSpec(payload []byte) (ShowSpec, error) {
	var spec ShowSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return spec, fmt.Errorf("parse show spec: %w", err)
	}
	return spec, nil
}

func DecodeTimeline(payload []byte) (*Timeline, error) {
	var timeline Timeline
	if err := json.Unmarshal(payload, &timeline); err != nil {
		return nil, fmt.Errorf("parse behavior timeline: %w", err)
	}
	return &timeline, nil
}

func DecodeProfile(payload []byte) (*DeviceProfile, error) {
	var profile DeviceProfile
	if err := json.Unmarshal(payload, &profile); err != nil {
		return nil, fmt.Errorf("parse device profile: %w", err)
	}
	return &profile, nil
}
