<div align="center">

# go-api-s3

Simple HTTP API for uploading and downloading files to an S3-compatible bucket.

![Go Version](https://img.shields.io/badge/Go-1.20%2B-00ADD8?logo=go&logoColor=white)
![Storage](https://img.shields.io/badge/Storage-S3%20compatible-1a8917)
![Docs](https://img.shields.io/badge/API-OpenAPI%2FSwagger-blue)

</div>

---

## ✨ Features

- Health check endpoint (`GET /`) returning JSON status.
- File upload to S3 (`POST /send`) via multipart form-data.
- File download from S3 (`GET /get`) as an octet-stream.
- Auto-loaded environment variables from `.env` (using `godotenv`).
- Interactive Swagger UI served at `/docs/`.

## 🧱 Tech Stack

- Go (standard `net/http`)
- AWS SDK v2 for S3
- `godotenv` for environment loading
- `swag` + `http-swagger` for API documentation

## 🚀 Quick Start

1. Clone the repository and enter the folder.
2. Copy your environment variables into a `.env` file (see below).
3. Run the server:

   ```bash
   go run ./...
   ```

4. Open the endpoints:
   - API base URL: `http://localhost:8080`
   - Swagger UI: `http://localhost:8080/docs/index.html`

> The S3 bucket name is currently hard-coded as `digitsd` in `main.go`.

## ⚙️ Configuration

The service reads configuration from the environment. A sample file is provided as `.env.example`.

| Variable                | Required | Description                                                                         |
| ----------------------- | -------- | ----------------------------------------------------------------------------------- |
| `AWS_ACCESS_KEY_ID`     | Yes      | Access key for S3                                                                   |
| `AWS_SECRET_ACCESS_KEY` | Yes      | Secret key for S3                                                                   |
| `AWS_REGION`            | Yes      | AWS region (e.g. `us-east-1`)                                                       |
| `AWS_ENDPOINT_URL`      | No       | Custom S3-compatible endpoint (e.g. MinIO). When set, path-style addressing is used |

To get started quickly, copy the example file and fill in your values:

```bash
cp .env.example .env
```

The `.env` file in the project root will be loaded automatically on startup.

## 📡 API Overview

### Health check

- **Method:** `GET`
- **Path:** `/`
- **Response:**
  - `200 OK` – JSON object with `status` and `message` fields.

### Upload file

- **Method:** `POST`
- **Path:** `/send`
- **Body:** `multipart/form-data`
  - `folder` (string, required) – Target folder inside the bucket.
  - `file` (file, required) – File to upload.
- **Responses:**
  - `200 OK` – JSON with `status`, `message`, and `path` of the uploaded file.
  - `400 Bad Request` – Invalid/missing form data.
  - `405 Method Not Allowed` – Wrong HTTP method.
  - `500 Internal Server Error` – Upload failed.

### Download file

- **Method:** `GET`
- **Path:** `/get`
- **Query parameters:**
  - `folder` (string, required) – Folder inside the bucket.
  - `file` (string, required) – File name to download.
- **Responses:**
  - `200 OK` – File content as `application/octet-stream`.
  - `400 Bad Request` – Missing query parameters.
  - `404 Not Found` – File not found in S3.
  - `405 Method Not Allowed` – Wrong HTTP method.
  - `500 Internal Server Error` – Download failed.

## 💡 Usage Examples

### Health check

```bash
curl http://localhost:8080/
```

### Upload file

```bash
curl -X POST \
  -F "folder=uploads" \
  -F "file=@/path/to/your/file.txt" \
  http://localhost:8080/send
```

### Download file

```bash
curl -G \
  --data-urlencode "folder=uploads" \
  --data-urlencode "file=file.txt" \
  -o file.txt \
  http://localhost:8080/get
```

## 📘 Swagger Documentation

Swagger docs are generated with `swag` based on annotations in `main.go` and served via `http-swagger`.

To regenerate the docs after changing handlers or comments:

```bash
swag init
```

Then start the server and open:

- Swagger UI: `http://localhost:8080/docs/index.html`

## 📂 Project Structure

```text
.
├── main.go        # HTTP server, handlers, and S3 integration
├── docs/          # Generated Swagger docs
├── tmp/           # Temporary build artifacts (if any)
└── README.md      # This file
```
