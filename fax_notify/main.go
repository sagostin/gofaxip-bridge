package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
)

const timeLayout = "2006-01-02 15:04:05"
const lastRunFile = "last_run.txt"
const interval = 2 * time.Minute
const firstRun = 10 * time.Minute

type QFileData struct {
	SrcNum     string `json:"src_num"`
	SrcCid     string `json:"src_cid"`
	DestNum    string `json:"dest_num"`
	DestCid    string `json:"dest_cid"`
	Pages      int    `json:"total_pages"`
	TotalDials int    `json:"total_dials"`
	TotalTries int    `json:"total_tries"`
	JobID      int    `json:"job_id"`
	Status     string `json:"status"`
	Why        string `json:"why"`
	TiffPath   string `json:"tiff_path"`
}

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Starting fax_notify")
	for {
		// Get the last run time from file
		sinceTime := getLastRunTime()

		// Run the journalctl command and parse the output
		log.Info("Running journalctl")
		output := runJournalctl(sinceTime)
		parseOutput(output)

		// Update the last run time to the current time
		updateLastRunTime()

		// Wait for 2 minutes before the next run
		time.Sleep(interval)
	}
}

func getLastRunTime() time.Time {
	content, err := ioutil.ReadFile(lastRunFile)
	if err != nil {
		return time.Now().Add(-firstRun)
	}

	lastRunStr := string(content)
	lastRunTime, err := time.Parse(timeLayout, lastRunStr)
	if err != nil {
		return time.Now().Add(-firstRun)
	}
	return lastRunTime
}

func updateLastRunTime() {
	currentTime := time.Now().Format(timeLayout)
	err := ioutil.WriteFile(lastRunFile, []byte(currentTime), 0644)
	if err != nil {
		fmt.Println("Error updating last run time:", err)
	}
}

func runJournalctl(sinceTime time.Time) string {
	cmd := exec.Command("journalctl", "--no-pager", "-u", "faxq", "--since", sinceTime.Format(timeLayout))
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error running journalctl:", err)
		return ""
	}
	return string(output)
}

func parseOutput(output string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "NOTIFY: bin/notify") {
			qfile, why := extractInfo(line)

			log.Info("qfile: " + qfile + " why: " + why)

			if !(why == "rejected" || why == "removed" || why == "killed" || why == "requeued") {
				continue
			}
			filePath := os.Getenv("BASE_HYLAFAX_PATH") + qfile

			log.Info("filePath: " + filePath)

			qfileContents, err := readQfile(filePath)
			if err != nil {
				log.Errorf("Error reading qfile: %s", err)
				continue
			}

			qfileContents.Why = why

			err = sendWebhook(qfileContents)
			if err != nil {
				log.Error("Error sending webhook:", err)
			} else {
				log.Info("Webhook sent successfully")
			}
			//}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Error("Error scanning output:", err)
	}
}

func extractInfo(line string) (string, string) {
	re := regexp.MustCompile(`NOTIFY: bin/notify "([^"]*)" "([^"]*)"`)
	matches := re.FindStringSubmatch(line)
	if len(matches) >= 3 {
		return matches[1], matches[2]
	}
	return "", ""
}

func readQfile(filename string) (QFileData, error) {
	var data QFileData

	qfile, err := OpenQfile(filename)
	if err != nil {
		return data, err
	}

	totPages, _ := qfile.GetInt("totpages")
	totTries, _ := qfile.GetInt("tottries")
	totDials, _ := qfile.GetInt("totdials")
	jobID, _ := qfile.GetInt("jobid")

	data = QFileData{
		SrcNum:     qfile.GetString("owner"),
		SrcCid:     qfile.GetString("tsi"),
		DestNum:    qfile.GetString("number"),
		DestCid:    qfile.GetString("external"),
		Pages:      totPages,
		TotalDials: totDials,
		TotalTries: totTries,
		Status:     qfile.GetString("status"),
		JobID:      jobID,
		TiffPath:   extractTiffPath(qfile),
	}

	return data, nil
}

