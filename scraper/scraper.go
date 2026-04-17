package scraper

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// School represents a row from the Schools table.
type School struct {
	ID     int
	NPSN   string
	SN     string
	BAPPID string
}

// ScrapeResult holds the result for a single school.
type ScrapeResult struct {
	NPSN     string `json:"npsn"`
	BAPPID   string `json:"bapp_id"`
	Status   string `json:"status"`
	ImgCount int    `json:"images_downloaded"`
	Error    string `json:"error,omitempty"`
}

// RunScraper is the main entry point for the scraping process.
// It reads pending schools from the DB, scrapes images from DAC, and updates status.
func RunScraper(db *sql.DB, s3Client *s3.Client, bucketName string) ([]ScrapeResult, error) {
	// 1. Fetch pending schools
	schools, err := getPendingSchools(db)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending schools: %w", err)
	}

	if len(schools) == 0 {
		log.Println("[Scraper] No pending schools found.")
		return nil, nil
	}

	log.Printf("[Scraper] Found %d pending schools to process.", len(schools))

	// 2. Login to DAC
	session, err := Login()
	if err != nil {
		return nil, fmt.Errorf("DAC login failed: %w", err)
	}

	log.Println("[Scraper] DAC login successful.")

	// 3. Process each school
	var results []ScrapeResult

	for i, school := range schools {
		log.Printf("[Scraper] Processing %d/%d: NPSN=%s SN=%s BAPP_ID=%s",
			i+1, len(schools), school.NPSN, school.SN, school.BAPPID)

		result := processSchool(db, session, school, s3Client, bucketName)
		results = append(results, result)
	}

	// Summary
	successCount := 0
	for _, r := range results {
		if r.Status == "DONE" {
			successCount++
		}
	}
	log.Printf("[Scraper] Completed: %d/%d schools processed successfully.", successCount, len(results))

	return results, nil
}

// getPendingSchools fetches all schools from the DB that have not been processed yet.
func getPendingSchools(db *sql.DB) ([]School, error) {
	rows, err := db.Query("SELECT Id, NPSN, SN, BAPP_ID FROM Schools WHERE STATUS IS NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schools []School
	for rows.Next() {
		var s School
		if err := rows.Scan(&s.ID, &s.NPSN, &s.SN, &s.BAPPID); err != nil {
			return nil, err
		}
		schools = append(schools, s)
	}
	return schools, rows.Err()
}

// processSchool handles the full scraping flow for a single school.
func processSchool(db *sql.DB, session string, school School, s3Client *s3.Client, bucketName string) ScrapeResult {
	result := ScrapeResult{
		NPSN:   school.NPSN,
		BAPPID: school.BAPPID,
	}

	// Step 1: Search datatable to find extractedId
	extractedID, err := SearchDatatable(session, school.NPSN, school.SN)
	if err != nil {
		result.Status = "FAILED"
		result.Error = fmt.Sprintf("search failed: %v", err)
		log.Printf("[Scraper] ✗ NPSN=%s: %s", school.NPSN, result.Error)
		return result
	}

	// Step 2: Get detail HTML and extract image URLs
	images, err := GetDetailImages(session, extractedID)
	if err != nil {
		result.Status = "FAILED"
		result.Error = fmt.Sprintf("detail fetch failed: %v", err)
		log.Printf("[Scraper] ✗ NPSN=%s: %s", school.NPSN, result.Error)
		return result
	}

	if len(images) == 0 {
		result.Status = "FAILED"
		result.Error = "no images found on detail page"
		log.Printf("[Scraper] ✗ NPSN=%s: %s", school.NPSN, result.Error)
		return result
	}

	// Step 3: Download images to local folder
	folderName := sanitizeFolderName(school.NPSN + "_" + school.BAPPID)
	downloaded, err := DownloadImages(images, folderName, session, s3Client, bucketName)
	if err != nil {
		result.Status = "FAILED"
		result.Error = fmt.Sprintf("download failed: %v", err)
		log.Printf("[Scraper] ✗ NPSN=%s: %s", school.NPSN, result.Error)
		return result
	}

	result.ImgCount = downloaded

	// Step 4: Update database status to DONE
	_, err = db.Exec("UPDATE Schools SET STATUS = 'DONE' WHERE Id = ?", school.ID)
	if err != nil {
		result.Status = "FAILED"
		result.Error = fmt.Sprintf("DB update failed: %v", err)
		log.Printf("[Scraper] ✗ NPSN=%s: %s", school.NPSN, result.Error)
		return result
	}

	result.Status = "DONE"
	log.Printf("[Scraper] ✓ NPSN=%s: %d images downloaded, status updated to DONE", school.NPSN, downloaded)
	return result
}

// sanitizeFolderName cleans up folder name for filesystem use.
func sanitizeFolderName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		" ", "_",
	)
	return replacer.Replace(name)
}
