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

// SearchDatatable searches the DAC datatable for a school by NPSN and SN,
// returning the extractedId (data-id) for the matched row.
func SearchDatatable(session, npsn, sn string) (string, error) {
	baseURL := os.Getenv("DAC_BASE_URL")
	primaryURL := baseURL + "/app/approval/datatable"
	fallbackURL := baseURL + "/app/approval/filter_table"

	form := url.Values{}
	form.Set("draw", "1")
	form.Set("status", "all")
	form.Set("npsn", npsn)
	form.Set("termin", "all")
	form.Set("sn", sn)
	form.Set("start", "0")
	form.Set("length", "10")

	// Try primary URL first
	extractedID, err := trySearch(primaryURL, session, form, npsn)
	if err == nil && extractedID != "" {
		return extractedID, nil
	}

	// If primary returned empty, try fallback URL
	log.Printf("[Search] Primary URL returned no result for NPSN %s, trying fallback...", npsn)
	extractedID, err = trySearch(fallbackURL, session, form, npsn)
	if err == nil && extractedID != "" {
		return extractedID, nil
	}

	// Fallback: Try with just NPSN code (no SN)
	log.Printf("[Search] Trying with just NPSN code: %s", npsn)
	form.Set("sn", "")
	extractedID, err = trySearch(primaryURL, session, form, npsn)
	if err == nil && extractedID != "" {
		return extractedID, nil
	}

	return "", fmt.Errorf("could not find item for NPSN=%s SN=%s", npsn, sn)
}

func trySearch(targetURL, session string, form url.Values, npsn string) (string, error) {
	req, err := http.NewRequest("POST", targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", "ci_session="+session)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", fmt.Errorf("404 Not Found")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Data [][]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse datatable response: %w", err)
	}

	if len(result.Data) == 0 {
		return "", fmt.Errorf("no data rows found")
	}

	// Find the row matching the NPSN and extract data-id
	dataIDRegex := regexp.MustCompile(`data-id=['"]([^'"]+)['"]`)

	for _, row := range result.Data {
		// Check if NPSN matches (index 2 in the row)
		if len(row) > 2 {
			rowNpsn := fmt.Sprintf("%v", row[2])
			if strings.TrimSpace(rowNpsn) != strings.TrimSpace(npsn) {
				continue
			}
		}

		// Search for data-id in any cell
		for _, cell := range row {
			cellStr := fmt.Sprintf("%v", cell)
			if matches := dataIDRegex.FindStringSubmatch(cellStr); len(matches) > 1 {
				log.Printf("[Search] Found extractedId=%s for NPSN=%s", matches[1], npsn)
				return matches[1], nil
			}
		}
	}

	// If no NPSN match, try first row as fallback
	for _, cell := range result.Data[0] {
		cellStr := fmt.Sprintf("%v", cell)
		if matches := dataIDRegex.FindStringSubmatch(cellStr); len(matches) > 1 {
			log.Printf("[Search] Using first row extractedId=%s for NPSN=%s (fallback)", matches[1], npsn)
			return matches[1], nil
		}
	}

	return "", fmt.Errorf("no data-id found in response rows")
}
