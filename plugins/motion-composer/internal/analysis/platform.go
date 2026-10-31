package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type PlatformAxis struct {
	Role            string   `json:"role"`
	Channel         string   `json:"channel"`
	ValueType       string   `json:"value_type"`
	Unit            string   `json:"unit"`
	Min             float64  `json:"min"`
	Max             float64  `json:"max"`
	Neutral         float64  `json:"neutral"`
	MaxSlewRate     *float64 `json:"max_slew_rate"`
	Direction       string   `json:"direction"`
	DirectionStatus string   `json:"direction_status"`
}

type PlatformSemantic struct {
	Status           string   `json:"status"`
	OutputRoles      []string `json:"output_roles"`
	RequiresApproval bool     `json:"requires_approval"`
	Reason           string   `json:"reason,omitempty"`
}

type PlatformProfile struct {
	SchemaVersion       int                         `json:"schema_version"`
	PlatformProfileID   string                      `json:"platform_profile_id"`
	DisplayName         string                      `json:"display_name"`
	Plugin              string                      `json:"plugin"`
	CapabilityClass     string                      `json:"capability_class"`
	ProtocolVariant     string                      `json:"protocol_variant"`
	ActiveCommand       *string                     `json:"active_command"`
	MotionAxes          []PlatformAxis              `json:"motion_axes"`
	Semantics           map[string]PlatformSemantic `json:"semantics"`
	ProtocolConstraints map[string]json.RawMessage  `json:"protocol_constraints"`
}

type MotionIntent struct {
	Phrases []IntentPhrase `json:"phrases"`
}

type IntentPhrase struct {
	ID       string `json:"id"`
	Intent   string `json:"intent"`
	Semantic string `json:"semantic"`
}

type PlatformBindingPhrase struct {
	ID               string   `json:"id"`
	Semantic         string   `json:"semantic"`
	Status           string   `json:"status"`
	OutputRoles      []string `json:"output_roles"`
	RequiresApproval bool     `json:"requires_approval"`
	Reason           string   `json:"reason,omitempty"`
}

type PlatformBinding struct {
	SchemaVersion         int                     `json:"schema_version"`
	PlatformProfileID     string                  `json:"platform_profile_id"`
	PlatformProfileSHA256 string                  `json:"platform_profile_sha256"`
	DeviceProfileSHA256   string                  `json:"device_profile_sha256,omitempty"`
	OverallStatus         string                  `json:"overall_status"`
	OK                    bool                    `json:"ok"`
	Phrases               []PlatformBindingPhrase `json:"phrases"`
	RequiredApprovals     []string                `json:"required_approvals"`
	UnsupportedSemantics  []string                `json:"unsupported_semantics"`
}

func CompilePlatformBinding(profilePayload, intentPayload []byte) (*PlatformBinding, error) {
	return CompilePlatformBindingWithDevice(profilePayload, intentPayload, nil)
}

func CompilePlatformBindingWithDevice(profilePayload, intentPayload, devicePayload []byte) (*PlatformBinding, error) {
	profilePayload = trimJSONBOM(profilePayload)
	intentPayload = trimJSONBOM(intentPayload)
	devicePayload = trimJSONBOM(devicePayload)

	var profile PlatformProfile
	if err := json.Unmarshal(profilePayload, &profile); err != nil {
		return nil, fmt.Errorf("platform profile JSON: %w", err)
	}
	if err := validatePlatformProfile(profile); err != nil {
		return nil, err
	}
	deviceHash := ""
	if len(devicePayload) > 0 {
		var device DeviceProfile
		if err := json.Unmarshal(devicePayload, &device); err != nil {
			return nil, fmt.Errorf("device profile JSON: %w", err)
		}
		if err := validatePlatformAgainstDevice(profile, device); err != nil {
			return nil, err
		}
		deviceHash = sha256Hex(devicePayload)
	}

	var intent MotionIntent
	if err := json.Unmarshal(intentPayload, &intent); err != nil {
		return nil, fmt.Errorf("motion intent JSON: %w", err)
	}
	if len(intent.Phrases) == 0 {
		return nil, errors.New("motion intent requires at least one phrase")
	}

	axes := map[string]bool{}
	for _, axis := range profile.MotionAxes {
		axes[axis.Role] = true
	}
	seenPhraseIDs := map[string]bool{}
	binding := &PlatformBinding{
		SchemaVersion:         1,
		PlatformProfileID:     profile.PlatformProfileID,
		PlatformProfileSHA256: sha256Hex(profilePayload),
		DeviceProfileSHA256:   deviceHash,
		OverallStatus:         "exact",
		OK:                    true,
		Phrases:               []PlatformBindingPhrase{},
		RequiredApprovals:     []string{},
		UnsupportedSemantics:  []string{},
	}

	requiredApprovals := map[string]bool{}
	unsupported := map[string]bool{}
	for _, phrase := range intent.Phrases {
		id := strings.TrimSpace(phrase.ID)
		if id == "" {
			return nil, errors.New("motion intent phrase id cannot be empty")
		}
		if seenPhraseIDs[id] {
			return nil, fmt.Errorf("duplicate motion intent phrase id %q", id)
		}
		seenPhraseIDs[id] = true
		semantic := normalizeSemantic(firstNonEmpty(phrase.Semantic, phrase.Intent))
		if semantic == "" {
			return nil, fmt.Errorf("phrase %q has no semantic or intent", id)
		}

		mapped, ok := profile.Semantics[semantic]
		if !ok {
			mapped = PlatformSemantic{
				Status:           "unsupported",
				OutputRoles:      []string{},
				RequiresApproval: false,
				Reason:           fmt.Sprintf("platform profile %q has no semantics mapping for %q", profile.PlatformProfileID, semantic),
			}
		}
		if err := validateSemanticMapping(profile.PlatformProfileID, semantic, mapped, axes); err != nil {
			return nil, err
		}

		item := PlatformBindingPhrase{
			ID:               id,
			Semantic:         semantic,
			Status:           mapped.Status,
			OutputRoles:      append([]string(nil), mapped.OutputRoles...),
			RequiresApproval: mapped.RequiresApproval,
			Reason:           mapped.Reason,
		}
		binding.Phrases = append(binding.Phrases, item)

		switch mapped.Status {
		case "unsupported":
			binding.OverallStatus = "unsupported"
			binding.OK = false
			unsupported[semantic] = true
		case "approximate":
			if binding.OverallStatus == "exact" {
				binding.OverallStatus = "approximate"
			}
			if mapped.RequiresApproval {
				requiredApprovals[id] = true
			}
		}
	}

	for id := range requiredApprovals {
		binding.RequiredApprovals = append(binding.RequiredApprovals, id)
	}
	sort.Strings(binding.RequiredApprovals)
	for semantic := range unsupported {
		binding.UnsupportedSemantics = append(binding.UnsupportedSemantics, semantic)
	}
	sort.Strings(binding.UnsupportedSemantics)
	return binding, nil
}

