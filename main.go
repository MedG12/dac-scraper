package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"go-api-s3/docs"
	"go-api-s3/scraper"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"
	"github.com/xuri/excelize/v2"
)

var s3Client *s3.Client
var bucketName string
var db *sql.DB

func initDB() {
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASS")
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", dbUser, dbPass, dbHost, dbPort, dbName)
	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Failed to open database connection: %v", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	log.Println("Database connection established")
}

func enableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
	(*w).Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS, POST")
	(*w).Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

type MemoryLogger struct {
	sync.Mutex
	logs []string
}

func (m *MemoryLogger) Write(p []byte) (n int, err error) {
	m.Lock()
	defer m.Unlock()
	m.logs = append(m.logs, string(p))
	if len(m.logs) > 500 {
		m.logs = m.logs[len(m.logs)-500:]
	}
	return len(p), nil
}

var memLogger = &MemoryLogger{}

func initAWS() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Environment file not found. Using system environment variables.")
	}

	bucketName = os.Getenv("AWS_BUCKET_NAME")
	if bucketName == "" {
		log.Fatalf("AWS_BUCKET_NAME environment variable is not set")
	}

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatalf("Failed to load AWS configuration: %v", err)
	}

	customEndpoint := os.Getenv("AWS_ENDPOINT_URL")

	if customEndpoint != "" {
		s3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(customEndpoint)
			o.UsePathStyle = true
		})
	} else {
		s3Client = s3.NewFromConfig(cfg)
	}
}

func testS3Connection() {
	output, err := s3Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int32(1),
	})

	if err != nil {
		log.Fatalf("CRITICAL ERROR: Failed to connect to S3 bucket. Details: %v", err)
	}

	if len(output.Contents) == 0 {
		log.Fatalf("CRITICAL ERROR: Connected to S3, but the bucket is completely empty.")
	}

	log.Println("S3 connection test passed successfully. Files detected in bucket.")
}

// statusHandler
// @Summary Health check
// @Description Returns service health and basic status information.
// @Tags health
// @Produce json
// @Success 200 {object} map[string]string
// @Router / [get]
func statusHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "OK",
		"message": "Service is running optimally.",
	})
}

// sendHandler
// @Summary Upload file to S3
// @Description Uploads a single file to the configured S3 bucket under the provided folder.
// @Tags files
// @Accept mpfd
// @Produce json
// @Param folder formData string true "Target folder inside the bucket"
// @Param file formData file true "File to upload"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Bad Request"
// @Failure 405 {string} string "Method Not Allowed"
// @Failure 500 {string} string "Internal Server Error"
// @Router /send [post]
func sendHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Failed to parse multipart form.", http.StatusBadRequest)
		return
	}

	folder := r.FormValue("folder")
	if folder == "" {
		http.Error(w, "Folder parameter is required.", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	key := path.Join(folder, handler.Filename)

	_, err = s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   file,
	})

	if err != nil {
		http.Error(w, "Internal server error during upload.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "File uploaded successfully.",
		"path":    key,
	})
}

// getHandler
// @Summary Download file from S3
// @Description Downloads a file from the configured S3 bucket using folder and file query parameters.
// @Tags files
// @Produce octet-stream
// @Param folder query string true "Folder inside the bucket"
// @Param file query string true "File name to download"
// @Success 200 {file} binary
// @Failure 400 {string} string "Bad Request"
// @Failure 404 {string} string "Not Found"
// @Failure 405 {string} string "Method Not Allowed"
// @Failure 500 {string} string "Internal Server Error"
// @Router /get [get]

func getHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}

	folder := r.URL.Query().Get("folder")
	fileName := r.URL.Query().Get("file")

	if folder == "" || fileName == "" {
		http.Error(w, "Folder and file parameters are required.", http.StatusBadRequest)
		return
	}

	key := path.Join(folder, fileName)

	result, err := s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})

	if err != nil {
		http.Error(w, "Requested file not found.", http.StatusNotFound)
		return
	}
	defer result.Body.Close()

	w.Header().Set("Content-Disposition", "inline; filename="+fileName)
	w.Header().Set("Content-Type", "application/pdf")

	_, err = io.Copy(w, result.Body)
	if err != nil {
		http.Error(w, "Internal server error during file download.", http.StatusInternalServerError)
		return
	}
}

