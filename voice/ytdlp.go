package voice

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ppalone/ytsearch"

	"github.com/lrstanley/go-ytdlp"
)

func (vs *VoiceSystem) Search(q string) ([]SearchResult, error) {
	vs.cache.RLock()
	if item, ok := vs.cache.items[q]; ok {
		if time.Now().Before(item.expiresAt) {
			vs.cache.RUnlock()
			return item.results, nil
		}
	}
	vs.cache.RUnlock()

	query := q
	ytp := getYoutubePrefix()
	if strings.HasPrefix(strings.ToUpper(q), strings.ToUpper(ytp)) {
		query = strings.TrimSpace(q[len(ytp):])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2600*time.Millisecond)
	defer cancel()
	resMu := sync.Mutex{}
	var yt []SearchResult
	seen := make(map[string]bool)
	wg := sync.WaitGroup{}
	wg.Add(1)
	safeGo(func() {
		defer wg.Done()
		c := ytsearch.NewClient(nil)
		r, _ := c.Search(ctx, query)
		for _, v := range r.Results {
			resMu.Lock()
			if !seen[v.VideoID] {
				seen[v.VideoID] = true
				yt = append(yt, SearchResult{URL: "https://www.youtube.com/watch?v=" + v.VideoID, Title: voiceSys.TruncateWithPreserve(v.Title, 100, "[YT] ", "")})
			}
			resMu.Unlock()
		}
	})
	d := make(chan struct{})
	safeGo(func() {
		wg.Wait()
		close(d)
	})
	select {
	case <-d:
	case <-time.After(2300 * time.Millisecond):
	}
	resMu.Lock()
	defer resMu.Unlock()
	fin := yt
	if len(fin) > 25 {
		fin = fin[:25]
	}

	vs.cache.Lock()
	vs.cache.items[q] = cachedItem{
		results:   fin,
		expiresAt: time.Now().Add(5 * time.Minute),
	}
	vs.cache.Unlock()
	return fin, nil
}

func (vs *VoiceSystem) SearchPlaylist(q string) ([]ytdlpSearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ytRs, ytmRs []ytdlpSearchResult
	var ytErr, ytmErr error
	wg := sync.WaitGroup{}
	wg.Add(2)

	safeGo(func() {
		defer wg.Done()
		ytRs, ytErr = ytdlpSearchPlaylist(ctx, q, 10)
	})
	safeGo(func() {
		defer wg.Done()
		ytmRs, ytmErr = ytdlpSearchPlaylistYTM(ctx, q, 10)
	})
	wg.Wait()

	if ytErr != nil && ytmErr != nil {
		return nil, fmt.Errorf("YouTube: %v, YTM: %v", ytErr, ytmErr)
	}

	var res []ytdlpSearchResult
	seen := make(map[string]bool)
	for _, r := range ytmRs {
		if seen[r.URL] {
			continue
		}
		res = append(res, ytdlpSearchResult{Title: "[PL] " + r.Title, Uploader: r.Uploader, URL: r.URL})
		seen[r.URL] = true
	}
	for _, r := range ytRs {
		if seen[r.URL] {
			continue
		}
		res = append(res, ytdlpSearchResult{Title: "[PL] " + r.Title, Uploader: r.Uploader, URL: r.URL})
		seen[r.URL] = true
	}

	return res, nil
}

func newYtdlp() (*ytdlp.Command, func()) {
	cmd := ytdlp.New().
		Quiet().
		NoWarnings()

	if proxy := os.Getenv("YOUTUBE_PROXY"); proxy != "" {
		cmd.Proxy(proxy)
	}

	return cmd, func() {}
}

func buildYtdlpArgs() []string {
	jsOnce.Do(func() {
		for _, rt := range []string{"node", "deno", "quickjs"} {
			if path, err := exec.LookPath(rt); err == nil {
				cachedJSArgs = append(cachedJSArgs, "--js-runtimes", rt+":"+path)
				break
			}
		}
	})

	args := append([]string(nil), cachedJSArgs...)
	args = append(args,
		"--no-playlist",
		"--no-check-certificates",
		"--no-warnings",
		"--extractor-args", "youtube:player_client=android,web",
		"--prefer-free-formats",
		"--socket-timeout", "15",
		"--retries", "10",
		"--fragment-retries", "10",
		"--no-cache-dir",
	)

	if _, err := os.Stat("cookies.txt"); err == nil {
		args = append(args, "--cookies", "cookies.txt")
	} else if c := os.Getenv("YOUTUBE_COOKIES"); c != "" {
		if _, err := os.Stat(c); err == nil {
			args = append(args, "--cookies", c)
		}
	}

	return args
}