func validatePlatformProfile(profile PlatformProfile) error {
	if profile.SchemaVersion != 1 {
		return errors.New("platform profile schema_version must be 1")
	}
	if strings.TrimSpace(profile.PlatformProfileID) == "" {
		return errors.New("platform_profile_id is required")
	}
	if strings.TrimSpace(profile.Plugin) == "" {
		return errors.New("platform plugin is required")
	}
	switch profile.CapabilityClass {
	case "heave_only", "pitch_roll_heave", "pitch_roll", "pitch_roll_legacy", "full_6dof":
	default:
		return fmt.Errorf("unsupported capability_class %q", profile.CapabilityClass)
	}
	if len(profile.MotionAxes) == 0 {
		return errors.New("platform profile requires at least one motion axis")
	}
	seen := map[string]bool{}
	for _, axis := range profile.MotionAxes {
		if strings.TrimSpace(axis.Role) == "" || strings.TrimSpace(axis.Channel) == "" {
			return errors.New("motion axis role and channel are required")
		}
		if seen[axis.Role] {
			return fmt.Errorf("duplicate motion axis role %q", axis.Role)
		}
		seen[axis.Role] = true
		if axis.ValueType != "float" {
			return fmt.Errorf("motion axis %q must be float", axis.Role)
		}
		if axis.Min >= axis.Max {
			return fmt.Errorf("motion axis %q min must be less than max", axis.Role)
		}
		if axis.Neutral < axis.Min || axis.Neutral > axis.Max {
			return fmt.Errorf("motion axis %q neutral must be within range", axis.Role)
		}
		if axis.DirectionStatus != "confirmed" && axis.DirectionStatus != "provisional" {
			return fmt.Errorf("motion axis %q direction_status is invalid", axis.Role)
		}
	}
	if profile.ActiveCommand != nil && strings.TrimSpace(*profile.ActiveCommand) == "" {
		return errors.New("active_command must be null or non-empty")
	}
	if len(profile.Semantics) == 0 {
		return errors.New("platform profile requires semantics mappings")
	}
	return nil
}

func validatePlatformAgainstDevice(profile PlatformProfile, device DeviceProfile) error {
	if strings.TrimSpace(device.Device) == "" {
		return errors.New("device profile device is required")
	}
	if len(device.Axes) == 0 {
		return errors.New("device profile requires at least one axis")
	}
	deviceAxes := map[string]DeviceAxis{}
	for _, axis := range device.Axes {
		deviceAxes[axis.Role] = axis
	}
	for _, axis := range profile.MotionAxes {
		actual, ok := deviceAxes[axis.Role]
		if !ok {
			return fmt.Errorf("platform axis %q is missing from device profile", axis.Role)
		}
		if actual.Channel != axis.Channel {
			return fmt.Errorf("platform axis %q uses channel %q but device profile declares %q", axis.Role, axis.Channel, actual.Channel)
		}
		if actual.ValueType != axis.ValueType {
			return fmt.Errorf("platform axis %q value_type %q does not match device profile %q", axis.Role, axis.ValueType, actual.ValueType)
		}
	}
	return nil
}

func validateSemanticMapping(profileID, semantic string, mapped PlatformSemantic, axes map[string]bool) error {
	switch mapped.Status {
	case "exact", "approximate", "unsupported":
	default:
		return fmt.Errorf("platform %q semantic %q has invalid status %q", profileID, semantic, mapped.Status)
	}
	if mapped.Status == "unsupported" {
		if len(mapped.OutputRoles) != 0 {
			return fmt.Errorf("unsupported semantic %q cannot declare output roles", semantic)
		}
		return nil
	}
	if len(mapped.OutputRoles) == 0 {
		return fmt.Errorf("semantic %q must declare output roles", semantic)
	}
	for _, role := range mapped.OutputRoles {
		if !axes[role] {
			return fmt.Errorf("semantic %q references unavailable motion axis %q", semantic, role)
		}
	}
	if mapped.Status == "approximate" && !mapped.RequiresApproval {
		return fmt.Errorf("approximate semantic %q must require approval", semantic)
	}
	return nil
}

func normalizeSemantic(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func trimJSONBOM(payload []byte) []byte {
	if len(payload) >= 3 && payload[0] == 0xEF && payload[1] == 0xBB && payload[2] == 0xBF {
		return payload[3:]
	}
	return payload
}