// deleteHandler
// @Summary Delete file from S3
// @Description Deletes a file from the configured S3 bucket using folder and file query parameters.
// @Tags files
// @Produce json
// @Param folder query string true "Folder inside the bucket"
// @Param file query string true "File name to delete"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Bad Request"
// @Failure 404 {string} string "Not Found"
// @Failure 405 {string} string "Method Not Allowed"
// @Failure 500 {string} string "Internal Server Error"
// @Router /delete [delete]
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}

	folder := r.URL.Query().Get("folder")
	fileName := r.URL.Query().Get("file")

	if folder == "" || fileName == "" {
		http.Error(w, "Folder and file parameters are required.", http.StatusBadRequest)
		return
	}

	key := path.Join(folder, fileName)

	_, err := s3Client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})

	if err != nil {
		http.Error(w, "Internal server error during file deletion.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "File deleted successfully.",
		"path":    key,
	})
}

// downloadHandler
// @Summary Upload Excel File for DAC
// @Description Receives an Excel file, extracts 'BAPP ID', 'NPSN', and 'Serial Number' and returns them.
// @Tags dac
// @Accept mpfd
// @Produce json
// @Param file formData file true "Excel file to process (.xlsx)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Bad Request"
// @Failure 405 {string} string "Method Not Allowed"
// @Failure 500 {string} string "Internal Server Error"
// @Router /download [post]
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Failed to parse multipart form.", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		http.Error(w, "Failed to parse Excel file.", http.StatusBadRequest)
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		http.Error(w, "Excel file has no sheets.", http.StatusBadRequest)
		return
	}
	firstSheet := sheets[0]

	rows, err := f.GetRows(firstSheet)
	if err != nil {
		http.Error(w, "Failed to read rows from Excel.", http.StatusInternalServerError)
		return
	}

	if len(rows) < 1 {
		http.Error(w, "Excel file is empty.", http.StatusBadRequest)
		return
	}

	header := rows[0]
	bappIdx, npsnIdx, snIdx := -1, -1, -1

	for i, col := range header {
		cleanCol := strings.TrimSpace(col)
		switch {
		case strings.EqualFold(cleanCol, "BAPP ID"):
			bappIdx = i
		case cleanCol == "NPSN":
			npsnIdx = i
		case strings.EqualFold(cleanCol, "Serial Number") || cleanCol == "SN":
			snIdx = i
		}
	}

	if bappIdx == -1 || npsnIdx == -1 || snIdx == -1 {
		http.Error(w, "Missing required columns: BAPP ID, NPSN, or Serial Number.", http.StatusBadRequest)
		return
	}

	var results []map[string]string

	for i := 1; i < len(rows); i++ {
		row := rows[i]

		getCol := func(idx int) string {
			if idx < len(row) {
				return row[idx]
			}
			return ""
		}

		bappID := getCol(bappIdx)
		npsn := getCol(npsnIdx)
		serialNumber := getCol(snIdx)

		// Skip empty rows (if all three are empty)
		if bappID == "" && npsn == "" && serialNumber == "" {
			continue
		}

		results = append(results, map[string]string{
			"BAPP ID":       bappID,
			"NPSN":          npsn,
			"Serial Number": serialNumber,
		})

		_, err := db.Exec("INSERT INTO Schools (NPSN, SN, BAPP_ID, STATUS) VALUES (?, ?, ?, NULL)", npsn, serialNumber, bappID)
		if err != nil {
			log.Printf("Failed to insert data into Schools table: %v", err)
			http.Error(w, fmt.Sprintf("Failed to insert NPSN %s into database: %v", npsn, err), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Data extracted successfully.",
		"data":    results,
	})
}

