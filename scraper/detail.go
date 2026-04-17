package scraper

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// ImageInfo holds the URL and title of a scraped image.
type ImageInfo struct {
	URL   string
	Title string
}

// GetDetailImages fetches the detail page HTML for the given ID and extracts image URLs.
func GetDetailImages(session, extractedID string) ([]ImageInfo, error) {
	baseURL := os.Getenv("DAC_BASE_URL")
	targetURL := baseURL + "/app/approval/detail"

	form := url.Values{}
	form.Set("id", extractedID)

	req, err := http.NewRequest("POST", targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create detail request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", "ci_session="+session)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("detail request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read detail response: %w", err)
	}

	// Parse JSON response - the response contains { "html": "..." } or { "status": "success", "html": "..." }
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse detail JSON: %w", err)
	}

	htmlContent, ok := result["html"].(string)
	if !ok || htmlContent == "" {
		return nil, fmt.Errorf("no HTML content in detail response for ID=%s", extractedID)
	}

	// Extract images from HTML
	images := parseImagesFromHTML(htmlContent, baseURL)
	log.Printf("[Detail] Found %d images for ID=%s", len(images), extractedID)

	return images, nil
}

// parseImagesFromHTML extracts image URLs and titles from the detail HTML.
// Images are inside .col-6 containers with .card-header for title and <img> for source.
func parseImagesFromHTML(html, baseURL string) []ImageInfo {
	var images []ImageInfo

	// Pattern to find col-6 blocks containing images
	// We look for card-header text followed by img src
	// Example structure:
	//   <div class="col-6">
	//     <div class="card-header">FOTO SEKOLAH</div>
	//     <img src="/uploads/foto.jpg" ...>
	//   </div>

	// Strategy: find all <img> tags with src attributes within the HTML
	imgRegex := regexp.MustCompile(`(?s)<div[^>]*class="[^"]*col-6[^"]*"[^>]*>.*?(?:<div[^>]*class="[^"]*card-header[^"]*"[^>]*>(.*?)</div>)?.*?<img[^>]+src="([^"]+)"[^>]*/?>.*?</div>`)

	matches := imgRegex.FindAllStringSubmatch(html, -1)

	if len(matches) > 0 {
		for i, m := range matches {
			title := strings.TrimSpace(m[1])
			if title == "" {
				title = fmt.Sprintf("image_%d", i+1)
			}
			imgURL := resolveURL(m[2], baseURL)
			images = append(images, ImageInfo{URL: imgURL, Title: title})
		}
		return images
	}

	// Fallback: simpler regex to find all img tags with src
	simpleImgRegex := regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	// Title regex to pair with images - look for card-header before each img
	titleRegex := regexp.MustCompile(`(?s)card-header[^>]*>(.*?)</div>`)

	titles := titleRegex.FindAllStringSubmatch(html, -1)
	imgMatches := simpleImgRegex.FindAllStringSubmatch(html, -1)

	for i, m := range imgMatches {
		imgURL := m[1]

		// Skip tiny/icon images and data URIs
		if strings.HasPrefix(imgURL, "data:") || strings.Contains(imgURL, "favicon") {
			continue
		}

		title := fmt.Sprintf("image_%d", i+1)
		if i < len(titles) {
			t := strings.TrimSpace(titles[i][1])
			if t != "" {
				title = t
			}
		}

		imgURL = resolveURL(imgURL, baseURL)
		images = append(images, ImageInfo{URL: imgURL, Title: title})
	}

	return images
}

// resolveURL converts a relative URL to absolute using the base URL.
func resolveURL(imgURL, baseURL string) string {
	if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
		return imgURL
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(imgURL, "/")
}
