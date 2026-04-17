package scraper

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DownloadImages downloads all images directly to S3.
// folderName should be in the format NPSN_BAPP_ID.
// Returns the number of successfully downloaded images.
func DownloadImages(images []ImageInfo, folderName, session string, s3Client *s3.Client, bucketName string) (int, error) {
	downloaded := 0

	for i, img := range images {
		filename := sanitizeFilename(img.Title)
		if filename == "" {
			filename = fmt.Sprintf("image_%d", i+1)
		}

		// Determine extension from URL
		ext := getExtensionFromURL(img.URL)
		if ext == "" {
			ext = ".jpg" // default
		}

		// Avoid duplicate extensions
		if !strings.HasSuffix(strings.ToLower(filename), strings.ToLower(ext)) {
			filename += ext
		}

		key := fmt.Sprintf("DAC/%s/%s", folderName, filename)

		if err := downloadFile(img.URL, key, session, s3Client, bucketName); err != nil {
			log.Printf("[Download] Failed to upload %s to S3: %v", img.URL, err)
			continue
		}

		downloaded++
		log.Printf("[Download] (%d/%d) Uploaded to S3: %s", downloaded, len(images), key)
	}

	return downloaded, nil
}

// downloadFile streams a single file from URL directly to S3.
func downloadFile(url, key, session string, s3Client *s3.Client, bucketName string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Cookie", "ci_session="+session)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d for %s", resp.StatusCode, url)
	}

	_, err = s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   resp.Body,
	})

	if err != nil {
		return fmt.Errorf("failed to upload file %s to S3: %w", key, err)
	}

	return nil
}

// sanitizeFilename removes/replaces characters not allowed in filenames.
func sanitizeFilename(name string) string {
	// Remove HTML tags if any
	htmlTagRegex := regexp.MustCompile(`<[^>]*>`)
	name = htmlTagRegex.ReplaceAllString(name, "")

	// Replace special characters
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	name = replacer.Replace(name)

	// Collapse multiple underscores
	multiUnderscore := regexp.MustCompile(`_+`)
	name = multiUnderscore.ReplaceAllString(name, "_")

	return strings.Trim(name, "_")
}

// getExtensionFromURL extracts the file extension from a URL.
func getExtensionFromURL(url string) string {
	// Remove query string
	if idx := strings.Index(url, "?"); idx != -1 {
		url = url[:idx]
	}

	ext := filepath.Ext(url)
	if ext != "" {
		return ext
	}
	return ""
}
