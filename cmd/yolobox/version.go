package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type versionCache struct {
	LatestVersion string    `json:"latest_version"`
	ReleaseURL    string    `json:"release_url,omitempty"`
	ReleaseNotes  []string  `json:"release_notes,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

const versionCheckInterval = 24 * time.Hour
const maxReleaseSummaryItems = 3
const latestReleaseAPIURL = "https://api.github.com/repos/finbarr/yolobox/releases/latest"

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Name    string               `json:"name"`
	Body    string               `json:"body"`
	HTMLURL string               `json:"html_url"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type releaseInfo struct {
	Version string
	URL     string
	Notes   []string
}

func versionCachePath() string {
	configDir, _ := os.UserConfigDir()
	return filepath.Join(configDir, "yolobox", "version-check.json")
}

func checkForUpdates() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		doVersionCheck()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func doVersionCheck() {
	cachePath := versionCachePath()

	var cache versionCache
	if data, err := os.ReadFile(cachePath); err == nil {
		if err := json.Unmarshal(data, &cache); err == nil {
			if versionCacheUsable(cache) {
				showUpdateMessage(releaseInfo{
					Version: cache.LatestVersion,
					URL:     cachedReleaseURL(cache),
					Notes:   cache.ReleaseNotes,
				})
				return
			}
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	release, err := fetchLatestRelease(client)
	if err != nil {
		return
	}
	latestVersion := latestVersionFromRelease(release)
	if latestVersion == "" {
		return
	}

	cache = versionCache{
		LatestVersion: latestVersion,
		ReleaseURL:    releaseURL(release),
		ReleaseNotes:  releaseSummary(release),
		CheckedAt:     time.Now(),
	}
	if data, err := json.Marshal(cache); err == nil {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
			_ = os.WriteFile(cachePath, data, 0644)
		}
	}

	showUpdateMessage(releaseInfo{
		Version: cache.LatestVersion,
		URL:     cache.ReleaseURL,
		Notes:   cache.ReleaseNotes,
	})
}

func versionCacheUsable(cache versionCache) bool {
	return cache.LatestVersion != "" && cache.ReleaseURL != "" && time.Since(cache.CheckedAt) < versionCheckInterval
}

func fetchLatestRelease(client *http.Client) (githubRelease, error) {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(latestReleaseAPIURL)
	if err != nil {
		return githubRelease{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != 200 {
		return githubRelease{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func latestVersionFromRelease(release githubRelease) string {
	return strings.TrimPrefix(release.TagName, "v")
}

func cachedReleaseURL(cache versionCache) string {
	if cache.ReleaseURL != "" {
		return cache.ReleaseURL
	}
	if cache.LatestVersion == "" {
		return ""
	}
	return releaseURLForVersion(cache.LatestVersion)
}

func releaseURL(release githubRelease) string {
	if release.HTMLURL != "" {
		return release.HTMLURL
	}
	if version := latestVersionFromRelease(release); version != "" {
		return releaseURLForVersion(version)
	}
	return ""
}

func releaseURLForVersion(version string) string {
	return fmt.Sprintf("https://github.com/finbarr/yolobox/releases/tag/%s", comparableVersion(version))
}

func releaseSummary(release githubRelease) []string {
	return summarizeReleaseBody(release.Body, maxReleaseSummaryItems)
}

func summarizeReleaseBody(body string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	sections := map[string][]string{}
	var fallback []string
	currentSection := ""

	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		if title, ok := markdownHeadingTitle(line); ok {
			currentSection = normalizeReleaseHeading(title)
			continue
		}

		item, ok := markdownBulletText(line)
		if !ok {
			continue
		}
		item = cleanReleaseNote(item)
		if item == "" {
			continue
		}
		if currentSection != "" {
			sections[currentSection] = append(sections[currentSection], item)
		}
		fallback = append(fallback, item)
	}

	var notes []string
	for _, section := range []string{
		"added",
		"changed",
		"fixed",
		"security",
		"removed",
		"deprecated",
		"whats changed",
	} {
		notes = appendUniqueReleaseNotes(notes, sections[section], limit)
		if len(notes) >= limit {
			return notes[:limit]
		}
	}

	notes = appendUniqueReleaseNotes(notes, fallback, limit)
	if len(notes) > limit {
		return notes[:limit]
	}
	return notes
}

func markdownHeadingTitle(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if title == "" {
		return "", false
	}
	return title, true
}

func normalizeReleaseHeading(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	title = strings.Trim(title, ":")
	title = strings.ReplaceAll(title, "'", "")
	title = strings.ReplaceAll(title, "’", "")
	return title
}

func markdownBulletText(line string) (string, bool) {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return strings.TrimSpace(line[2:]), true
	}
	return "", false
}

func cleanReleaseNote(note string) string {
	note = strings.TrimSpace(note)
	if note == "" || strings.HasPrefix(strings.ToLower(note), "full changelog") {
		return ""
	}
	note = strings.TrimPrefix(note, "[x] ")
	note = strings.TrimPrefix(note, "[ ] ")
	if idx := strings.Index(note, " by @"); idx > 0 {
		note = strings.TrimSpace(note[:idx])
	}
	if idx := strings.Index(note, " in https://github.com/"); idx > 0 {
		note = strings.TrimSpace(note[:idx])
	}
	return strings.TrimSpace(note)
}

func appendUniqueReleaseNotes(dst []string, src []string, limit int) []string {
	for _, note := range src {
		if note == "" || contains(dst, note) {
			continue
		}
		dst = append(dst, note)
		if len(dst) >= limit {
			return dst
		}
	}
	return dst
}

func showUpdateMessage(update releaseInfo) {
	if !isNewerVersion(update.Version, Version) {
		return
	}

	fmt.Fprintf(os.Stderr, "\n%syolobox v%s available%s\n", colorYellow, update.Version, colorReset)
	printReleaseNotes(os.Stderr, update.Notes)
	if update.URL != "" {
		fmt.Fprintf(os.Stderr, "   Release: %s\n", update.URL)
	}
	fmt.Fprintf(os.Stderr, "   Run %syolobox upgrade%s to update\n\n", colorBold, colorReset)
}

func printReleaseNotes(w io.Writer, notes []string) {
	if len(notes) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "   What's new:")
	for _, note := range notes {
		_, _ = fmt.Fprintf(w, "   - %s\n", note)
	}
}

func comparableVersion(version string) string {
	match := versionPattern.FindString(strings.TrimSpace(version))
	if match == "" {
		return ""
	}
	if strings.HasPrefix(match, "v") {
		return match
	}
	return "v" + match
}

func isNewerVersion(latestVersion, currentVersion string) bool {
	latest := comparableVersion(latestVersion)
	if latest == "" {
		return false
	}
	current := comparableVersion(currentVersion)
	if current == "" {
		return true
	}
	return compareSemver(latest, current) > 0
}

func compareSemver(a, b string) int {
	parse := func(version string) [3]int {
		version = strings.TrimPrefix(version, "v")
		var parts [3]int
		for i, part := range strings.SplitN(version, ".", 3) {
			parts[i], _ = strconv.Atoi(part)
		}
		return parts
	}

	av := parse(a)
	bv := parse(b)
	for i := 0; i < len(av); i++ {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}

func printVersion() {
	fmt.Printf("%syolobox%s %s%s%s (%s/%s)\n", colorBold, colorReset, colorCyan, Version, colorReset, runtime.GOOS, runtime.GOARCH)
}
