package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/jung-kurt/gofpdf"
	log "github.com/sirupsen/logrus"
	"golang.org/x/image/tiff"
	"image/jpeg"
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

			//if why == "rejected" || why == "removed" || why == "killed" {
			// skip checking, and handle checking on n8n, for testing purposes.

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
	tiffLine := qfile.GetString("!tiff:0")
	parts := strings.Split(tiffLine, "::")
	log.Info(tiffLine)
	log.Warn(os.Getenv("BASE_HYLAFAX_PATH"))

	if len(parts) > 1 {
		return filepath.Join(os.Getenv("BASE_HYLAFAX_PATH"), parts[1])
	}
	return ""
}

func convertTiffToPdf(inputPath string) (string, error) {
	// Open the TIFF file
	log.Warn("tiff file path: " + inputPath)
	tiffFile, err := os.Open(inputPath)
	if err != nil {
		return "", err
	}
	defer func(tiffFile *os.File) {
		err := tiffFile.Close()
		if err != nil {
			log.Error(err)
		}
	}(tiffFile)

	// Decode the TIFF image (only the first page)
	tiffImage, err := tiff.Decode(tiffFile)
	if err != nil {
		return "", err
	}

	// Create a new PDF
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	// Convert TIFF to JPEG (in memory)
	jpegBuffer := new(bytes.Buffer)
	err = jpeg.Encode(jpegBuffer, tiffImage, nil)
	if err != nil {
		return "", err
	}

	// Add the JPEG to the PDF
	imageOptions := gofpdf.ImageOptions{ImageType: "JPEG", ReadDpi: true}
	pdf.RegisterImageOptionsReader("tiff_image", imageOptions, jpegBuffer)
	pdf.Image("tiff_image", 10, 10, 190, 0, false, "", 0, "")

	// Save the PDF to a temporary file
	outputPath := filepath.Join(os.TempDir(), fmt.Sprintf("converted_%d.pdf", time.Now().UnixNano()))
	err = pdf.OutputFileAndClose(outputPath)
	if err != nil {
		return "", err
	}

	return outputPath, nil
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
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = writer.WriteField("json_data", string(jsonData))
	if err != nil {
		return err
	}

	// Convert TIFF to PDF (only first page)
	pdfPath, err := convertTiffToPdf(data.TiffPath)
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
