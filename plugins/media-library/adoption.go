package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type adoptItem struct {
	CandidateID           string   `json:"candidate_id"`
	LocalPath             string   `json:"local_path"`
	Provider              string   `json:"provider"`
	ProviderID            string   `json:"provider_id"`
	Kind                  string   `json:"kind"`
	Title                 string   `json:"title"`
	Author                string   `json:"author"`
	LicenseName           string   `json:"license_name"`
	LicenseURL            string   `json:"license_url"`
	RedistributionAllowed bool     `json:"redistribution_allowed"`
	SourceURL             string   `json:"source_url"`
	DownloadURL           string   `json:"download_url"`
	Filename              string   `json:"filename"`
	Tags                  []string `json:"tags"`
	Collection            string   `json:"collection"`
}

type adoptInput struct {
	LibraryID string      `json:"library_id"`
	Mode      string      `json:"mode"`
	Actor     string      `json:"actor"`
	Items     []adoptItem `json:"items"`
}

type catalogEvent struct {
	EventID        string   `json:"event_id"`
	Type           string   `json:"type"`
	LibraryID      string   `json:"library_id"`
	AssetID        string   `json:"asset_id"`
	RelativePath   string   `json:"relative_path,omitempty"`
	Title          string   `json:"title,omitempty"`
	Kind           string   `json:"kind,omitempty"`
	SHA256         string   `json:"sha256"`
	LicenseName    string   `json:"license_name,omitempty"`
	LicenseURL     string   `json:"license_url,omitempty"`
	SourceProvider string   `json:"source_provider,omitempty"`
	SourceURL      string   `json:"source_url,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Collection     string   `json:"collection,omitempty"`
	Actor          string   `json:"actor"`
	CreatedAt      string   `json:"created_at"`
	Deduplicated   bool     `json:"deduplicated,omitempty"`
}

func adoptAssets(in adoptInput) (any, error) {
	library, err := getLibrary(strings.TrimSpace(in.LibraryID))
	if err != nil {
		return nil, err
	}
	if library.AccessMode == "read_only" {
		return nil, fmt.Errorf("资源库为只读模式，不能采纳资源")
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("请选择要采纳的资源")
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "copy"
	}
	if mode != "copy" && mode != "reference" {
		return nil, fmt.Errorf("采纳模式必须是 copy 或 reference")
	}
	if library.BackendType == "smb" && mode == "reference" {
		return nil, fmt.Errorf("SMB 共享库必须复制文件，不能引用其他电脑上的本地路径")
	}
	actor := safeActor(in.Actor)
	db, err := openLibraryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	saved := []map[string]any{}
	failed := []map[string]any{}
	for _, requested := range in.Items {
		item, saveErr := adoptOne(db, library, mode, actor, requested)
		if saveErr != nil {
			failed = append(failed, map[string]any{"title": requested.Title, "error": saveErr.Error()})
			continue
		}
		saved = append(saved, item)
	}
	return map[string]any{"saved": saved, "failed": failed, "total": len(saved)}, nil
}

func adoptOne(db *sql.DB, library *libraryRecord, mode string, actor string, requested adoptItem) (map[string]any, error) {
	sourcePath := strings.TrimSpace(requested.LocalPath)
	title := strings.TrimSpace(requested.Title)
	kind := strings.TrimSpace(requested.Kind)
	tags := normalizeAdoptionTags(requested.Tags)
	collection := normalizeAdoptionCollection(requested.Collection)
	if strings.TrimSpace(requested.CandidateID) != "" {
		var candidatePath, candidateTitle, candidateKind string
		err := db.QueryRow(`SELECT path,name,kind FROM candidates WHERE id=?`, strings.TrimSpace(requested.CandidateID)).Scan(&candidatePath, &candidateTitle, &candidateKind)
		if err != nil {
			return nil, fmt.Errorf("候选资源不存在或索引已失效")
		}
		if sourcePath == "" {
			sourcePath = candidatePath
		}
		if title == "" {
			title = candidateTitle
		}
		if kind == "" {
			kind = candidateKind
		}
	}

	var temporary string
	copyFilename := requested.Filename
	if strings.TrimSpace(requested.DownloadURL) != "" {
		if library.BackendType == "smb" && !requested.RedistributionAllowed {
			return nil, fmt.Errorf("该在线素材未明确允许再分发，不能发布到 SMB 共享库")
		}
		if parsed, parseErr := url.Parse(strings.TrimSpace(requested.DownloadURL)); parseErr == nil {
			if downloadName := safeFilename(path.Base(parsed.Path)); downloadName != "" && filepath.Ext(downloadName) != "" {
				downloadExt := filepath.Ext(downloadName)
				requestedExt := filepath.Ext(copyFilename)
				switch {
				case strings.TrimSpace(copyFilename) == "":
					copyFilename = downloadName
				case requestedExt == "":
					copyFilename += downloadExt
				case !strings.EqualFold(requestedExt, downloadExt):
					copyFilename = strings.TrimSuffix(copyFilename, requestedExt) + downloadExt
				}
			}
		}
		download, err := downloadToTemporary(requested.DownloadURL, requested.Kind)
		if err != nil {
			return nil, err
		}
		temporary = download
		defer os.Remove(temporary)
		sourcePath = temporary
	}
	if sourcePath == "" {
		return nil, fmt.Errorf("缺少本地文件或下载地址")
	}
	info, err := os.Stat(sourcePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("源文件不可用: %s", sourcePath)
	}
	if title == "" {
		title = filepath.Base(sourcePath)
	}
	if kind == "" {
		kind = mediaExtensions[strings.ToLower(filepath.Ext(sourcePath))]
	}
	if kind == "" {
		return nil, fmt.Errorf("不支持的媒体类型: %s", filepath.Ext(sourcePath))
	}
	digest, err := hashFile(sourcePath)
	if err != nil {
		return nil, err
	}

	var existingID, existingRelative, existingLocal, existingKind, existingTitle string
	err = db.QueryRow(`SELECT id,relative_path,local_path,kind,title FROM library_items WHERE library_id=? AND sha256=?`, library.ID, digest).
		Scan(&existingID, &existingRelative, &existingLocal, &existingKind, &existingTitle)
	if err == nil {
		_ = appendCatalogEvent(library, catalogEvent{
			EventID: randomID("event"), Type: "asset.adopt", LibraryID: library.ID,
			AssetID: existingID, RelativePath: existingRelative,
			Title: title, Kind: kind, SHA256: digest, LicenseName: requested.LicenseName,
			LicenseURL: requested.LicenseURL, SourceProvider: requested.Provider,
			SourceURL: requested.SourceURL, Tags: tags, Collection: collection, Actor: actor,
			CreatedAt: nowRFC3339(), Deduplicated: true,
		})
		return map[string]any{
			"id": existingID, "library_id": library.ID, "relative_path": existingRelative,
			"local_path": existingLocal, "kind": existingKind, "title": existingTitle,
			"sha256": digest, "deduplicated": true,
		}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	destination := sourcePath
	relativePath := ""
	if mode == "copy" {
		destination, relativePath, err = copyIntoLibrary(library, sourcePath, digest, firstNonEmpty(copyFilename, requested.Filename, title, filepath.Base(sourcePath)))
		if err != nil {
			return nil, err
		}
	} else {
		absolute, absErr := filepath.Abs(sourcePath)
		if absErr != nil {
			return nil, absErr
		}
		destination = absolute
		relativePath = ""
	}

	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(destination)))
	metadata, _ := json.Marshal(map[string]any{"tags": tags, "collection": collection})
	id := randomID("asset")
	now := nowRFC3339()
	_, err = db.Exec(`INSERT INTO library_items(id,library_id,sha256,relative_path,local_path,kind,title,mime,bytes,author,license_name,license_url,
		redistribution_allowed,source_provider,source_id,source_url,adopted_by,adopted_at,updated_at,metadata_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, library.ID, digest, relativePath, destination, kind, title, mimeType, info.Size(), requested.Author,
		requested.LicenseName, requested.LicenseURL, boolInt(requested.RedistributionAllowed), requested.Provider,
		requested.ProviderID, requested.SourceURL, actor, now, now, string(metadata))
	if err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	if err := replaceSearchDocument(tx, searchDocument{
		EntryType: "library", EntryID: id, LibraryID: library.ID, Kind: kind, UpdatedAt: now,
		Title: title, Filename: filepath.Base(destination), Path: relativePath,
		Tags: strings.Join(tags, " "), Collection: collection, License: requested.LicenseName, Author: requested.Author,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	event := catalogEvent{
		EventID: randomID("event"), Type: "asset.adopt", LibraryID: library.ID, AssetID: id,
		RelativePath: relativePath, Title: title, Kind: kind, SHA256: digest,
		LicenseName: requested.LicenseName, LicenseURL: requested.LicenseURL,
		SourceProvider: requested.Provider, SourceURL: requested.SourceURL,
		Tags: tags, Collection: collection, Actor: actor, CreatedAt: now,
	}
	if err := appendCatalogEvent(library, event); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "library_id": library.ID, "relative_path": relativePath,
		"local_path": destination, "kind": kind, "title": title, "sha256": digest,
		"tags": tags, "collection": collection, "deduplicated": false,
	}, nil
}

func downloadToTemporary(rawURL string, kind string) (string, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return "", fmt.Errorf("下载地址必须是 http 或 https")
	}
	request, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := downloadClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("下载失败，来源返回 HTTP %d", response.StatusCode)
	}
	root, err := pluginDataRoot()
	if err != nil {
		return "", err
	}
	staging := filepath.Join(root, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(staging, "adopt-*"+extensionForKind(kind))
	if err != nil {
		return "", err
	}
	defer temp.Close()
	written, err := io.Copy(temp, io.LimitReader(response.Body, maxDownloadCap+1))
	if err != nil {
		os.Remove(temp.Name())
		return "", err
	}
	if written > maxDownloadCap {
		os.Remove(temp.Name())
		return "", fmt.Errorf("素材超过单文件上限")
	}
	if written < minDownloadSize {
		os.Remove(temp.Name())
		return "", fmt.Errorf("下载内容过小，可能是错误页")
	}
	return temp.Name(), nil
}