func ytdlpSearch(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		PreferFreeFormats().
		Run(ctx, append(args, "ytsearch"+fmt.Sprintf("%d", m)+":"+q)...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[3] + "s")
		u := ps[0]
		if extractVideoID(u) != "" {
			rs = append(rs, ytdlpSearchResult{u, ps[1], ps[2], d})
		}
	}
	return rs, nil
}

func ytdlpSearchYTM(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, fmt.Sprintf("ytmsearch%d:%s", m, q))...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[3] + "s")
		u := ps[0]
		if extractVideoID(u) != "" {
			rs = append(rs, ytdlpSearchResult{URL: u, Title: ps[1], Uploader: ps[2], Duration: d})
		}
	}
	return rs, nil
}

func ytdlpSearchPlaylist(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	searchURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s&sp=EgIQAw%%253D%%253D", url.QueryEscape(q))

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, searchURL)...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 || ps[1] == "" || ps[1] == "NA" {
			continue
		}
		rs = append(rs, ytdlpSearchResult{URL: ps[0], Title: ps[1], Uploader: ps[2]})
	}
	return rs, nil
}

func ytdlpSearchPlaylistYTM(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	searchURL := fmt.Sprintf("https://music.youtube.com/search?q=%s&filter=playlists", url.QueryEscape(q))

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, searchURL)...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 || ps[1] == "" || ps[1] == "NA" {
			continue
		}
		rs = append(rs, ytdlpSearchResult{URL: ps[0], Title: ps[1], Uploader: ps[2]})
	}
	return rs, nil
}

func ytdlpExtractMetadata(ctx context.Context, u string) (*ytdlpMetadata, error) {
	u = strings.Replace(u, "music.youtube.com", "www.youtube.com", 1)

	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	args = append(args, "-f", "bestaudio[ext=webm]/bestaudio[ext=m4a]/bestaudio/best")
	res, err := cmd.
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s\t%(id)s\t%(filename)s").
		Output(filepath.Join(AudioCacheDir, "%(id)s.%(ext)s")).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, "--skip-download", u)...)

	if err != nil {
		voiceSys.LogInfo("yt-dlp metadata failed: %v, stderr: %s (URL: %s)", err, res.Stderr, u)
		return nil, err
	}

	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 6 {
			continue
		}
		d, _ := time.ParseDuration(ps[3] + "s")
		return &ytdlpMetadata{URL: ps[0], Title: ps[1], Uploader: ps[2], Duration: d, ID: ps[4], Filename: ps[5]}, nil
	}
	return nil, errors.New("failed to parse metadata")
}

func isLikelyMusicStreamingSite(url string) bool {
	lowerURL := strings.ToLower(url)

	musicPathPatterns := []string{
		"/track/", "/tracks/",
		"/album/", "/albums/",
		"/song/", "/songs/",
		"/playlist/", "/playlists/",
		"/artist/", "/artists/",
		"/music/",
	}

	for _, pattern := range musicPathPatterns {
		if strings.Contains(lowerURL, pattern) {
			return true
		}
	}

	musicSubdomains := []string{
		"music.", "play.", "listen.", "stream.",
	}

	for _, subdomain := range musicSubdomains {
		if strings.Contains(lowerURL, "://"+subdomain) || strings.Contains(lowerURL, "://www."+subdomain) {
			return true
		}
	}

	return false
}

