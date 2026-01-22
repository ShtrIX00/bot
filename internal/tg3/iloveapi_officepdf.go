package tg3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"TGBOT2/internal/config"
)

type iloveAuthResp struct {
	Token string `json:"token"`
}

type iloveStartResp struct {
	Server string `json:"server"`
	Task   string `json:"task"`
}

type iloveUploadResp struct {
	ServerFilename string `json:"server_filename"`
}

type iloveProcessReq struct {
	Task  string             `json:"task"`
	Tool  string             `json:"tool"`
	Files []iloveProcessFile `json:"files"`
}
type iloveProcessFile struct {
	ServerFilename string `json:"server_filename"`
	Filename       string `json:"filename"`
}

func ConvertXLSXToPDF(cfg *config.Config, xlsxPath string) (string, error) {
	if cfg == nil || cfg.ILoveAPIPublicKey == "" {
		return "", fmt.Errorf("ILOVEAPI_PUBLIC_KEY is not set")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 1) AUTH -> token
	token, err := iloveAuth(ctx, client, cfg.ILoveAPIPublicKey)
	if err != nil {
		return "", err
	}

	// 2) START officepdf -> server + task
	server, task, err := iloveStart(ctx, client, token, cfg.ILoveAPIRegion)
	if err != nil {
		return "", err
	}

	// 3) UPLOAD xlsx
	serverFilename, err := iloveUpload(ctx, client, token, server, task, xlsxPath)
	if err != nil {
		return "", err
	}

	// 4) PROCESS tool=officepdf
	origName := filepath.Base(xlsxPath)
	if err := iloveProcess(ctx, client, token, server, task, serverFilename, origName); err != nil {
		return "", err
	}

	// 5) DOWNLOAD result bytes (for single file -> pdf directly) :contentReference[oaicite:1]{index=1}
	pdfBytes, err := iloveDownload(ctx, client, token, server, task)
	if err != nil {
		return "", err
	}

	outPath := filepath.Join(os.TempDir(), origName[:len(origName)-len(filepath.Ext(origName))]+".pdf")
	if err := os.WriteFile(outPath, pdfBytes, 0o644); err != nil {
		return "", fmt.Errorf("write pdf: %w", err)
	}
	return outPath, nil
}

func iloveAuth(ctx context.Context, client *http.Client, publicKey string) (string, error) {
	body, _ := json.Marshal(map[string]string{"public_key": publicKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.ilovepdf.com/v1/auth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ilove auth failed: %s (%s)", resp.Status, string(b))
	}

	var out iloveAuthResp
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("ilove auth: empty token")
	}
	return out.Token, nil
}

func iloveStart(ctx context.Context, client *http.Client, token, region string) (server, task string, err error) {
	if region == "" {
		region = "eu"
	}
	url := fmt.Sprintf("https://api.ilovepdf.com/v1/start/officepdf/%s", region) // :contentReference[oaicite:2]{index=2}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("ilove start failed: %s (%s)", resp.Status, string(b))
	}

	var out iloveStartResp
	if err := json.Unmarshal(b, &out); err != nil {
		return "", "", err
	}
	if out.Server == "" || out.Task == "" {
		return "", "", fmt.Errorf("ilove start: empty server/task")
	}
	return out.Server, out.Task, nil
}

func iloveUpload(ctx context.Context, client *http.Client, token, server, task, filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	_ = w.WriteField("task", task)

	fw, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	_ = w.Close()

	url := fmt.Sprintf("https://%s/v1/upload", server) // :contentReference[oaicite:3]{index=3}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ilove upload failed: %s (%s)", resp.Status, string(b))
	}

	var out iloveUploadResp
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if out.ServerFilename == "" {
		return "", fmt.Errorf("ilove upload: empty server_filename")
	}
	return out.ServerFilename, nil
}

func iloveProcess(ctx context.Context, client *http.Client, token, server, task, serverFilename, filename string) error {
	reqBody := iloveProcessReq{
		Task: task,
		Tool: "officepdf",
		Files: []iloveProcessFile{
			{ServerFilename: serverFilename, Filename: filename},
		},
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("https://%s/v1/process", server) // :contentReference[oaicite:4]{index=4}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ilove process failed: %s (%s)", resp.Status, string(b))
	}
	return nil
}

func iloveDownload(ctx context.Context, client *http.Client, token, server, task string) ([]byte, error) {
	url := fmt.Sprintf("https://%s/v1/download/%s", server, task) // :contentReference[oaicite:5]{index=5}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ilove download failed: %s (%s)", resp.Status, string(b))
	}
	return b, nil
}
