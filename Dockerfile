# --- STAGE 1: Build (Si Tukang Masak) ---
FROM golang:1.25-alpine AS builder

# Tentukan direktori kerja di dalam container
WORKDIR /app

# Instal git dan dependensi sistem jika diperlukan
RUN apk add --no-cache git

# Copy file dependency dulu agar proses build lebih cepat (caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy semua source code project lu ke dalam container
COPY . .

# Compile kode Go menjadi binary bernama "app"
# CGO_ENABLED=0 supaya binary-nya statik (bisa jalan di mana saja tanpa library tambahan)
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main .

# --- STAGE 2: Run (Si Penyaji) ---
# Kita pakai alpine supaya sizenya kecil (cuma ~5MB) dibanding pake image golang asli (~300MB)
FROM alpine:latest  

# CA certificates buat HTTPS (AWS, MySQL SSL, scraper)
RUN apk add --no-cache ca-certificates

# Non-root user buat security
RUN adduser -D -h /home/appuser appuser
WORKDIR /home/appuser
USER appuser

# Ambil hasil compile (binary) dari stage builder tadi
COPY --from=builder /app/main .

# Port yang dibuka (sesuai port HTTP server lu)
EXPOSE 8080

# Jalankan aplikasinya
CMD ["./main"]