// scrapeHandler
// @Summary Scrape DAC images
// @Description Scrapes images from DAC website for all schools with pending status in the database.
// @Tags dac
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 405 {string} string "Method Not Allowed"
// @Failure 500 {string} string "Internal Server Error"
// @Router /scrape [post]
func scrapeHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}

	go func() {
		// Run scraper in the background
		_, err := scraper.RunScraper(db, s3Client, bucketName)
		if err != nil {
			log.Printf("[Scraper Error] %v", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Scraping process has been started in the background.",
	})
}

// statsHandler
// @Summary Get scraping statistics
// @Description Returns total, done, and pending school counts plus optionally a list of all schools.
// @Tags dac
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/stats [get]
func statsHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	withDetail := r.URL.Query().Get("detail") == "true"

	type SchoolRow struct {
		ID        int            `json:"id"`
		NPSN      string         `json:"npsn"`
		SN        string         `json:"sn"`
		BAPPID    string         `json:"bapp_id"`
		Status    sql.NullString `json:"-"`
		StatusStr string         `json:"status"`
	}

	rows, err := db.Query("SELECT Id, NPSN, SN, BAPP_ID, STATUS FROM Schools ORDER BY Id ASC")
	if err != nil {
		http.Error(w, "Failed to query database.", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var schools []SchoolRow
	total, done := 0, 0

	for rows.Next() {
		var s SchoolRow
		if err := rows.Scan(&s.ID, &s.NPSN, &s.SN, &s.BAPPID, &s.Status); err != nil {
			http.Error(w, "Failed to scan row.", http.StatusInternalServerError)
			return
		}
		if s.Status.Valid {
			s.StatusStr = s.Status.String
		} else {
			s.StatusStr = "PENDING"
		}
		if s.StatusStr == "DONE" {
			done++
		}
		total++
		if withDetail {
			schools = append(schools, s)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	res := map[string]interface{}{
		"total":   total,
		"done":    done,
		"pending": total - done,
	}
	if withDetail {
		res["schools"] = schools
	}
	json.NewEncoder(w).Encode(res)
}

// logsHandler
// @Summary Get scraper logs
// @Description Returns recent system logs.
// @Tags dac
// @Produce json
// @Router /api/logs [get]
func logsHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	memLogger.Lock()
	defer memLogger.Unlock()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": memLogger.logs,
	})
}

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}

func detailHandler(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, detailHTML)
}

