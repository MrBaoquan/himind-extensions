package validate

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type manifest struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Author       string        `json:"author"`
	Categories   []string      `json:"categories"`
	Description  string        `json:"description"`
	ReleaseNotes string        `json:"release_notes"`
	Version      string        `json:"version"`
	Entry        string        `json:"entry"`
	Runtime      string        `json:"runtime"`
	Platforms    []string      `json:"platforms"`
	MinAgent     string        `json:"min_agent_version"`
	Governance   string        `json:"governance"`
	Capabilities []capability  `json:"capabilities"`
	Permissions  []string      `json:"permissions"`
	Contributes  contributions `json:"contributes"`
}

type capability struct {
	ID          string          `json:"id"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	RiskLevel   string          `json:"risk_level"`
}

type contributions struct {
	Views    []view    `json:"views"`
	Commands []command `json:"commands"`
}

type view struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Location string `json:"location"`
	Entry    string `json:"entry"`
}

type command struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	platforms      = map[string]bool{"windows-x64": true, "windows-arm64": true}
	governances    = map[string]bool{"required": true, "managed": true, "optional": true, "blocked": true}
	runtimes       = map[string]bool{"process-jsonrpc-stdio": true}
	riskLevels     = map[string]bool{"read_only": true, "local_write": true, "process": true, "network": true, "system": true, "builtin_policy": true}
)

func Main() {
	input := flag.String("path", "", "plugin directory or .hmpkg/.zip file")
	flag.Parse()
	if strings.TrimSpace(*input) == "" {
		fail("-path is required")
	}
	if err := ValidatePath(*input); err != nil {
		fail(err.Error())
	}
	fmt.Printf("valid plugin: %s\n", *input)
}

func fail(message string) {
	fmt.Fprintf(os.Stderr, "invalid plugin: %s\n", message)
	os.Exit(1)
}

func ValidatePath(input string) error {
	info, err := os.Stat(input)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ValidateDirectory(input)
	}
	return ValidateArchive(input)
}

func ValidateDirectory(root string) error {
	manifestPath := filepath.Join(root, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("plugin.json: %w", err)
	}
	item, err := ParseManifest(data)
	if err != nil {
		return err
	}
	if err := validateManifestFiles(root, item); err != nil {
		return err
	}
	return validateDirectoryPaths(root)
}

func ValidateArchive(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer archive.Close()
	seen := map[string]bool{}
	var manifestData []byte
	files := map[string][]byte{}
	for _, file := range archive.File {
		name := filepath.ToSlash(file.Name)
		if err := validateArchivePath(name); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("duplicate archive path: %s", name)
		}
		seen[name] = true
		if file.FileInfo().IsDir() {
			continue
		}
		reader, readErr := file.Open()
		if readErr != nil {
			return readErr
		}
		content, readErr := ioReadAllLimit(reader, 64<<20)
		_ = reader.Close()
		if readErr != nil {
			return fmt.Errorf("archive entry %s: %w", name, readErr)
		}
		files[name] = content
		if name == "plugin.json" {
			manifestData = content
		}
	}
	if manifestData == nil {
		return errors.New("archive must contain plugin.json at root")
	}
	item, err := ParseManifest(manifestData)
	if err != nil {
		return err
	}
	if !seen[item.Entry] {
		return fmt.Errorf("entry is missing from archive: %s", item.Entry)
	}
	return validateArchiveChecksums(files)
}

func validateArchiveChecksums(files map[string][]byte) error {
	data, ok := files["checksums.sha256"]
	if !ok {
		return errors.New("archive must contain checksums.sha256 at root")
	}
	expected := map[string]string{}
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 {
			return fmt.Errorf("invalid checksums.sha256 line %d", lineNumber+1)
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return fmt.Errorf("invalid checksum on line %d", lineNumber+1)
		}
		name := parts[1]
		if name == "checksums.sha256" || validateArchivePath(name) != nil {
			return fmt.Errorf("invalid checksum path on line %d: %s", lineNumber+1, name)
		}
		if _, duplicate := expected[name]; duplicate {
			return fmt.Errorf("duplicate checksum path: %s", name)
		}
		expected[name] = strings.ToLower(parts[0])
	}
	for name, content := range files {
		if name == "checksums.sha256" {
			continue
		}
		checksum, ok := expected[name]
		if !ok {
			return fmt.Errorf("archive file is not covered by checksums.sha256: %s", name)
		}
		actual := sha256.Sum256(content)
		if checksum != hex.EncodeToString(actual[:]) {
			return fmt.Errorf("archive checksum mismatch: %s", name)
		}
		delete(expected, name)
	}
	for name := range expected {
		return fmt.Errorf("checksums.sha256 references missing file: %s", name)
	}
	return nil
}

func ParseManifest(data []byte) (manifest, error) {
	var item manifest
	if err := json.Unmarshal(data, &item); err != nil {
		return item, fmt.Errorf("plugin.json: invalid JSON: %w", err)
	}
	if !idPattern.MatchString(item.ID) {
		return item, errors.New("id must use lowercase reverse-domain style, for example com.himind.example.tool")
	}
	if strings.TrimSpace(item.Name) == "" {
		return item, errors.New("name is required")
	}
	if strings.TrimSpace(item.Author) == "" {
		return item, errors.New("author is required")
	}
	if len(item.Categories) == 0 {
		return item, errors.New("categories is required")
	}
	if strings.TrimSpace(item.Description) == "" {
		return item, errors.New("description is required")
	}
	if strings.TrimSpace(item.ReleaseNotes) == "" {
		return item, errors.New("release_notes is required")
	}
	if !versionPattern.MatchString(item.Version) {
		return item, errors.New("version must use semantic version format, for example 1.0.0")
	}
	if filepath.IsAbs(item.Entry) || invalidRelativePath(item.Entry) {
		return item, errors.New("entry must be a relative path inside the package")
	}
	if !runtimes[item.Runtime] {
		return item, fmt.Errorf("unsupported runtime: %s", item.Runtime)
	}
	if len(item.Platforms) == 0 {
		item.Platforms = []string{"windows-x64"}
	}
	for _, platform := range item.Platforms {
		if !platforms[platform] {
			return item, fmt.Errorf("unsupported platform: %s", platform)
		}
	}
	if !governances[item.Governance] {
		return item, fmt.Errorf("unsupported governance: %s", item.Governance)
	}
	capabilityIDs := map[string]bool{}
	for _, capability := range item.Capabilities {
		if strings.TrimSpace(capability.ID) == "" || capabilityIDs[capability.ID] {
			return item, fmt.Errorf("capability IDs must be non-empty and unique")
		}
		capabilityIDs[capability.ID] = true
		if !riskLevels[capability.RiskLevel] {
			return item, fmt.Errorf("unsupported risk_level for %s: %s", capability.ID, capability.RiskLevel)
		}
		if len(capability.InputSchema) == 0 || !json.Valid(capability.InputSchema) {
			return item, fmt.Errorf("input_schema must be valid JSON for %s", capability.ID)
		}
	}
	viewIDs := map[string]bool{}
	for _, view := range item.Contributes.Views {
		if strings.TrimSpace(view.ID) == "" || viewIDs[view.ID] {
			return item, errors.New("view IDs must be non-empty and unique")
		}
		viewIDs[view.ID] = true
		if filepath.IsAbs(view.Entry) || invalidRelativePath(view.Entry) {
			return item, fmt.Errorf("view entry must be relative: %s", view.Entry)
		}
	}
	return item, nil
}

func validateManifestFiles(root string, item manifest) error {
	entry := filepath.Join(root, filepath.FromSlash(item.Entry))
	info, err := os.Stat(entry)
	if err != nil {
		if _, sourceErr := os.Stat(filepath.Join(root, "main.go")); sourceErr == nil {
			return validateViewFiles(root, item)
		}
		return fmt.Errorf("entry is missing: %s", item.Entry)
	}
	if info.IsDir() {
		return fmt.Errorf("entry must be a file: %s", item.Entry)
	}
	return validateViewFiles(root, item)
}

func ReadManifest(root string) (manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return manifest{}, err
	}
	return ParseManifest(data)
}

func validateViewFiles(root string, item manifest) error {
	for _, view := range item.Contributes.Views {
		path := filepath.Join(root, filepath.FromSlash(view.Entry))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return fmt.Errorf("view entry is missing or not a file: %s", view.Entry)
		}
	}
	return nil
}

func validateDirectoryPaths(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative != "." && invalidDirectoryPath(relative) {
			return fmt.Errorf("invalid plugin path: %s", relative)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed: %s", relative)
		}
		return nil
	})
}

func validateArchivePath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, ":") || strings.Contains(path, "\\") || invalidRelativePath(path) {
		return fmt.Errorf("invalid archive path: %s", path)
	}
	return nil
}

func invalidRelativePath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return true
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == "." || clean == ".." || strings.HasPrefix(clean, "../")
}

func invalidDirectoryPath(path string) bool {
	return invalidRelativePath(filepath.ToSlash(path))
}

func ioReadAllLimit(reader io.Reader, limit int64) ([]byte, error) {
	data := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	for int64(len(data)) <= limit {
		count, err := reader.Read(buffer)
		data = append(data, buffer[:count]...)
		if int64(len(data)) > limit {
			return nil, errors.New("file exceeds size limit")
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return data, nil
			}
			return nil, err
		}
	}
	return nil, errors.New("file exceeds size limit")
}