func extractTiffPath(qfile *Qfile) string {
	tiffLine := qfile.GetString("!tiff")
	log.Info("Raw tiff line: " + tiffLine)

	if tiffLine == "" {
		log.Warn("No !tiff tag found in qfile")
		// Dump all params for debugging
		for _, param := range qfile.params {
			log.Info(fmt.Sprintf("Tag: %s, Value: %s", param.Tag, param.Value))
		}
		return ""
	}

	// Remove the leading "0::" and any trailing quote
	tiffPath := strings.TrimPrefix(tiffLine, "0::")
	tiffPath = strings.TrimSuffix(tiffPath, "\"")

	log.Info("Extracted tiff path: " + tiffPath)

	fullPath := filepath.Join(os.Getenv("BASE_HYLAFAX_PATH"), strings.TrimSpace(tiffPath))
	log.Info("Constructed full tiff path: " + fullPath)

	return fullPath
}

func convertTiffToPdf(qfile QFileData, inputPath string) (string, error) {
	log.Info("Converting TIFF to PDF and extracting first page, input path: " + inputPath)

	// Check if the file exists
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("TIFF file does not exist: %s", inputPath)
	}

	// Create temporary paths for intermediate and final PDFs
	tempDir := os.TempDir()
	//fullPdfPath := filepath.Join(tempDir, fmt.Sprintf("full_%d.pdf", time.Now().UnixNano()))
	finalPdfPath := filepath.Join(tempDir, fmt.Sprintf("first_page__%d_%s_%s.pdf", time.Now().UnixNano(), qfile.SrcNum, qfile.DestNum))

	// Step 1: Convert entire TIFF to PDF
	cmd := exec.Command("convert",
		"-density", "300",
		"-compress", "lzw",
		"-quality", "100",
		"-background", "white",
		"-alpha", "remove",
		inputPath+"[0]",
		"-resize", "2550x3300>",
		finalPdfPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to convert TIFF to PDF: %v, output: %s", err, string(output))
	}

	log.Info("Successfully converted TIFF to PDF and extracted first page, output path: " + finalPdfPath)
	return finalPdfPath, nil
}

func WriteQFileDataFields(writer *multipart.Writer, data QFileData) error {
	fields := []struct {
		name  string
		value string
	}{
		{"src_num", data.SrcNum},
		{"src_cid", data.SrcCid},
		{"dest_num", data.DestNum},
		{"dest_cid", data.DestCid},
		{"total_pages", strconv.Itoa(data.Pages)},
		{"total_dials", strconv.Itoa(data.TotalDials)},
		{"total_tries", strconv.Itoa(data.TotalTries)},
		{"job_id", strconv.Itoa(data.JobID)},
		{"status", data.Status},
		{"why", data.Why},
		{"tiff_path", data.TiffPath},
	}

	for _, field := range fields {
		err := writer.WriteField(field.name, field.value)
		if err != nil {
			return err
		}
	}

	return nil
}

func sendWebhook(data QFileData) error {
	webhookURL := os.Getenv("WEBHOOK_URL")
	username := os.Getenv("WEBHOOK_USERNAME")
	password := os.Getenv("WEBHOOK_PASSWORD")

	client := &http.Client{}

	// Prepare multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add JSON data
	err := WriteQFileDataFields(writer, data)
	if err != nil {
		return err
	}

	// Convert TIFF to PDF (only first page)
	pdfPath, err := convertTiffToPdf(data, data.TiffPath)
	if err == nil {
		defer func(name string) {
			err := os.Remove(name)
			if err != nil {
				log.Error(err)
			}
		}(pdfPath)

		// Add PDF file
		file, err := os.Open(pdfPath)
		if err != nil {
			return err
		}
		defer func(file *os.File) {
			err := file.Close()
			if err != nil {
				log.Error(err)
			}
		}(file)

		part, err := writer.CreateFormFile("pdf_file", filepath.Base(pdfPath))
		if err != nil {
			return err
		}
		_, err = io.Copy(part, file)
		if err != nil {
			return err
		}
	} else {
		log.Error(err)
	}

	err = writer.Close()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Error(err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook request failed with status code: %d", resp.StatusCode)
	}

	return nil
}

// OpenQfile and related functions should be implemented here
// This part is missing from the provided code, so you'll need to add it
