package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	igAPIBase   = "https://graph.instagram.com"
	mediaFields = "id,media_type,media_url,thumbnail_url,caption,timestamp,permalink"
)

// APIMedia represents a single media item from the Instagram API.
type APIMedia struct {
	ID           string `json:"id"`
	MediaType    string `json:"media_type"`
	MediaURL     string `json:"media_url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Caption      string `json:"caption,omitempty"`
	Timestamp    string `json:"timestamp"`
	Permalink    string `json:"permalink"`
}

// APIResponse is the top-level response from the /me/media endpoint.
type APIResponse struct {
	Data []APIMedia `json:"data"`
}

// Post is the metadata written to instagram.json for each downloaded image.
type Post struct {
	Index     int    `json:"index"`
	Filename  string `json:"filename"`
	Caption   string `json:"caption"`
	Permalink string `json:"permalink"`
	Timestamp string `json:"timestamp"`
	MediaType string `json:"mediaType"`
}

// TokenRefreshResponse is the response from the token refresh endpoint.
type TokenRefreshResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("[ig-scraper] ")

	token := flag.String("token", os.Getenv("INSTAGRAM_ACCESS_TOKEN"),
		"Instagram long-lived access token (or set INSTAGRAM_ACCESS_TOKEN env var)")
	outputDir := flag.String("output", ".", "Path to the Astro project root")
	count := flag.Int("count", 9, "Number of posts to fetch (1-9)")
	tokenOut := flag.String("token-out", "", "Write the refreshed token to this file")
	noRefresh := flag.Bool("no-refresh", false, "Skip the token refresh step")
	flag.Parse()

	if *token == "" {
		log.Fatal("Error: Instagram access token is required.\n" +
			"  Use --token=<token> or set the INSTAGRAM_ACCESS_TOKEN environment variable.\n" +
			"  See ig-scraper/README.md for setup instructions.")
	}

	currentToken := *token

	// --- Token refresh ---
	if !*noRefresh {
		refreshed, err := refreshToken(currentToken)
		if err != nil {
			log.Printf("Warning: token refresh failed (%v) -- continuing with existing token.", err)
		} else {
			currentToken = refreshed
			log.Println("Access token refreshed successfully.")
		}
	}

	// Write (possibly refreshed) token to file so GitHub Actions can rotate the secret.
	if *tokenOut != "" {
		if err := os.WriteFile(*tokenOut, []byte(currentToken), 0o600); err != nil {
			log.Printf("Warning: could not write token to %s: %v", *tokenOut, err)
		} else {
			log.Printf("Token written to %s", *tokenOut)
		}
	}

	// --- Ensure output directories exist ---
	imgDir := filepath.Join(*outputDir, "src", "images", "instagram")
	dataDir := filepath.Join(*outputDir, "src", "data")
	for _, dir := range []string{imgDir, dataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("Failed to create directory %s: %v", dir, err)
		}
	}

	// --- Fetch media list ---
	log.Printf("Fetching latest %d posts from Instagram...", *count)
	media, err := fetchMedia(currentToken, *count)
	if err != nil {
		log.Fatalf("Failed to fetch media: %v", err)
	}
	log.Printf("Received %d media item(s).", len(media))

	// --- Download images and build metadata ---
	var posts []Post
	for i, item := range media {
		n := i + 1
		filename := fmt.Sprintf("%d.jpg", n)
		destPath := filepath.Join(imgDir, filename)

		imgURL := resolveImageURL(item)
		if imgURL == "" {
			log.Printf("Skipping item %d -- no suitable image URL (type: %s)", n, item.MediaType)
			continue
		}

		log.Printf("  [%d/%d] Downloading %s (%s)...", n, len(media), filename, item.MediaType)
		if err := downloadFile(imgURL, destPath); err != nil {
			log.Printf("  Warning: could not download item %d: %v", n, err)
			continue
		}

		posts = append(posts, Post{
			Index:     n,
			Filename:  filename,
			Caption:   item.Caption,
			Permalink: item.Permalink,
			Timestamp: item.Timestamp,
			MediaType: item.MediaType,
		})
	}

	// --- Write metadata JSON ---
	jsonPath := filepath.Join(dataDir, "instagram.json")
	if err := writeJSON(jsonPath, posts); err != nil {
		log.Fatalf("Failed to write metadata to %s: %v", jsonPath, err)
	}

	log.Printf("Done. %d post(s) saved. Metadata: %s", len(posts), jsonPath)
}

// resolveImageURL returns the best image URL for a media item.
// Videos use their thumbnail; images and carousels use media_url directly.
func resolveImageURL(item APIMedia) string {
	if item.MediaType == "VIDEO" {
		return item.ThumbnailURL
	}
	return item.MediaURL
}

// refreshToken calls the Instagram token-refresh endpoint and returns the new token.
func refreshToken(token string) (string, error) {
	url := fmt.Sprintf("%s/refresh_access_token?grant_type=ig_refresh_token&access_token=%s",
		igAPIBase, token)

	resp, err := httpGet(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var r TokenRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if r.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response")
	}
	return r.AccessToken, nil
}

// fetchMedia retrieves the latest `count` media items from the Instagram API.
func fetchMedia(token string, count int) ([]APIMedia, error) {
	url := fmt.Sprintf("%s/me/media?fields=%s&limit=%d&access_token=%s",
		igAPIBase, mediaFields, count, token)

	resp, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return result.Data, nil
}

// downloadFile downloads a URL and saves it to dest, overwriting any existing file.
func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// httpGet performs a GET with a short timeout (used for API calls, not downloads).
func httpGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	return client.Get(url)
}

// writeJSON marshals v to a pretty-printed JSON file at path.
func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
