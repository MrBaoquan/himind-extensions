package analysis

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func profileBytes(t *testing.T, id string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "workflows", "video-to-motion", "profiles", id+".json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return payload
}

func intentJSON(phrases string) []byte {
	return []byte(`{"phrases":[` + phrases + `]}`)
}

func TestCompilePlatformBindingYDLExact(t *testing.T) {
	binding, err := CompilePlatformBinding(
		profileBytes(t, "mp-ydl-heave-v1"),
		intentJSON(`{"id":"p1","intent":"weightless_drop"},{"id":"p2","semantic":"flight-float"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.OverallStatus != "exact" || !binding.OK {
		t.Fatalf("unexpected binding: %+v", binding)
	}
	if len(binding.Phrases) != 2 {
		t.Fatalf("phrases=%d", len(binding.Phrases))
	}
	for _, phrase := range binding.Phrases {
		if phrase.Status != "exact" {
			t.Fatalf("phrase %s status=%s", phrase.ID, phrase.Status)
		}
		if len(phrase.OutputRoles) != 1 || phrase.OutputRoles[0] != "heave" {
			t.Fatalf("roles=%v", phrase.OutputRoles)
		}
	}
	if len(binding.PlatformProfileSHA256) != 64 {
		t.Fatalf("hash=%q", binding.PlatformProfileSHA256)
	}
}

func TestCompilePlatformBindingNJQKDropRequiresApproval(t *testing.T) {
	binding, err := CompilePlatformBinding(
		profileBytes(t, "mp-njqk-pitch-roll-v1"),
		intentJSON(`{"id":"drop","intent":"weightless_drop"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.OverallStatus != "approximate" || !binding.OK {
		t.Fatalf("unexpected binding: %+v", binding)
	}
	if len(binding.RequiredApprovals) != 1 || binding.RequiredApprovals[0] != "drop" {
		t.Fatalf("approvals=%v", binding.RequiredApprovals)
	}
	if got := binding.Phrases[0].OutputRoles; len(got) != 2 || got[0] != "pitch" || got[1] != "roll" {
		t.Fatalf("roles=%v", got)
	}
}

func TestCompilePlatformBindingNJLJA3TRejectsTheoreticalYaw(t *testing.T) {
	binding, err := CompilePlatformBinding(
		profileBytes(t, "mp-njlj-a3t-v1"),
		intentJSON(`{"id":"yaw","intent":"yaw_turn"},{"id":"drop","intent":"weightless_drop"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.OK || binding.OverallStatus != "unsupported" {
		t.Fatalf("unexpected binding: %+v", binding)
	}
	if binding.Phrases[0].Status != "unsupported" {
		t.Fatalf("yaw status=%s", binding.Phrases[0].Status)
	}
	if binding.Phrases[1].Status != "exact" {
		t.Fatalf("drop status=%s", binding.Phrases[1].Status)
	}
}

func TestCompilePlatformBindingLegacyYawUnsupported(t *testing.T) {
	binding, err := CompilePlatformBinding(
		profileBytes(t, "motion-plat-pitch-roll-v1"),
		intentJSON(`{"id":"yaw","intent":"yaw_turn"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.OK || len(binding.UnsupportedSemantics) != 1 || binding.UnsupportedSemantics[0] != "yaw_turn" {
		t.Fatalf("unexpected binding: %+v", binding)
	}
}

func TestCompilePlatformBindingUnknownSemanticIsUnsupported(t *testing.T) {
	binding, err := CompilePlatformBinding(
		profileBytes(t, "mp-ydl-heave-v1"),
		intentJSON(`{"id":"x","intent":"unknown_semantic"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.OK || binding.Phrases[0].Status != "unsupported" {
		t.Fatalf("unexpected binding: %+v", binding)
	}
}

func TestCompilePlatformBindingRejectsMappingToUnavailableAxis(t *testing.T) {
	profile := []byte(`{
      "schema_version":1,
      "platform_profile_id":"bad",
      "display_name":"bad",
      "plugin":"bad",
      "capability_class":"pitch_roll",
      "protocol_variant":"v1",
      "active_command":null,
      "motion_axes":[{"role":"pitch","channel":"OAxis_01","value_type":"float","unit":"deg","min":-1,"max":1,"neutral":0,"max_slew_rate":null,"direction":"unknown","direction_status":"provisional"}],
      "controls":[],"effects":[],
      "semantics":{"weightless_drop":{"status":"exact","output_roles":["heave"],"requires_approval":false}},
      "safety":{"safe_stop_mode":"reset_only","feedback":"none","tail_reset_required":true},
      "evidence":[{"kind":"test","path":"test","observed":"2026-10-30"}]
    }`)
	if _, err := CompilePlatformBinding(profile, intentJSON(`{"id":"drop","intent":"weightless_drop"}`)); err == nil {
		t.Fatal("expected unavailable axis error")
	}
}

func TestCompilePlatformBindingWithDeviceAcceptsMatchingChannels(t *testing.T) {
	device := []byte(`{"schema_version":1,"device":"YDL-0","axes":[{"role":"heave","channel":"OAxis_06","value_type":"float","min":0,"max":1000,"neutral":0,"max_slew_rate":0.5}]}`)
	binding, err := CompilePlatformBindingWithDevice(
		profileBytes(t, "mp-ydl-heave-v1"),
		intentJSON(`{"id":"drop","intent":"weightless_drop"}`),
		device,
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.DeviceProfileSHA256 == "" {
		t.Fatal("expected device profile hash")
	}
}

func TestCompilePlatformBindingWithDeviceRejectsWrongChannel(t *testing.T) {
	device := []byte(`{"schema_version":1,"device":"YDL-0","axes":[{"role":"heave","channel":"OAxis_07","value_type":"float","min":0,"max":1000,"neutral":0}]}`)
	if _, err := CompilePlatformBindingWithDevice(
		profileBytes(t, "mp-ydl-heave-v1"),
		intentJSON(`{"id":"drop","intent":"weightless_drop"}`),
		device,
	); err == nil {
		t.Fatal("expected wrong channel error")
	}
}

func TestCompilePlatformBindingWithDeviceRejectsWrongValueType(t *testing.T) {
	device := []byte(`{"schema_version":1,"device":"YDL-0","axes":[{"role":"heave","channel":"OAxis_06","value_type":"bool","neutral":0}]}`)
	if _, err := CompilePlatformBindingWithDevice(
		profileBytes(t, "mp-ydl-heave-v1"),
		intentJSON(`{"id":"drop","intent":"weightless_drop"}`),
		device,
	); err == nil {
		t.Fatal("expected wrong value_type error")
	}
}