func normalizeAdoptionTags(values []string) []string {
	seen := map[string]bool{}
	tags := make([]string, 0, len(values))
	for _, value := range values {
		tag := strings.TrimSpace(value)
		if tag == "" || len([]rune(tag)) > 60 || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
		if len(tags) == 20 {
			break
		}
	}
	return tags
}

func normalizeAdoptionCollection(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	value = strings.Trim(value, "/")
	runes := []rune(value)
	if len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}

func copyIntoLibrary(library *libraryRecord, sourcePath string, digest string, filename string) (string, string, error) {
	safe := safeFilename(filename)
	if safe == "" {
		safe = safeFilename(filepath.Base(sourcePath))
	}
	if safe == "" {
		safe = "asset"
	}
	sourceExt := filepath.Ext(sourcePath)
	if sourceExt != "" {
		requestedExt := filepath.Ext(safe)
		if requestedExt == "" {
			safe += sourceExt
		} else if !strings.EqualFold(requestedExt, sourceExt) {
			safe = strings.TrimSuffix(safe, requestedExt) + sourceExt
		}
	}
	relativeDir := filepath.Join("assets", digest[:2])
	destinationDir, err := resolveWithin(library.RootURI, filepath.Join(library.RootURI, relativeDir))
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return "", "", err
	}
	destination := filepath.Join(destinationDir, safe)
	if existingDigest, err := hashFile(destination); err == nil {
		if existingDigest == digest {
			relative, _ := filepath.Rel(library.RootURI, destination)
			return destination, filepath.ToSlash(relative), nil
		}
		ext := filepath.Ext(safe)
		base := strings.TrimSuffix(safe, ext)
		destination = filepath.Join(destinationDir, base+"-"+digest[:8]+ext)
	}
	temporary := destination + ".adopting"
	if err := copyFile(sourcePath, temporary); err != nil {
		return "", "", err
	}
	if err := os.Rename(temporary, destination); err != nil {
		os.Remove(temporary)
		return "", "", err
	}
	relative, _ := filepath.Rel(library.RootURI, destination)
	return destination, filepath.ToSlash(relative), nil
}

func appendCatalogEvent(library *libraryRecord, event catalogEvent) error {
	metaDir := filepath.Join(library.RootURI, ".himind-media", "catalog", "users")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(metaDir, safeActor(event.Actor)+".jsonl")
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(payload, '\n'))
	return err
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyFile(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func safeActor(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = os.Getenv("USERNAME")
	}
	if value == "" {
		value = "local-user"
	}
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r > 127 {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, "- .")
	if value == "" {
		return "local-user"
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