func ytdlpStream(ctx context.Context, u string, ss time.Duration, out io.Writer) (*ytdlpMetadata, error) {
	u = strings.Replace(u, "music.youtube.com", "www.youtube.com", 1)

	cmd, cleanup := newYtdlp()
	defer cleanup()

	proxy := os.Getenv("YOUTUBE_PROXY")

	args := buildYtdlpArgs()
	args = append(args, "--ignore-config")
	if ss > 0 {
		args = append(args, "--ss", fmt.Sprintf("%.3f", ss.Seconds()))
	}
	execCmd := cmd.
		Format("bestaudio[ext=webm]/bestaudio[ext=m4a]/bestaudio/best").
		Output("-").
		NoSimulate().
		NoPart().
		NoPlaylist().
		NoCheckCertificates().
		BuildCommand(ctx, append(args, u)...)

	execCmd.Stdout = out
	execCmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	if proxy != "" {
		execCmd.Env = append(execCmd.Env, "http_proxy="+proxy, "https_proxy="+proxy, "all_proxy="+proxy)
	}
	execCmd.WaitDelay = 0

	var stderr bytes.Buffer
	execCmd.Stderr = &stderr

	if err := execCmd.Start(); err != nil {
		return nil, err
	}

	if err := execCmd.Wait(); err != nil {
		msg := strings.ToLower(err.Error() + stderr.String())
		if strings.Contains(msg, "broken pipe") || strings.Contains(msg, "signal: killed") {
			return &ytdlpMetadata{}, nil
		}
		voiceSys.LogInfo("yt-dlp stream failed for %s: %v, stderr: %s", u, err, stderr.String())
		voiceSys.LogInfo("yt-dlp exited with error: %v", err)
		return nil, err
	}

	return &ytdlpMetadata{}, nil
}

func ytdlpResolveMetadata(ctx context.Context, u string) (string, string, string, time.Duration, int64, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := append(buildYtdlpArgs(), "--skip-download")
	res, err := cmd.
		Print("%(title)s\t%(uploader)s\t%(duration)s\t%(id)s\t%(filesize,filesize_approx)s").
		NoSimulate().
		IgnoreConfig().
		NoWarnings().
		Run(ctx, append(args, u)...)

	if err != nil {
		stderr := strings.ToLower(res.Stderr)
		if strings.Contains(stderr, "drm") {
			return "", "", "", 0, 0, fmt.Errorf("DRM: %w", err)
		}
		return "", "", "", 0, 0, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[2] + "s")
		sz := int64(0)
		if len(ps) >= 5 {
			fmt.Sscanf(ps[4], "%d", &sz)
		}
		return ps[0], ps[1], ps[3], d, sz, nil
	}
	return "", "", "", 0, 0, errors.New("failed to resolve metadata")
}

func ytdlpExtractPlaylist(ctx context.Context, u string, m int) ([]ytdlpPlaylistEntry, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	res := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(id)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		BuildCommand(ctx, append(args, u, "--yes-playlist")...)

	var stdout, stderr bytes.Buffer
	res.Stdout = &stdout
	res.Stderr = &stderr

	if err := res.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp playlist failed: %w, stderr: %s", err, stderr.String())
	}

	rawOutput := strings.TrimSpace(stdout.String())
	ls := strings.Split(rawOutput, "\n")

	es := make([]ytdlpPlaylistEntry, 0)
	isYouTube := isYouTubeURL(u) || strings.Contains(u, "music.youtube.com")

	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 {
			continue
		}
		url := ps[0]
		title := ps[1]
		uploader := ps[2]

		if isYouTube && len(ps) >= 4 {
			id := ps[3]
			if id != "" && id != "NA" {
				url = "https://www.youtube.com/watch?v=" + id
			}
		}

		es = append(es, ytdlpPlaylistEntry{URL: url, Title: title, Uploader: uploader})
	}
	return es, nil
}

func extractMetadataFromDRMSite(ctx context.Context, url string) (title, artist string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body := new(strings.Builder)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	linesRead := 0
	for scanner.Scan() && linesRead < 500 {
		body.WriteString(scanner.Text())
		body.WriteString(" ")
		linesRead++
		if strings.Contains(scanner.Text(), "</head>") {
			break
		}
	}

	htmlContent := body.String()

	titleRegex := regexp.MustCompile(`<meta[^>]*property=["']og:title["'][^>]*content=["']([^"']+)["']`)
	if matches := titleRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		title = matches[1]
		if idx := strings.Index(title, " - song and lyrics by"); idx != -1 {
			title = title[:idx]
		}
		if idx := strings.Index(title, " | Spotify"); idx != -1 {
			title = title[:idx]
		}
	}

	descRegex := regexp.MustCompile(`<meta[^>]*property=["']og:description["'][^>]*content=["']([^"']+)["']`)
	if matches := descRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		desc := matches[1]
		if strings.Contains(strings.ToLower(url), "spotify") {
			parts := strings.Split(desc, " · ")
			if len(parts) >= 1 {
				artist = strings.TrimSpace(parts[0])
			}
		}
	}

	if title == "" {
		return "", "", errors.New("could not extract metadata")
	}

	return title, artist, nil
}
