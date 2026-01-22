package tg3

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"TGBOT2/internal/config"
)

var libreOfficeMu sync.Mutex

func ConvertXLSXToPDFLibreOffice(cfg *config.Config, xlsxPath, outDir string) (string, error) {
	if strings.TrimSpace(xlsxPath) == "" {
		return "", fmt.Errorf("xlsxPath is empty")
	}
	if strings.TrimSpace(outDir) == "" {
		outDir = os.TempDir()
	}

	libreOfficeMu.Lock()
	defer libreOfficeMu.Unlock()

	inAbs, err := filepath.Abs(xlsxPath)
	if err != nil {
		return "", fmt.Errorf("abs input: %w", err)
	}

	base := filepath.Base(inAbs)
	pdfName := strings.TrimSuffix(base, filepath.Ext(base)) + ".pdf"
	outPDF := filepath.Join(outDir, pdfName)

	_ = os.Remove(outPDF)

	soffice := "soffice"
	if cfg != nil && strings.TrimSpace(cfg.SofficePath) != "" {
		soffice = cfg.SofficePath
	}

	cmd := exec.Command(
		soffice,
		"--headless",
		"--nologo",
		"--nofirststartwizard",
		"--convert-to", "pdf",
		"--outdir", outDir,
		inAbs,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("soffice failed: %w; stderr=%s; stdout=%s", err, stderr.String(), stdout.String())
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("soffice timeout; stderr=%s; stdout=%s", stderr.String(), stdout.String())
	}

	if _, err := os.Stat(outPDF); err != nil {
		return "", fmt.Errorf("pdf not created: %w; stderr=%s; stdout=%s", err, stderr.String(), stdout.String())
	}

	return outPDF, nil
}
