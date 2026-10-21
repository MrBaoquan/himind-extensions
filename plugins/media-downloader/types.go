package main

type RuntimeStatus struct {
	State             string   `json:"state"`
	Source            string   `json:"source,omitempty"`
	Path              string   `json:"path,omitempty"`
	Version           string   `json:"version,omitempty"`
	ExpectedVersion   string   `json:"expected_version"`
	FFmpegPath        string   `json:"ffmpeg_path,omitempty"`
	FFmpegVersion     string   `json:"ffmpeg_version,omitempty"`
	DataRoot          string   `json:"data_root,omitempty"`
	DataRootWritable  bool     `json:"data_root_writable"`
	DownloadsDir      string   `json:"downloads_dir,omitempty"`
	DownloadsWritable bool     `json:"downloads_writable"`
	Capabilities      []string `json:"capabilities"`
	Message           string   `json:"message,omitempty"`
}

type RuntimeInstallResult struct {
	State     string `json:"state"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256,omitempty"`
	Installed bool   `json:"installed"`
	Mode      string `json:"mode,omitempty"`
	Message   string `json:"message"`
}

type InfoInput struct {
	URL      string `json:"url"`
	Playlist bool   `json:"playlist,omitempty"`
	MaxItems int    `json:"max_items,omitempty"`
}

type FormatInfo struct {
	FormatID       string `json:"format_id"`
	Ext            string `json:"ext,omitempty"`
	Resolution     string `json:"resolution,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	VideoCodec     string `json:"vcodec,omitempty"`
	AudioCodec     string `json:"acodec,omitempty"`
	Filesize       int64  `json:"filesize,omitempty"`
	FilesizeApprox int64  `json:"filesize_approx,omitempty"`
	Note           string `json:"note,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
}

type PlaylistEntry struct {
	ID       string  `json:"id,omitempty"`
	Title    string  `json:"title,omitempty"`
	URL      string  `json:"url,omitempty"`
	Uploader string  `json:"uploader,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

type MediaInfo struct {
	ID              string          `json:"id,omitempty"`
	Title           string          `json:"title,omitempty"`
	Uploader        string          `json:"uploader,omitempty"`
	Extractor       string          `json:"extractor,omitempty"`
	WebpageURL      string          `json:"webpage_url,omitempty"`
	DurationSeconds float64         `json:"duration_seconds,omitempty"`
	Thumbnail       string          `json:"thumbnail,omitempty"`
	IsPlaylist      bool            `json:"is_playlist"`
	EntryCount      int             `json:"entry_count"`
	Entries         []PlaylistEntry `json:"entries,omitempty"`
	Formats         []FormatInfo    `json:"formats,omitempty"`
	Subtitles       []string        `json:"subtitles,omitempty"`
}

type JobStartInput struct {
	WorkspaceRoot   string   `json:"workspace_root"`
	URL             string   `json:"url"`
	DestDir         string   `json:"dest_dir,omitempty"`
	FormatID        string   `json:"format_id,omitempty"`
	Quality         string   `json:"quality,omitempty"`
	AudioOnly       bool     `json:"audio_only,omitempty"`
	Subtitles       []string `json:"subtitles,omitempty"`
	Playlist        bool     `json:"playlist,omitempty"`
	MaxItems        int      `json:"max_items,omitempty"`
	MaxBytes        int64    `json:"max_bytes,omitempty"`
	Overwrite       bool     `json:"overwrite,omitempty"`
	Title           string   `json:"title,omitempty"`
	Thumbnail       string   `json:"thumbnail,omitempty"`
	Extractor       string   `json:"extractor,omitempty"`
	DurationSeconds float64  `json:"duration_seconds,omitempty"`
}

type JobStatusInput struct {
	JobID string `json:"job_id"`
}

type JobLogInput struct {
	JobID    string `json:"job_id"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type JobCleanupInput struct {
	Statuses      []string `json:"statuses,omitempty"`
	OlderThanDays int      `json:"older_than_days,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
}

type JobCleanupResult struct {
	Matched   int  `json:"matched"`
	Removed   int  `json:"removed"`
	Failed    int  `json:"failed"`
	Remaining int  `json:"remaining"`
	DryRun    bool `json:"dry_run"`
}

type JobLogResult struct {
	JobID     string `json:"job_id"`
	Path      string `json:"path"`
	Tail      string `json:"tail"`
	Truncated bool   `json:"truncated"`
	Size      int64  `json:"size"`
}

type JobListInput struct {
	Limit    int      `json:"limit,omitempty"`
	Offset   int      `json:"offset,omitempty"`
	Query    string   `json:"query,omitempty"`
	Statuses []string `json:"statuses,omitempty"`
	Since    string   `json:"since,omitempty"`
	Until    string   `json:"until,omitempty"`
	Sort     string   `json:"sort,omitempty"`
}

type JobSummary struct {
	Total           int   `json:"total"`
	Queued          int   `json:"queued"`
	Running         int   `json:"running"`
	Canceling       int   `json:"canceling"`
	Completed       int   `json:"completed"`
	Failed          int   `json:"failed"`
	Canceled        int   `json:"canceled"`
	Active          int   `json:"active"`
	DownloadedBytes int64 `json:"downloaded_bytes"`
	OutputBytes     int64 `json:"output_bytes"`
}

type JobListResult struct {
	Items   []JobRecord `json:"items"`
	Total   int         `json:"total"`
	Count   int         `json:"count"`
	Offset  int         `json:"offset"`
	Limit   int         `json:"limit"`
	Summary JobSummary  `json:"summary"`
}

type JobRecord struct {
	ID              string   `json:"job_id"`
	Status          string   `json:"status"`
	URL             string   `json:"url"`
	Title           string   `json:"title,omitempty"`
	Thumbnail       string   `json:"thumbnail,omitempty"`
	Extractor       string   `json:"extractor,omitempty"`
	DurationSeconds float64  `json:"duration_seconds,omitempty"`
	WorkspaceRoot   string   `json:"workspace_root"`
	DestDir         string   `json:"dest_dir"`
	FormatID        string   `json:"format_id,omitempty"`
	Quality         string   `json:"quality,omitempty"`
	AudioOnly       bool     `json:"audio_only,omitempty"`
	Subtitles       []string `json:"subtitles,omitempty"`
	Playlist        bool     `json:"playlist,omitempty"`
	MaxItems        int      `json:"max_items,omitempty"`
	MaxBytes        int64    `json:"max_bytes,omitempty"`
	Overwrite       bool     `json:"overwrite,omitempty"`
	PID             int      `json:"pid,omitempty"`
	CreatedAt       string   `json:"created_at"`
	StartedAt       string   `json:"started_at,omitempty"`
	FinishedAt      string   `json:"finished_at,omitempty"`
	Progress        float64  `json:"progress"`
	DownloadedBytes int64    `json:"downloaded_bytes,omitempty"`
	TotalBytes      int64    `json:"total_bytes,omitempty"`
	OutputBytes     int64    `json:"output_bytes,omitempty"`
	Speed           string   `json:"speed,omitempty"`
	ETA             string   `json:"eta,omitempty"`
	Outputs         []string `json:"outputs,omitempty"`
	Error           string   `json:"error,omitempty"`
	YtdlpVersion    string   `json:"ytdlp_version,omitempty"`
	FFmpegPath      string   `json:"ffmpeg_path,omitempty"`
	LogPath         string   `json:"log_path,omitempty"`
}