const commonHTMLHead = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>DAC Scraper Monitor</title>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:'Inter',sans-serif;background:#0f0f1a;color:#e0e0e0;min-height:100vh}
.bg-glow{position:fixed;top:-120px;left:-120px;width:400px;height:400px;background:radial-gradient(circle,rgba(99,102,241,.15),transparent 70%);pointer-events:none;z-index:0}
.bg-glow2{position:fixed;bottom:-120px;right:-120px;width:400px;height:400px;background:radial-gradient(circle,rgba(16,185,129,.1),transparent 70%);pointer-events:none;z-index:0}
.container{max-width:1100px;margin:0 auto;padding:32px 20px;position:relative;z-index:1}
h1{font-size:1.8rem;font-weight:700;margin-bottom:8px;background:linear-gradient(135deg,#818cf8,#34d399);-webkit-background-clip:text;-webkit-text-fill-color:transparent}
.subtitle{color:#888;font-size:.9rem;margin-bottom:24px}
.nav-links{margin-bottom:32px;display:flex;gap:16px}
.nav-links a{color:#a5b4fc;text-decoration:none;font-weight:500;font-size:.9rem;padding:6px 14px;background:rgba(129,140,248,.1);border-radius:8px;transition:all .2s;border:1px solid rgba(129,140,248,.2)}
.nav-links a:hover{background:rgba(129,140,248,.2);border-color:#818cf8}
.cards{display:grid;grid-template-columns:repeat(3,1fr);gap:20px;margin-bottom:28px}
.card{background:rgba(255,255,255,.04);border:1px solid rgba(255,255,255,.08);border-radius:16px;padding:24px 28px;backdrop-filter:blur(12px);transition:transform .2s,border-color .3s}
.card:hover{transform:translateY(-4px);border-color:rgba(255,255,255,.15)}
.card-label{font-size:.8rem;text-transform:uppercase;letter-spacing:.08em;color:#888;margin-bottom:8px;font-weight:600}
.card-value{font-size:2.4rem;font-weight:700}
.card-value.total{color:#818cf8}
.card-value.done{color:#34d399}
.card-value.pending{color:#fbbf24}
.progress-wrap{background:rgba(255,255,255,.06);border-radius:12px;height:18px;margin-bottom:32px;overflow:hidden;position:relative}
.progress-bar{height:100%;border-radius:12px;background:linear-gradient(90deg,#818cf8,#34d399);transition:width .6s ease;position:relative}
.progress-bar::after{content:attr(data-pct);position:absolute;right:10px;top:50%;transform:translateY(-50%);font-size:.7rem;font-weight:700;color:#fff}
.terminal{background:#000;border:1px solid rgba(255,255,255,.1);border-radius:12px;padding:16px;height:400px;overflow-y:auto;font-family:monospace;font-size:.8rem;color:#0f0;line-height:1.4}
.table-header{display:flex;justify-content:space-between;align-items:center;margin-bottom:14px}
.table-header h2{font-size:1.1rem;font-weight:600}
.filter-group{display:flex;gap:8px}
.filter-btn{background:rgba(255,255,255,.06);border:1px solid rgba(255,255,255,.1);color:#ccc;padding:6px 16px;border-radius:8px;cursor:pointer;font-size:.78rem;font-weight:500;transition:all .2s}
.filter-btn:hover,.filter-btn.active{background:rgba(129,140,248,.18);border-color:#818cf8;color:#a5b4fc}
table{width:100%;border-collapse:collapse}
thead th{text-align:left;font-size:.75rem;text-transform:uppercase;letter-spacing:.06em;color:#666;padding:10px 14px;border-bottom:1px solid rgba(255,255,255,.06)}
tbody td{padding:12px 14px;font-size:.88rem;border-bottom:1px solid rgba(255,255,255,.04)}
tbody tr{transition:background .15s}
tbody tr:hover{background:rgba(255,255,255,.03)}
.badge{display:inline-block;padding:3px 12px;border-radius:20px;font-size:.75rem;font-weight:600}
.badge-done{background:rgba(52,211,153,.12);color:#34d399}
.badge-pending{background:rgba(251,191,36,.12);color:#fbbf24}
.badge-failed{background:rgba(239,68,68,.12);color:#ef4444}
.refresh-note{text-align:center;color:#555;font-size:.75rem;margin-top:20px}
@media(max-width:680px){.cards{grid-template-columns:1fr}}
</style>
</head>
<body>
<div class="bg-glow"></div><div class="bg-glow2"></div>
`

const dashboardHTML = commonHTMLHead + `<div class="container">
  <h1>DAC Scraper Dashboard</h1>
  <p class="subtitle">Real-time scraping progress monitor</p>

  <div class="nav-links">
    <a href="/dashboard">Dashboard (Logs)</a>
    <a href="/detail">Detail Data</a>
  </div>

  <div class="cards">
    <div class="card"><div class="card-label">Total Schools</div><div class="card-value total" id="total">-</div></div>
    <div class="card"><div class="card-label">Done</div><div class="card-value done" id="done">-</div></div>
    <div class="card"><div class="card-label">Remaining</div><div class="card-value pending" id="pending">-</div></div>
  </div>

  <div class="progress-wrap"><div class="progress-bar" id="pbar" style="width:0%" data-pct="0%"></div></div>

  <div class="table-header">
    <h2>Live Logs</h2>
  </div>
  <div class="terminal" id="terminal">Initializing...</div>

  <p class="refresh-note">Auto-refreshes every 2 seconds</p>
</div>
<script>
let scrapeFired = false;
async function fireScrape(){
  if(scrapeFired) return;
  try{
    await fetch('/scrape', {method: 'POST'});
    scrapeFired = true;
  }catch(e){console.error('Failed to trigger scrape', e)}
}

async function loadStats(){
  try{
    const r=await fetch('/api/stats');
    const d=await r.json();
    document.getElementById('total').textContent=d.total;
    document.getElementById('done').textContent=d.done;
    document.getElementById('pending').textContent=d.pending;
    const pct=d.total?Math.round(d.done/d.total*100):0;
    const bar=document.getElementById('pbar');
    bar.style.width=pct+'%';
    bar.dataset.pct=pct+'%';
  }catch(e){}
}

async function loadLogs(){
  try{
    const r=await fetch('/api/logs');
    const d=await r.json();
    const term = document.getElementById('terminal');
    if(d.logs && d.logs.length > 0){
      term.innerHTML = d.logs.join('').replace(/\n/g, '<br>');
      term.scrollTop = term.scrollHeight; // auto-scroll
    }
  }catch(e){}
}

async function poll(){
  await loadStats();
  await loadLogs();
}

fireScrape().then(()=>{
  poll();
  setInterval(poll, 2000);
});
</script>
</body>
</html>`

const detailHTML = commonHTMLHead + `<div class="container">
  <h1>Scraped Data Details</h1>
  <p class="subtitle">Detailed view of individual schools</p>

  <div class="nav-links">
    <a href="/dashboard">Dashboard (Logs)</a>
    <a href="/detail">Detail Data</a>
  </div>

  <div class="table-header">
    <h2>Schools</h2>
    <div class="filter-group">
      <button class="filter-btn active" data-f="ALL">All</button>
      <button class="filter-btn" data-f="DONE">Done</button>
      <button class="filter-btn" data-f="PENDING">Pending</button>
      <button class="filter-btn" data-f="FAILED">Failed</button>
    </div>
  </div>
  <table>
    <thead><tr><th>#</th><th>NPSN</th><th>SN</th><th>BAPP ID</th><th>Status</th></tr></thead>
    <tbody id="tbody"><tr><td colspan="5">Loading...</td></tr></tbody>
  </table>
</div>
<script>
let allSchools=[];
let currentFilter="ALL";

document.querySelectorAll('.filter-btn').forEach(b=>{
  b.addEventListener('click',()=>{
    document.querySelectorAll('.filter-btn').forEach(x=>x.classList.remove('active'));
    b.classList.add('active');
    currentFilter=b.dataset.f;
    renderTable();
  });
});

function badgeClass(s){
  if(s==='DONE')return'badge-done';
  if(s==='FAILED')return'badge-failed';
  return'badge-pending';
}

function renderTable(){
  const tb=document.getElementById('tbody');
  const filtered=currentFilter==='ALL'?allSchools:allSchools.filter(s=>s.status===currentFilter);
  tb.innerHTML=filtered.map((s,i)=>'<tr><td>'+(i+1)+'</td><td>'+s.npsn+'</td><td>'+s.sn+'</td><td>'+s.bapp_id+'</td><td><span class="badge '+badgeClass(s.status)+'">'+s.status+'</span></td></tr>').join('');
}

async function load(){
  try{
    const r=await fetch('/api/stats?detail=true');
    const d=await r.json();
    allSchools=d.schools||[];
    renderTable();
  }catch(e){console.error(e)}
}
load();
</script>
</body>
</html>`

func main() {
	initAWS()
	initDB()
	testS3Connection()

	// Redirect standard log to both stdout and memLogger
	log.SetOutput(io.MultiWriter(os.Stdout, memLogger))

	if host := os.Getenv("SWAGGER_HOST"); host != "" {
		docs.SwaggerInfo.Host = host
	}

	http.HandleFunc("/", statusHandler)
	http.HandleFunc("/send", sendHandler)
	http.HandleFunc("/get", getHandler)
	http.HandleFunc("/delete", deleteHandler)
	http.HandleFunc("/download", downloadHandler)
	http.HandleFunc("/scrape", scrapeHandler)
	http.HandleFunc("/api/stats", statsHandler)
	http.HandleFunc("/api/logs", logsHandler)
	http.HandleFunc("/dashboard", dashboardHandler)
	http.HandleFunc("/detail", detailHandler)
	http.HandleFunc("/docs/", httpSwagger.WrapHandler)

	log.Println("Server is listening on port 8080.")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
